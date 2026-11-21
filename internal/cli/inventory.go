package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nityam02/hermes-cloud-foundation/internal/graph"
	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
	awsinv "github.com/nityam02/hermes-cloud-foundation/internal/inventory/aws"
)

func init() { Register(newInventoryCmd) }

// migDiscoverAWS is replaceable in tests (no real AWS calls in tests).
var migDiscoverAWS = func(ctx context.Context, so awsinv.SessionOptions, o awsinv.Options) (*inventory.Inventory, error) {
	cfg, err := awsinv.NewSession(ctx, so)
	if err != nil {
		return nil, err
	}
	d := &awsinv.Discoverer{Clients: awsinv.NewClientFactory(cfg), Options: o}
	return d.Run(ctx)
}

func newInventoryCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inventory",
		Short: "Read-only, agentless discovery of source resources (no IaC state needed)",
		Long: `Discover source cloud resources directly through official APIs. Discovery is read-only
(an operation allowlist is enforced before any request is sent), agentless, and works for
environments built by hand: Terraform state and CloudFormation stacks only annotate managed_by.`,
	}
	cmd.AddCommand(newInventoryAWSCmd(app), newInventoryImportCmd(app), newInventoryGraphCmd(app), newInventoryPolicyCmd(app))
	return cmd
}

func newInventoryAWSCmd(app *App) *cobra.Command {
	var (
		so          awsinv.SessionOptions
		regions     []string
		services    []string
		ccTypes     []string
		tfState     []string
		concurrency int
		redact      bool
		out         string
		opsOut      string
		timeout     time.Duration
	)
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Discover an AWS account through read-only AWS APIs",
		Example: `  hermes inventory aws --role-arn arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly \
      --external-id <id> --regions eu-north-1 --out inventory.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if so.RoleARN != "" && so.ExternalID == "" {
				fmt.Fprintln(app.Stderr, "warning: --role-arn without --external-id; a cross-account discovery role should require sts:ExternalId (see migration/discovery/README.md)")
			}
			if so.ExternalID != "" && so.RoleARN == "" {
				return Refused("--external-id requires --role-arn")
			}
			rec := awsinv.NewRecorder()
			so.Recorder = rec
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			inv, err := migDiscoverAWS(ctx, so, awsinv.Options{
				HomeRegion: so.Region, Regions: migSplit(regions), Services: migSplit(services), Concurrency: concurrency,
				CloudControlTypes: migSplit(ccTypes), RoleARN: so.RoleARN, ExternalIDUsed: so.ExternalID != "",
				Generator: "hermes/" + app.Build.Version, Now: migNow(app),
			})
			if err != nil {
				return err
			}
			if len(tfState) > 0 {
				n, err := inventory.AnnotateFromTerraformState(inv, tfState)
				if err != nil {
					return err
				}
				inv.Metadata.Notes = append(inv.Metadata.Notes, fmt.Sprintf("%d resource(s) annotated managed_by=terraform from %d state file(s)", n, len(tfState)))
				inv.Finalize()
			}
			if redact {
				inventory.Redact(inv)
			}
			if opsOut != "" {
				b, _ := json.MarshalIndent(rec.Operations(), "", "  ")
				if err := os.WriteFile(opsOut, append(b, '\n'), 0o600); err != nil {
					return err
				}
			}
			return migEmitInventory(app, inv, out)
		},
	}
	f := cmd.Flags()
	f.StringVar(&so.Profile, "profile", "", "shared config profile (default: the default credential chain)")
	f.StringVar(&so.Region, "region", "us-east-1", "home region for STS and global services (IAM, Route 53, CloudFront, S3 listing)")
	f.StringSliceVar(&regions, "regions", nil, "regions to scan (default: enabled regions from ec2:DescribeRegions)")
	f.StringSliceVar(&services, "services", nil, "services to scan (default: all): "+strings.Join(awsinv.ServiceNames(), ","))
	f.StringVar(&so.RoleARN, "role-arn", "", "read-only discovery role to assume (session hermes-discovery, 1 hour)")
	f.StringVar(&so.ExternalID, "external-id", "", "sts:ExternalId required by the discovery role")
	f.StringVar(&so.EndpointURL, "endpoint-url", "", "custom endpoint for every AWS client (moto/LocalStack-style testing)")
	f.IntVar(&so.MaxAttempts, "max-attempts", 5, "SDK attempts per call (throttling retries); exhausted retries become 'throttled' gaps")
	f.IntVar(&concurrency, "concurrency", 4, "parallel collector tasks")
	f.StringSliceVar(&ccTypes, "cloudcontrol-types", nil, "extra CloudFormation types collected with the Cloud Control API fallback (e.g. AWS::IoT::Thing)")
	f.StringArrayVar(&tfState, "terraform-state", nil, "Terraform/OpenTofu state v4 JSON file used only to annotate managed_by (repeatable)")
	f.BoolVar(&redact, "redact", false, "replace account ids in the output with REDACTED")
	f.StringVar(&out, "out", "", "write the Inventory JSON to this file (default: stdout)")
	f.StringVar(&opsOut, "operations-out", "", "write the list of API operations that were called (proof of read-only calls)")
	f.DurationVar(&timeout, "timeout", 30*time.Minute, "overall discovery timeout")
	return cmd
}

func migEmitInventory(app *App, inv *inventory.Inventory, out string) error {
	data, err := inv.Marshal()
	if err != nil {
		return err
	}
	if out == "" || out == "-" {
		_, err = app.Stdout.Write(data)
		migInventorySummary(app, inv, "", app.Stderr)
		return err
	}
	if err := migWriteFile(out, data); err != nil {
		return err
	}
	if migJSON(app) {
		return migPrintJSON(app, map[string]any{"out": out, "summary": inv.Summary, "regions": inv.Metadata.Regions, "gaps": inv.Gaps})
	}
	migInventorySummary(app, inv, out, app.Stdout)
	return nil
}

func migInventorySummary(app *App, inv *inventory.Inventory, out string, w interface{ Write([]byte) (int, error) }) {
	fmt.Fprintf(w, "Discovered %d resource(s) in %s (collector %s, read-only).\n", inv.Summary.ResourceCount, strings.Join(inv.Metadata.Regions, ", "), inv.Metadata.Collector)
	types := make([]string, 0, len(inv.Summary.ByType))
	for t := range inv.Summary.ByType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		fmt.Fprintf(w, "  %-48s %d\n", t, inv.Summary.ByType[t])
	}
	fmt.Fprintf(w, "Relationships: %d. Gaps: %d.\n", len(inv.Relationships), len(inv.Gaps))
	for _, g := range inv.Gaps {
		fmt.Fprintf(w, "  gap: %s %s %s: %s (%s)\n", g.Service, g.Region, g.Operation, g.Reason, g.Impact)
	}
	if out != "" {
		fmt.Fprintf(w, "Inventory written to %s. Next: hermes migrate assess --inventory %s --target gcp|alibaba\n", out, out)
	}
}

func newInventoryImportCmd(app *App) *cobra.Command {
	var format, out string
	cmd := &cobra.Command{
		Use:   "import FILE...",
		Short: "Convert an external inventory export into a Hermes Inventory (completeness partial)",
		Long: `Convert AWS Config advanced query results ("aws configservice select-resource-config" output,
selecting resourceId, resourceType, resourceName, awsRegion, arn, accountId, tags, configuration,
relationships) into an Inventory. Only recorded resource types are present; every resource is at most
"partial". Google Migration Center and AWS Application Discovery Service exports are complementary
sizing sources and are not imported.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if format != "aws-config" {
				return Refused("unsupported --format %q (supported: aws-config)", format)
			}
			var inputs [][]byte
			for _, a := range args {
				b, err := os.ReadFile(a)
				if err != nil {
					return err
				}
				inputs = append(inputs, b)
			}
			inv, err := inventory.ImportAWSConfig(inputs...)
			if err != nil {
				return err
			}
			inv.Metadata.GeneratedAt = migNow(app)().UTC().Format(time.RFC3339)
			inv.Metadata.Generator = "hermes/" + app.Build.Version
			return migEmitInventory(app, inv, out)
		},
	}
	cmd.Flags().StringVar(&format, "format", "aws-config", "input format: aws-config")
	cmd.Flags().StringVar(&out, "out", "", "write the Inventory JSON to this file (default: stdout)")
	return cmd
}

