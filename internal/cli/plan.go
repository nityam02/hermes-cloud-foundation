package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/policy"
	"github.com/nityam02/hermes-cloud-foundation/internal/render"
	"github.com/nityam02/hermes-cloud-foundation/internal/tfplan"
	"github.com/nityam02/hermes-cloud-foundation/internal/ui"
)

var zeroVersion execution.Version

func init() {
	Register(newPlanCmd)
	Register(newApplyCmd)
	Register(newDestroyCmd)
}

type policyFlags struct {
	dirs []string
	data []string
	skip bool
}

func (pf *policyFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&pf.dirs, "policy-dir", nil, "add plan policies from this directory (repeatable); the embedded policies always run")
	cmd.Flags().StringArrayVar(&pf.data, "policy-data", nil, "extra conftest data file, e.g. policy exceptions (repeatable; JSON or YAML)")
	cmd.Flags().BoolVar(&pf.skip, "skip-policy", false, "skip the plan policy check (recorded in history)")
}

// planOutcome is what plan/destroy print.
type planOutcome struct {
	PlanID      string                 `json:"plan_id"`
	Kind        string                 `json:"kind"`
	Environment string                 `json:"environment"`
	Provider    string                 `json:"provider"`
	Protected   bool                   `json:"protected"`
	PlanDir     string                 `json:"plan_dir"`
	HasChanges  bool                   `json:"has_changes"`
	Summary     tfplan.Summary         `json:"summary"`
	Policy      execution.PolicyRecord `json:"policy"`
	Next        string                 `json:"next"`
}

// makePlan renders the environment and creates a saved plan of the given kind.
func (app *App) makePlan(ctx context.Context, l *loaded, kind string, pf policyFlags, rf renderFlags) (*execution.SavedPlan, error) {
	p := l.p
	if err := app.planChecks(l); err != nil {
		return nil, err
	}
	eng, err := app.engineFor(ctx, p, "")
	if err != nil {
		return nil, err
	}
	res, err := render.Render(p, app.renderOptions(p.Environment, eng.Name, eng.Version, rf))
	if err != nil {
		return nil, err
	}
	app.printWarnings(l.diags)
	ws := app.workspace()
	var policyDiags diag.Diagnostics
	req := execution.PlanRequest{
		Kind: kind, RootDir: res.Dir, PlanDir: ws.PlanDir(p.Environment, kind),
		Meta: execution.PlanMeta{
			Project: p.Project.Name, Environment: p.Environment, Provider: p.Cloud.Provider, Protected: p.Protected,
			RenderDigest: res.Digest, ModulesDigest: res.ModulesDigest, User: app.userName(),
		},
		Now: app.now,
		Policy: func(ctx context.Context, planJSON string) (execution.PolicyRecord, error) {
			rec, ds, err := policy.Check(ctx, planJSON, policy.Options{
				PolicyDirs: pf.dirs, DataFiles: pf.data, HermesDir: ws.Dir, Version: app.Build.Version, Protected: p.Protected, Skip: pf.skip,
				Data:     policyData(p),
				LookPath: app.lookPath, Run: policy.Runner(app.runTool), Now: app.now,
			})
			policyDiags = ds
			if err != nil {
				return rec, err
			}
			if ds.HasErrors() && !rec.Ran {
				return rec, &execution.GateError{Code: execution.CodePolicyMissing, Reason: ds.Err().Error(), Hint: "install conftest, or pass --skip-policy (recorded)"}
			}
			return rec, nil
		},
	}
	sp, err := execution.CreatePlan(ctx, eng, req)
	for _, d := range policyDiags.Sorted() {
		fmt.Fprintln(app.Stderr, d.String())
	}
	return sp, err
}

func policyData(p *config.Platform) map[string]any {
	return map[string]any{"hermes": map[string]any{
		"project": p.Project.Name, "environment": p.Environment, "provider": p.Cloud.Provider,
		"protected": p.Protected, "protected_environments": p.Project.ProtectedEnvironments,
	}}
}

func (app *App) printPlan(o planOutcome) error {
	if app.jsonOut() {
		return ui.JSON(app.Stdout, o)
	}
	w := app.Stdout
	s := o.Summary
	fmt.Fprintf(w, "%s plan for %s (%s%s)\n", o.Kind, o.Environment, o.Provider, map[bool]string{true: ", protected"}[o.Protected])
	fmt.Fprintf(w, "  %d to create, %d to update, %d to replace, %d to delete, %d to import\n", s.Create, s.Update, s.Replace, s.Delete, s.Import)
	for _, it := range s.Destructive() {
		fmt.Fprintf(w, "  ! %s %s\n", it.Action, it.Address)
	}
	switch {
	case o.Policy.Skipped:
		fmt.Fprintln(w, "  policy: SKIPPED (--skip-policy)")
	case !o.Policy.Ran:
		fmt.Fprintf(w, "  policy: not evaluated (%s)\n", o.Policy.Note)
	case o.Policy.Passed:
		fmt.Fprintf(w, "  policy: passed (%d warning(s))\n", len(o.Policy.Warnings))
	default:
		fmt.Fprintf(w, "  policy: FAILED (%d failure(s)); this plan cannot be applied\n", len(o.Policy.Failures))
	}
	// Every policy exception in effect is shown with the plan it applies to (and recorded in
	// meta.json and history).
	for _, e := range o.Policy.Exceptions {
		scope := "applies to a resource in this plan"
		if !e.InPlan {
			scope = "address not in this plan"
		}
		fmt.Fprintf(w, "  policy exception: %s %s (reason: %s; expires %s; %s; from %s)\n", e.Rule, e.Address, e.Reason, e.Expires, scope, e.File)
	}
	fmt.Fprintf(w, "  plan id: %s\n  saved:   %s\n", o.PlanID, o.PlanDir)
	if o.Next != "" {
		fmt.Fprintf(w, "Next: %s\n", o.Next)
	}
	return nil
}

