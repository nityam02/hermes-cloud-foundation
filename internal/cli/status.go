package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/state"
	"github.com/nityam02/hermes-cloud-foundation/internal/ui"
)

func init() {
	Register(newStatusCmd)
	Register(newBindingsCmd)
}

type statusReport struct {
	Environment string                  `json:"environment"`
	Provider    string                  `json:"provider"`
	Region      string                  `json:"region"`
	Protected   bool                    `json:"protected"`
	State       string                  `json:"state"`
	LastPlan    *execution.PlanMeta     `json:"last_plan,omitempty"`
	LastApply   *execution.HistoryEvent `json:"last_apply,omitempty"`
	Initialized bool                    `json:"initialized"`
	Resources   *int                    `json:"resources,omitempty"`
	Bindings    any                     `json:"bindings,omitempty"`
	Drift       *bool                   `json:"drift,omitempty"`
	Notes       []string                `json:"notes,omitempty"`
}

func newStatusCmd(app *App) *cobra.Command {
	var drift bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show environment, last plan/apply, bindings and resource counts",
		Long: `Status reads local metadata (saved plan, history) and, when the root module is
initialized, the state through the engine (output -json, show -json). --drift runs
plan -refresh-only -detailed-exitcode and reports whether real infrastructure drifted from
the state; it reads the cloud with your credentials but never changes anything.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			l, err := app.load()
			if err != nil {
				return err
			}
			p := l.p
			ws := app.workspace()
			r := statusReport{Environment: p.Environment, Provider: p.Cloud.Provider, Region: p.Cloud.Region, Protected: p.Protected}
			if be, err := state.ForPlatform(p, "(local)", false); err == nil {
				r.State = be.Location()
			} else {
				r.State = "not configured: " + err.Error()
			}
			if sp, err := execution.LoadPlan(ws.PlanDir(p.Environment, execution.KindApply)); err == nil {
				r.LastPlan = &sp.Meta
			}
			if evs, err := execution.ReadHistory(ws.HistoryFile(p.Environment)); err == nil {
				for i := len(evs) - 1; i >= 0; i-- {
					if evs[i].Action == "apply" && evs[i].Result == "ok" {
						ev := evs[i]
						r.LastApply = &ev
						break
					}
				}
			}
			dir := ws.BuildDir(p.Environment)
			if _, err := os.Stat(filepath.Join(dir, ".terraform")); err == nil {
				r.Initialized = true
				eng, err := app.engineFor(cmd.Context(), p, "")
				if err != nil {
					return err
				}
				if out, err := eng.OutputJSON(cmd.Context(), dir); err == nil {
					r.Bindings = outputValue(out, "bindings")
				} else {
					r.Notes = append(r.Notes, "state not reachable for outputs: "+err.Error())
				}
				if js, err := eng.ShowJSON(cmd.Context(), dir, ""); err == nil {
					n := countResources(js)
					r.Resources = &n
				}
				if drift {
					d, err := eng.PlanRefreshOnly(cmd.Context(), dir)
					if err != nil {
						return fmt.Errorf("drift check: %w", err)
					}
					r.Drift = &d
				}
			} else {
				r.Notes = append(r.Notes, "root module not initialized; run `hermes plan`")
				if drift {
					return Refused("--drift needs an initialized root module; run `hermes plan` first")
				}
			}
			if app.jsonOut() {
				return ui.JSON(app.Stdout, r)
			}
			w := app.Stdout
			fmt.Fprintf(w, "environment %s: %s %s%s\n  state: %s\n", r.Environment, r.Provider, r.Region,
				map[bool]string{true: " (protected)"}[r.Protected], r.State)
			if r.LastPlan != nil {
				s := r.LastPlan.Summary
				applied := "not applied"
				if r.LastPlan.AppliedAt != nil {
					applied = "applied " + r.LastPlan.AppliedAt.Format("2006-01-02T15:04:05Z")
				}
				fmt.Fprintf(w, "  last plan: %s at %s by %s (+%d ~%d -/+%d -%d), %s\n", short(r.LastPlan.PlanID),
					r.LastPlan.CreatedAt.Format("2006-01-02T15:04:05Z"), r.LastPlan.User, s.Create, s.Update, s.Replace, s.Delete, applied)
			} else {
				fmt.Fprintln(w, "  last plan: none")
			}
			if r.LastApply != nil {
				fmt.Fprintf(w, "  last apply: %s at %s by %s\n", short(r.LastApply.PlanID), r.LastApply.Time.Format("2006-01-02T15:04:05Z"), r.LastApply.User)
			}
			if r.Resources != nil {
				fmt.Fprintf(w, "  resources in state: %d\n", *r.Resources)
			}
			if r.Drift != nil {
				fmt.Fprintf(w, "  drift: %v\n", *r.Drift)
			}
			for _, n := range r.Notes {
				fmt.Fprintf(w, "  note: %s\n", n)
			}
			if r.Drift != nil && *r.Drift {
				return &ExitError{Code: ExitChanges}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&drift, "drift", false, "check for drift with plan -refresh-only (exit 3 when drift is found)")
	return cmd
}

// outputValue extracts .<name>.value from `output -json`.
func outputValue(out []byte, name string) any {
	var m map[string]struct {
		Value any `json:"value"`
	}
	if json.Unmarshal(out, &m) != nil {
		return nil
	}
	return m[name].Value
}

// countResources counts managed resources in `show -json` state output.
func countResources(js []byte) int {
	type module struct {
		Resources []struct {
			Mode string `json:"mode"`
		} `json:"resources"`
		ChildModules []json.RawMessage `json:"child_modules"`
	}
	var doc struct {
		Values struct {
			RootModule json.RawMessage `json:"root_module"`
		} `json:"values"`
	}
	if json.Unmarshal(js, &doc) != nil || doc.Values.RootModule == nil {
		return 0
	}
	var walk func(raw json.RawMessage) int
	walk = func(raw json.RawMessage) int {
		var m module
		if json.Unmarshal(raw, &m) != nil {
			return 0
		}
		n := 0
		for _, r := range m.Resources {
			if r.Mode == "managed" {
				n++
			}
		}
		for _, c := range m.ChildModules {
			n += walk(c)
		}
		return n
	}
	return walk(doc.Values.RootModule)
}

func newBindingsCmd(app *App) *cobra.Command {
	var service string
	cmd := &cobra.Command{
		Use:   "bindings",
		Short: "Print bindings and service environment variables (non-secret) from the state",
		Long: `Bindings reads the composition outputs (output -json) of an applied environment.
-o json prints {bindings, service_environment, secret_environment}; secret_environment holds
secret references only, never values. -o env --service <name> prints KEY=value lines for one
service, e.g. to run it locally against the environment.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := app.format("text", "json", "env")
			if err != nil {
				return err
			}
			l, err := app.load()
			if err != nil {
				return err
			}
			dir := app.workspace().BuildDir(l.p.Environment)
			if _, err := os.Stat(filepath.Join(dir, ".terraform")); err != nil {
				return Refused("environment %s is not initialized; run `hermes plan` and `hermes apply` first", l.p.Environment)
			}
			eng, err := app.engineFor(cmd.Context(), l.p, "")
			if err != nil {
				return err
			}
			out, err := eng.OutputJSON(cmd.Context(), dir)
			if err != nil {
				return err
			}
			doc := map[string]any{
				"environment":         l.p.Environment,
				"provider":            l.p.Cloud.Provider,
				"bindings":            outputValue(out, "bindings"),
				"service_environment": outputValue(out, "service_environment"),
				"secret_environment":  outputValue(out, "secret_environment"),
			}
			switch format {
			case "json":
				return ui.JSON(app.Stdout, doc)
			case "env":
				if service == "" {
					return Refused("-o env needs --service <name>")
				}
				envs, _ := doc["service_environment"].(map[string]any)
				vars, ok := envs[service].(map[string]any)
				if !ok {
					return Refused("service %q has no environment in the outputs", service)
				}
				keys := make([]string, 0, len(vars))
				for k := range vars {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(app.Stdout, "%s=%s\n", k, shellQuote(fmt.Sprint(vars[k])))
				}
				return nil
			}
			data, _ := json.MarshalIndent(doc["bindings"], "", "  ")
			fmt.Fprintf(app.Stdout, "%s\n", data)
			return nil
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "service name (for -o env)")
	return cmd
}

func unsafeShellRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("-_./:@", r)
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, unsafeShellRune) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
