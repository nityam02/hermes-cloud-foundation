package cli

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/internal/diagnostics"
	"github.com/nityam02/hermes-cloud-foundation/internal/providers"
)

//go:embed templates/*.yaml.tmpl
var templatesFS embed.FS

// Templates offered by `hermes init`.
var Templates = []string{"basic", "containerized", "database", "autonomous-logistics"}

var defaultRegions = map[string]string{
	config.ProviderAWS: "eu-north-1", config.ProviderGCP: "europe-north1", config.ProviderAlibaba: "eu-central-1",
}

var projectNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,28}[a-z0-9]$`)

type templateData struct {
	Name, Provider, Region, Account, AccountHint, Bucket, Extensions string
}

// RenderTemplate returns the spec text for a template (exported for tests and docs).
func RenderTemplate(name, project, provider, region, account string) ([]byte, error) {
	src, err := templatesFS.ReadFile("templates/" + name + ".yaml.tmpl")
	if err != nil {
		return nil, fmt.Errorf("unknown template %q (available: %s)", name, strings.Join(Templates, ", "))
	}
	if region == "" {
		region = defaultRegions[provider]
	}
	d := templateData{Name: project, Provider: provider, Region: region, Account: account, Bucket: project + "-hermes-state"}
	d.AccountHint = map[string]string{
		config.ProviderAWS:     "12-digit AWS account ID (rendered as allowed_account_ids)",
		config.ProviderGCP:     "GCP project ID (or export GOOGLE_CLOUD_PROJECT outside protected environments)",
		config.ProviderAlibaba: "Alibaba Cloud account ID (optional)",
	}[provider]
	usesSecrets := name != "basic" && name != "database"
	switch provider {
	case config.ProviderAWS:
		d.Extensions = "# extensions:\n#   aws:\n#     acm_certificate_arn: arn:aws:acm:<region>:<account>:certificate/<id>   # required for public services in protected environments\n"
	case config.ProviderAlibaba:
		d.Extensions = "extensions:\n  alibaba:\n"
		if usesSecrets {
			d.Extensions += "    kms_instance_id: kst-REPLACE-ME          # KMS 3.0 instance; Alibaba secrets live inside it\n"
		}
		d.Extensions += "    # certificate_id: <id>                   # required for public services in protected environments\n" +
			"    # state_tablestore_endpoint / state_tablestore_table: printed by `hermes state bootstrap` (state locking)\n"
		if !usesSecrets {
			d.Extensions = "# " + strings.ReplaceAll(strings.TrimSuffix(d.Extensions, "\n"), "\n", "\n# ") + "\n"
		}
	}
	var out bytes.Buffer
	if err := template.Must(template.New(name).Option("missingkey=error").Parse(string(src))).Execute(&out, d); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func newInitCmd(app *App) *cobra.Command {
	var name, provider, region, account, tmpl string
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create hermes.yaml from a template and add .hermes/ to .gitignore",
		Long: `Init writes a starter spec (non-interactive). It never overwrites an existing
file without --force, and it adds .hermes/ (generated roots, saved plans, history) to
.gitignore. Values marked REPLACE-ME are placeholders you must change.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !projectNameRe.MatchString(name) {
				return Refused("--name %q must match ^[a-z][a-z0-9-]{1,28}[a-z0-9]$", name)
			}
			if _, ok := providers.Get(provider); !ok {
				return Refused("--provider must be one of %s", strings.Join(config.Providers, ", "))
			}
			data, err := RenderTemplate(tmpl, name, provider, region, account)
			if err != nil {
				return Refused("%v", err)
			}
			// The template must produce a valid default environment; anything else is a bug.
			doc, ds, err := config.Parse(data, "hermes.yaml")
			if err != nil {
				return err
			}
			if doc != nil && !ds.HasErrors() {
				for _, env := range doc.Environments() {
					p, rds, err := doc.Resolve(env)
					if err != nil {
						return err
					}
					ds = append(ds, rds...)
					if p != nil {
						a, _ := providers.Get(p.Cloud.Provider)
						vds := a.Validate(p)
						if env != "dev" {
							vds = downgrade(vds) // production needs values only the user has (certificates, account)
						}
						ds = append(ds, vds...)
					}
				}
			}
			if ds.HasErrors() {
				return a2err(app, ds)
			}
			path := app.configPath()
			if _, err := os.Stat(path); err == nil && !force {
				return Refused("%s already exists (use --force to overwrite)", path)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // G306: hermes.yaml is a project file meant to be committed and shared
				return err
			}
			added, err := ensureGitignore(app.workDir())
			if err != nil {
				return err
			}
			fmt.Fprintf(app.Stdout, "Wrote %s (template %s, provider %s).\n", path, tmpl, provider)
			if added {
				fmt.Fprintln(app.Stdout, "Added .hermes/ to .gitignore.")
			}
			for _, d := range ds.Sorted() {
				if d.Severity != diag.SeverityInfo {
					fmt.Fprintf(app.Stdout, "  %s\n", d)
				}
			}
			fmt.Fprintln(app.Stdout, "Next: replace REPLACE-ME values, then `hermes validate`, `hermes state bootstrap`, `hermes plan`.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "project name (required)")
	f.StringVar(&provider, "provider", config.ProviderAWS, "cloud provider: aws, gcp or alibaba")
	f.StringVar(&region, "region", "", "region (default: a provider-specific example region)")
	f.StringVar(&account, "account", "", "account ID / project ID (recommended)")
	f.StringVar(&tmpl, "template", "basic", "template: "+strings.Join(Templates, ", "))
	f.BoolVar(&force, "force", false, "overwrite an existing spec")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func init() { Register(newInitCmd) }

func downgrade(ds diag.Diagnostics) diag.Diagnostics {
	out := make(diag.Diagnostics, len(ds))
	for i, d := range ds {
		if d.Severity == diag.SeverityError {
			d.Severity = diag.SeverityWarning
			d.Message = "before deploying this environment: " + d.Message
		}
		out[i] = d
	}
	return out
}

func a2err(app *App, ds diag.Diagnostics) error {
	return app.diagRefusal(ds, "internal error: the template does not validate")
}

// ensureGitignore adds .hermes/ to dir/.gitignore unless it is already ignored.
func ensureGitignore(dir string) (bool, error) {
	if diagnostics.GitignoreCovers(dir) {
		return false, nil
	}
	path := filepath.Join(dir, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		b.WriteByte('\n')
	}
	b.WriteString("# Hermes working directory (rendered roots, saved plans, history)\n.hermes/\n")
	return true, os.WriteFile(path, []byte(b.String()), 0o644) //nolint:gosec // G306: .gitignore is a project file meant to be committed and shared
}