func outcome(sp *execution.SavedPlan) planOutcome {
	m := sp.Meta
	return planOutcome{PlanID: m.PlanID, Kind: m.Kind, Environment: m.Environment, Provider: m.Provider, Protected: m.Protected,
		PlanDir: sp.Dir, HasChanges: m.HasChanges, Summary: m.Summary, Policy: m.Policy}
}

func newPlanCmd(app *App) *cobra.Command {
	var (
		detailed bool
		pf       policyFlags
		rf       renderFlags
	)
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Render, plan and save a reviewed plan (.hermes/plans/<env>/)",
		Long: `Plan renders the root module, runs init and plan -out, converts the plan with
show -json, classifies every change, evaluates the plan policies with conftest, and saves
plan.tfplan, plan.json and meta.json (plan_id = sha256 of the plan file). Only a saved plan
can be applied.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			l, err := app.load()
			if err != nil {
				return err
			}
			env := l.p.Environment
			sp, err := app.makePlan(cmd.Context(), l, execution.KindApply, pf, rf)
			if err != nil {
				app.historyForError(env, "plan", "", err)
				return err
			}
			m := sp.Meta
			app.history(env, execution.HistoryEvent{Action: "plan", Result: "ok", PlanID: m.PlanID, Engine: m.Engine,
				Changes: execution.CountsOf(m.Summary), SkipPolicy: m.Policy.Skipped, Policies: m.Policy.Sources(), PolicyExceptions: m.Policy.Exceptions})
			o := outcome(sp)
			switch {
			case m.Policy.Ran && !m.Policy.Passed:
				o.Next = "fix the policy failures and run `hermes plan` again"
			case !m.HasChanges:
				o.Next = "nothing to apply"
			case m.Protected:
				o.Next = fmt.Sprintf("hermes apply --env %s --approve %s", env, m.PlanID[:12])
			default:
				o.Next = fmt.Sprintf("hermes apply --env %s", env)
			}
			if len(m.Summary.Destructive()) > 0 && m.HasChanges {
				o.Next += " --allow-destructive"
			}
			if err := app.printPlan(o); err != nil {
				return err
			}
			if m.Policy.Ran && !m.Policy.Passed {
				return &ExitError{Code: ExitRefused}
			}
			if detailed && m.HasChanges {
				return &ExitError{Code: ExitChanges}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&detailed, "detailed-exitcode", false, "exit 3 when the plan contains changes")
	pf.add(cmd)
	cmd.Flags().BoolVar(&rf.refreshModules, "refresh-modules", false, "replace an extracted modules directory that differs from the embedded modules")
	return cmd
}

type applyFlags struct {
	approve          string
	allowDestructive bool
	yes              bool
	confirmEnv       string
	maxAge           time.Duration
}

// applySaved re-renders (without writing) to detect staleness, then runs the gates and
// applies the saved plan of the given kind.
func (app *App) applySaved(ctx context.Context, l *loaded, kind string, af applyFlags) error {
	p := l.p
	env := p.Environment
	action := map[string]string{execution.KindApply: "apply", execution.KindDestroy: "destroy"}[kind]
	sp, err := execution.LoadPlan(app.workspace().PlanDir(env, kind))
	if err != nil {
		app.historyForError(env, action, "", err)
		return err
	}
	if p.Cloud.Provider == config.ProviderGCP && p.Cloud.Account == "" {
		p.Cloud.Account = app.getenv("GOOGLE_CLOUD_PROJECT")
	}
	eng, err := app.engineFor(ctx, p, sp.Meta.Engine)
	if err != nil {
		return err
	}
	cur, err := render.Build(p, app.renderOptions(env, eng.Name, eng.Version, renderFlags{}))
	if err != nil {
		if errors.Is(err, render.ErrModulesChanged) {
			err = &execution.GateError{Code: execution.CodeStalePlan, Reason: err.Error(), Hint: "run `hermes plan`"}
		}
		app.historyForError(env, action, sp.Meta.PlanID, err)
		return err
	}
	req := execution.ApplyRequest{
		Kind: kind, Environment: env, Protected: p.Protected,
		CurrentDigest: cur.Digest, CurrentModulesDigest: cur.ModulesDigest,
		Approve: af.approve, AllowDestructive: af.allowDestructive, Yes: af.yes, ConfirmEnvironment: af.confirmEnv,
		MaxAge: af.maxAge, Interactive: app.interactive(), Now: app.now,
		Confirm: func(prompt string) (bool, error) { return ui.Confirm(app.Stdin, app.Stderr, prompt) },
	}
	if p.Protected && af.yes {
		fmt.Fprintln(app.Stderr, "note: --yes is ignored in protected environments; --approve is required")
	}
	if err := execution.Apply(ctx, eng, cur.Dir, sp, req); err != nil {
		app.historyForError(env, action, sp.Meta.PlanID, err)
		return err
	}
	app.history(env, execution.HistoryEvent{Action: action, Result: "ok", PlanID: sp.Meta.PlanID, Engine: eng.Name,
		Changes: execution.CountsOf(sp.Meta.Summary), SkipPolicy: sp.Meta.Policy.Skipped, Policies: sp.Meta.Policy.Sources(), PolicyExceptions: sp.Meta.Policy.Exceptions})
	if app.jsonOut() {
		return ui.JSON(app.Stdout, map[string]any{"result": "ok", "action": action, "plan_id": sp.Meta.PlanID, "environment": env})
	}
	fmt.Fprintf(app.Stdout, "%s of plan %s to %s complete.\n", action, short(sp.Meta.PlanID), env)
	if kind == execution.KindApply {
		fmt.Fprintf(app.Stdout, "Bindings: hermes bindings --env %s -o json\n", env)
	}
	return nil
}

func newApplyCmd(app *App) *cobra.Command {
	var af applyFlags
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply the saved plan for an environment, behind safety gates",
		Long: `Apply applies the plan saved by 'hermes plan' and nothing else. It refuses when the
spec or modules changed since planning (stale), when the plan is older than --max-plan-age,
when the plan file was modified, when policies failed, when the plan deletes or replaces
resources without --allow-destructive, and in protected environments without
--approve <plan-id-prefix> (at least 8 characters). Elsewhere it asks y/N on a terminal
unless --yes. Every apply and refusal is recorded in .hermes/history/<env>.jsonl.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			l, err := app.load()
			if err != nil {
				return err
			}
			return app.applySaved(cmd.Context(), l, execution.KindApply, af)
		},
	}
	cmd.Flags().StringVar(&af.approve, "approve", "", "plan-id prefix (>= 8 characters) approving the saved plan")
	cmd.Flags().BoolVar(&af.allowDestructive, "allow-destructive", false, "allow a plan that deletes or replaces resources")
	cmd.Flags().BoolVarP(&af.yes, "yes", "y", false, "do not prompt (non-protected environments only)")
	cmd.Flags().DurationVar(&af.maxAge, "max-plan-age", execution.DefaultMaxPlanAge, "refuse saved plans older than this")
	return cmd
}

