package cli

import (
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/internal/providers"
	"github.com/nityam02/hermes-cloud-foundation/internal/render"
	"github.com/nityam02/hermes-cloud-foundation/internal/ui"
)

func init() {
	Register(newValidateCmd)
	Register(newPortabilityCmd)
	Register(newRenderCmd)
}

type envValidation struct {
	Environment string            `json:"environment"`
	Provider    string            `json:"provider,omitempty"`
	Protected   bool              `json:"protected"`
	Valid       bool              `json:"valid"`
	Diagnostics diag.Diagnostics  `json:"diagnostics"`
	Portability *providers.Report `json:"portability,omitempty"`
}

func newValidateCmd(app *App) *cobra.Command {
	var portability bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the spec (schema, semantic and provider rules) for every environment",
		Long: `Validate parses hermes.yaml, checks it against the JSON Schema, applies each
environment overlay and the protected-environment rules, and runs the provider adapter
(regions, sizing, extension keys, certificates). Without --env every declared environment
is validated. Exit code 2 when any error is found.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			path := app.configPath()
			doc, pds, err := config.Load(path)
			if err != nil {
				return err
			}
			var results []envValidation
			if doc == nil || pds.HasErrors() {
				results = append(results, envValidation{Environment: app.Env, Diagnostics: pds})
			} else {
				envs := doc.Environments()
				if app.Env != "" {
					envs = []string{app.Env}
				}
				if len(envs) == 0 {
					envs = []string{""}
				}
				for i, env := range envs {
					r := envValidation{Environment: env}
					if i == 0 {
						r.Diagnostics = append(r.Diagnostics, pds...) // file-level findings once
					}
					p, ds, err := doc.Resolve(env)
					if err != nil {
						return err
					}
					r.Diagnostics = append(r.Diagnostics, ds...)
					if p != nil {
						r.Environment, r.Provider, r.Protected = p.Environment, p.Cloud.Provider, p.Protected
						if a, ok := providers.Get(p.Cloud.Provider); ok {
							r.Diagnostics = append(r.Diagnostics, a.Validate(p)...)
						}
						if portability {
							rep := providers.Portability(p)
							r.Portability = &rep
						}
					}
					results = append(results, r)
				}
			}
			valid := true
			for i := range results {
				results[i].Valid = !results[i].Diagnostics.HasErrors()
				results[i].Diagnostics = results[i].Diagnostics.Sorted()
				if results[i].Diagnostics == nil {
					results[i].Diagnostics = diag.Diagnostics{}
				}
				valid = valid && results[i].Valid
			}
			if app.jsonOut() {
				if err := ui.JSON(app.Stdout, map[string]any{"file": path, "valid": valid, "environments": results}); err != nil {
					return err
				}
			} else {
				for _, r := range results {
					printEnvValidation(app, r)
				}
			}
			if !valid {
				return &ExitError{Code: ExitRefused}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&portability, "portability", false, "also score the spec against every provider")
	return cmd
}

func printEnvValidation(app *App, r envValidation) {
	w := app.Stdout
	status := "valid"
	if !r.Valid {
		status = "INVALID"
	}
	prot := ""
	if r.Protected {
		prot = ", protected"
	}
	fmt.Fprintf(w, "environment %s (%s%s): %s\n", r.Environment, r.Provider, prot, status)
	for _, d := range r.Diagnostics {
		if d.Severity == diag.SeverityInfo && !app.Verbose {
			continue
		}
		fmt.Fprintf(w, "  %s\n", d)
	}
	if r.Portability != nil {
		printPortability(w, *r.Portability, false)
	}
}

func newPortabilityCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "portability",
		Short: "Score the spec against every provider (measured, per requested feature)",
		Long: `Portability lists the capability features the spec relies on and, for every
provider, the status of each (implemented = 1, partial = 0.5, not-implemented = 0), a
weighted score, provider-specific sizing findings, and the features whose status differs
between providers. Statuses describe the Hermes modules (mock-tested), not real-cloud tests.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			l, err := app.load()
			if err != nil {
				return err
			}
			rep := providers.Portability(l.p)
			if app.jsonOut() {
				return ui.JSON(app.Stdout, rep)
			}
			printPortability(app.Stdout, rep, true)
			return nil
		},
	}
}