func newInventoryGraphCmd(app *App) *cobra.Command {
	var invPath, format, focus, out string
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Render the dependency graph (mermaid, dot or json), optionally the blast radius of one resource",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			inv, err := inventory.Load(invPath)
			if err != nil {
				return err
			}
			g := graph.Build(inv)
			var radius *graph.Radius
			if focus != "" {
				g, radius, err = g.Focus(focus)
				if err != nil {
					return Refused("%v", err)
				}
			}
			if migJSON(app) {
				format = "json"
			}
			var data []byte
			switch format {
			case "mermaid":
				data = []byte(g.Mermaid())
			case "dot":
				data = []byte(g.DOT())
			case "json":
				data, err = g.JSON(radius)
				if err != nil {
					return err
				}
				data = append(data, '\n')
			default:
				return Refused("unknown --format %q (mermaid, dot, json)", format)
			}
			if radius != nil && format != "json" {
				fmt.Fprintf(app.Stderr, "Blast radius of %s: %d dependent(s), %d dependenc(ies).\n", focus, len(radius.Dependents), len(radius.Dependencies))
			}
			return migWriteOut(app, out, data)
		},
	}
	f := cmd.Flags()
	f.StringVar(&invPath, "inventory", "inventory.json", "Inventory file")
	f.StringVar(&format, "format", "mermaid", "output format: mermaid, dot or json")
	f.StringVar(&focus, "focus", "", "resource id: show only its blast radius (dependents and dependencies)")
	f.StringVar(&out, "out", "", "write to this file (default: stdout)")
	return cmd
}

func newInventoryPolicyCmd(app *App) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Print the least-privilege read-only IAM policy for AWS discovery",
		Long:  "Print the IAM policy generated from the same operation registry the read-only guard enforces (identical to migration/discovery/aws-readonly-discovery-policy.json).",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return migWriteOut(app, out, awsinv.DiscoveryPolicyJSON())
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write to this file (default: stdout)")
	return cmd
}

// --- shared helpers for the migration command groups (prefixed to avoid clashes) ---------

func migJSON(app *App) bool { return app.Output == "json" }

func migNow(app *App) func() time.Time {
	if app.Now != nil {
		return app.Now
	}
	return time.Now
}

func migSplit(xs []string) []string {
	var out []string
	for _, x := range xs {
		for _, p := range strings.Split(x, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func migPrintJSON(app *App, v any) error {
	enc := json.NewEncoder(app.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func migWriteFile(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	// Inventories, assessments, plans and reports describe the source estate: owner-only.
	return os.WriteFile(path, data, 0o600) //nolint:gosec // the path is the operator's own --out argument
}

func migWriteOut(app *App, out string, data []byte) error {
	if out == "" || out == "-" {
		_, err := app.Stdout.Write(data)
		return err
	}
	return migWriteFile(out, data)
}

func migMarshalJSON(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return append(b, '\n'), err
}

func migWorkdir(app *App) string {
	if app.WorkDir != "" {
		return app.WorkDir
	}
	wd, _ := os.Getwd()
	return wd
}