func newDestroyCmd(app *App) *cobra.Command {
	var (
		af applyFlags
		pf policyFlags
		rf renderFlags
	)
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Plan, then (with --approve) destroy every resource of an environment",
		Long: `Without --approve, destroy creates and saves a destroy plan (plan -destroy) and prints
its summary and plan id; nothing is destroyed. With --approve <plan-id-prefix> it applies
that saved destroy plan after the same staleness, age, tamper and policy gates as apply.
Protected environments also require --confirm-environment <env>. Databases and buckets with
deletion protection will make the engine refuse until protection is lifted in the spec.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			l, err := app.load()
			if err != nil {
				return err
			}
			if af.approve != "" {
				return app.applySaved(cmd.Context(), l, execution.KindDestroy, af)
			}
			env := l.p.Environment
			sp, err := app.makePlan(cmd.Context(), l, execution.KindDestroy, pf, rf)
			if err != nil {
				app.historyForError(env, "destroy-plan", "", err)
				return err
			}
			app.history(env, execution.HistoryEvent{Action: "destroy-plan", Result: "ok", PlanID: sp.Meta.PlanID,
				Engine: sp.Meta.Engine, Changes: execution.CountsOf(sp.Meta.Summary), SkipPolicy: sp.Meta.Policy.Skipped, Policies: sp.Meta.Policy.Sources(), PolicyExceptions: sp.Meta.Policy.Exceptions})
			o := outcome(sp)
			o.Next = fmt.Sprintf("hermes destroy --env %s --approve %s", env, sp.Meta.PlanID[:12])
			if sp.Meta.Protected {
				o.Next += " --confirm-environment " + env
			}
			return app.printPlan(o)
		},
	}
	cmd.Flags().StringVar(&af.approve, "approve", "", "plan-id prefix (>= 8 characters) of the saved destroy plan")
	cmd.Flags().StringVar(&af.confirmEnv, "confirm-environment", "", "repeat the environment name (required for protected environments)")
	cmd.Flags().DurationVar(&af.maxAge, "max-plan-age", execution.DefaultMaxPlanAge, "refuse saved plans older than this")
	pf.add(cmd)
	cmd.Flags().BoolVar(&rf.refreshModules, "refresh-modules", false, "replace an extracted modules directory that differs from the embedded modules")
	return cmd
}