func printPortability(w io.Writer, r providers.Report, full bool) {
	fmt.Fprintf(w, "Portability of %s/%s (%d requested features)\n", r.Project, r.Environment, len(r.Requested))
	rows := [][]string{{"PROVIDER", "SCORE", "IMPLEMENTED", "PARTIAL", "NOT-IMPLEMENTED", "NOTES"}}
	for _, ps := range r.Providers {
		counts := map[providers.Status]int{}
		for _, f := range ps.Features {
			counts[f.Status]++
		}
		name := ps.Provider
		if ps.Selected {
			name += " *"
		}
		note := ""
		if ps.Blocked {
			note = "blocked: sizing/extension errors"
		} else if n := len(ps.Diagnostics); n > 0 {
			note = fmt.Sprintf("%d sizing/extension finding(s)", n)
		}
		rows = append(rows, []string{name, fmt.Sprintf("%.1f", ps.Score), fmt.Sprint(counts[providers.Implemented]),
			fmt.Sprint(counts[providers.Partial]), fmt.Sprint(counts[providers.NotImplemented]), note})
	}
	ui.Table(w, rows)
	if !full {
		return
	}
	if len(r.Differences) > 0 {
		fmt.Fprintln(w, "\nDifferences that matter:")
		for _, d := range r.Differences {
			names := make([]string, 0, len(d.Statuses))
			for n := range d.Statuses {
				names = append(names, n)
			}
			sort.Strings(names)
			fmt.Fprintf(w, "  %s\n", d.Feature)
			for _, n := range names {
				c := d.Statuses[n]
				fmt.Fprintf(w, "    %-8s %-16s %s\n", n, c.Status, c.Note)
			}
		}
	}
	for _, ps := range r.Providers {
		for _, d := range ps.Diagnostics {
			fmt.Fprintf(w, "  [%s] %s\n", ps.Provider, d)
		}
	}
	fmt.Fprintf(w, "\n* = selected provider. %s\nNote: %s.\n", r.Method, r.Verification)
}

func newRenderCmd(app *App) *cobra.Command {
	var (
		printSpec bool
		rf        renderFlags
	)
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render the OpenTofu/Terraform root module (.hermes/build/<env>/main.tf.json)",
		Long: `Render writes the root module for the selected environment: exact provider
pins, the state backend, the provider block and a call to the provider composition with
the contract variables. --print prints the normalized spec instead (every default filled).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			l, err := app.load()
			if err != nil {
				return err
			}
			if printSpec {
				if app.jsonOut() {
					return ui.JSON(app.Stdout, l.p)
				}
				enc := yaml.NewEncoder(app.Stdout)
				enc.SetIndent(2)
				if err := enc.Encode(l.p); err != nil {
					return err
				}
				return enc.Close()
			}
			engine := l.p.Engine.Binary
			if eng, err := app.detectEngine(cmd.Context(), l.p.Engine.Binary); err == nil {
				engine = eng.Name
			} else if engine == "auto" {
				engine = "tofu"
				fmt.Fprintln(app.Stderr, "note: no engine found; rendering for OpenTofu")
			}
			res, err := render.Render(l.p, app.renderOptions(l.p.Environment, engine, zeroVersion, rf))
			if err != nil {
				return err
			}
			app.printWarnings(l.diags)
			if app.jsonOut() {
				return ui.JSON(app.Stdout, res)
			}
			fmt.Fprintf(app.Stdout, "Rendered %s (%s, %s, engine %s)\n  digest  %s\n  modules %s (%s)\n  state   %s\n",
				res.RootFile, l.p.Environment, res.Provider, res.Engine, res.Digest, res.ModulesDir, short(res.ModulesDigest), res.Backend.Location())
			return nil
		},
	}
	cmd.Flags().BoolVar(&printSpec, "print", false, "print the normalized spec instead of writing the root module")
	cmd.Flags().BoolVar(&rf.refreshModules, "refresh-modules", false, "replace an extracted modules directory that differs from the embedded modules")
	return cmd
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
