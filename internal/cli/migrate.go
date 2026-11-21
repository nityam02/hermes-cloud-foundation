package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
	"github.com/nityam02/hermes-cloud-foundation/internal/migration"
)

func init() { Register(newMigrateCmd) }

func newMigrateCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Terraform-independent migration: catalog, assess, generate, plan, validate, gated cutover",
		Long: `Plan and run a migration from an Inventory. Discovery, IaC adoption, destination provisioning,
data migration and application migration stay separate activities. Nothing here modifies source
resources: data movement is delegated to managed services through plan steps, and cutover only runs
hook commands you declare, behind approval gates.`,
	}
	cmd.AddCommand(newMigrateCatalogCmd(app), newMigrateAssessCmd(app), newMigrateGenerateCmd(app),
		newMigratePlanCmd(app), newMigrateValidateCmd(app), newMigrateCutoverCmd(app))
	return cmd
}

func newMigrateCatalogCmd(app *App) *cobra.Command {
	var catalogPath, target, format string
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Render the service catalog (source -> target mappings)",
		Example: `  hermes migrate catalog --format markdown > docs/migration/service-catalog.md
  hermes migrate catalog --target gcp`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := migration.LoadCatalog(catalogPath)
			if err != nil {
				return err
			}
			targets := migration.Targets
			if target != "" {
				if target != "gcp" && target != "alibaba" {
					return Refused("--target must be gcp or alibaba")
				}
				targets = []string{target}
			}
			if migJSON(app) {
				format = "json"
			}
			switch format {
			case "markdown":
				_, err = fmt.Fprint(app.Stdout, migration.CatalogMarkdown(c, targets))
			case "json":
				err = migPrintJSON(app, c)
			case "text":
				_, err = fmt.Fprint(app.Stdout, migration.CatalogText(c, targets))
			default:
				return Refused("unknown --format %q (markdown, json, text)", format)
			}
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&catalogPath, "catalog", "", "ServiceCatalog file (default: embedded migration/mappings/catalog.yaml)")
	f.StringVar(&target, "target", "", "only this target: gcp or alibaba (default: both)")
	f.StringVar(&format, "format", "text", "markdown, json or text")
	return cmd
}

func newMigrateAssessCmd(app *App) *cobra.Command {
	var invPath, target, catalogPath, pricingPath, out, md, project string
	cmd := &cobra.Command{
		Use:     "assess",
		Short:   "Classify an inventory against the catalog and assess the migration to a target",
		Example: `  hermes migrate assess --inventory inventory.json --target gcp --out assessment.json --markdown assessment.md`,
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if target != "gcp" && target != "alibaba" {
				return Refused("--target must be gcp or alibaba")
			}
			inv, err := inventory.Load(invPath)
			if err != nil {
				return err
			}
			c, err := migration.LoadCatalog(catalogPath)
			if err != nil {
				return err
			}
			p, err := migration.LoadPricing(pricingPath)
			if err != nil {
				return err
			}
			a, err := migration.Assess(inv, c, p, migration.AssessOptions{Target: target, Generator: "hermes/" + app.Build.Version,
				Project: project, Env: app.Env, Now: migNow(app)})
			if err != nil {
				return err
			}
			data, err := migMarshalJSON(a)
			if err != nil {
				return err
			}
			if md != "" {
				if err := migWriteFile(md, []byte(migration.AssessmentMarkdown(a))); err != nil {
					return err
				}
			}
			if out == "" || out == "-" {
				_, err = app.Stdout.Write(data)
				return err
			}
			if err := migWriteFile(out, data); err != nil {
				return err
			}
			if migJSON(app) {
				return migPrintJSON(app, map[string]any{"out": out, "markdown": md, "summary": a.Summary})
			}
			s := a.Summary
			fmt.Fprintf(app.Stdout, "Assessed %d resource(s) for %s: portability score %.1f%%, %d blocker(s), %d risk(s), %d discovery gap(s), %d wave(s).\n",
				s.ResourceCount, target, s.PortabilityScore, s.BlockerCount, s.RiskCount, s.GapCount, s.WaveCount)
			fmt.Fprintf(app.Stdout, "Data to migrate: %d bytes required; indicative transfer cost USD %.2f-%.2f (range from pricing assumptions, not a quote).\n",
				s.DataVolume.RequiredBytes, s.TransferCost.LowUSD, s.TransferCost.HighUSD)
			fmt.Fprintf(app.Stdout, "Assessment written to %s. Next: hermes migrate plan --assessment %s; hermes migrate generate --assessment %s --out <dir>\n", out, out, out)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&invPath, "inventory", "inventory.json", "Inventory file")
	f.StringVar(&target, "target", "", "target provider: gcp or alibaba")
	f.StringVar(&catalogPath, "catalog", "", "ServiceCatalog file (default: embedded)")
	f.StringVar(&pricingPath, "pricing", "", "PricingAssumptions file (default: embedded)")
	f.StringVar(&out, "out", "", "write the Assessment JSON to this file (default: stdout)")
	f.StringVar(&md, "markdown", "", "also write a Markdown report")
	f.StringVar(&project, "project", "", "project name for the draft spec (default migrated-platform)")
	return cmd
}

func newMigrateGenerateCmd(app *App) *cobra.Command {
	var aPath, out, project string
	cmd := &cobra.Command{
		Use:     "generate",
		Short:   "Write the DRAFT target Platform spec, MAPPING.md and UNSUPPORTED.md",
		Example: `  hermes migrate generate --assessment assessment.json --env production --out target/`,
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if out == "" {
				return Refused("--out <dir> is required")
			}
			a, err := migration.LoadAssessment(aPath)
			if err != nil {
				return err
			}
			res, err := migration.Generate(a, out, project, app.Env)
			if err != nil {
				return err
			}
			if migJSON(app) {
				return migPrintJSON(app, map[string]any{"files": res.Files, "unsupported": len(a.Unsupported)})
			}
			fmt.Fprintf(app.Stdout, "Wrote %s (DRAFT — requires human review).\n", strings.Join(res.Files, ", "))
			fmt.Fprintf(app.Stdout, "%d resource(s) mapped, %d listed in UNSUPPORTED.md.\n", len(a.SpecMapping), len(a.Unsupported))
			fmt.Fprintf(app.Stdout, "Next: review, then `hermes validate -c %s/hermes.yaml` and `hermes render -c %s/hermes.yaml` (OpenTofu via the platform renderer).\n", out, out)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&aPath, "assessment", "assessment.json", "Assessment file")
	f.StringVar(&out, "out", "", "output directory")
	f.StringVar(&project, "project", "", "override the project name")
	return cmd
}

func newMigratePlanCmd(app *App) *cobra.Command {
	var aPath, wfDir, out, md string
	cmd := &cobra.Command{
		Use:     "plan",
		Short:   "Generate the ordered migration plan (phases, steps, gates) from an assessment",
		Example: `  hermes migrate plan --assessment assessment.json --env production --out migration-plan.yaml --markdown plan.md`,
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			a, err := migration.LoadAssessment(aPath)
			if err != nil {
				return err
			}
			ws, err := migration.LoadWorkflows(wfDir)
			if err != nil {
				return err
			}
			p, err := migration.BuildPlan(a, ws, migration.PlanOptions{Environment: app.Env, Generator: "hermes/" + app.Build.Version, Now: migNow(app)})
			if err != nil {
				return err
			}
			data, err := p.YAML()
			if err != nil {
				return err
			}
			if md != "" {
				if err := migWriteFile(md, []byte(migration.PlanMarkdown(p))); err != nil {
					return err
				}
			}
			if out == "" || out == "-" {
				_, err = app.Stdout.Write(data)
				return err
			}
			if err := migWriteFile(out, data); err != nil {
				return err
			}
			if migJSON(app) {
				return migPrintJSON(app, map[string]any{"out": out, "plan_id": p.Metadata.PlanID, "summary": p.Summary})
			}
			s := p.Summary
			fmt.Fprintf(app.Stdout, "Plan %s: %d step(s) in %d phase(s); %d approval gate(s), %d validation gate(s), %d step(s) touch the source.\n",
				p.Metadata.PlanID[:12], s.Steps, s.Phases, s.ApprovalGates, s.ValidationGates, s.SourceImpactingSteps)
			fmt.Fprintf(app.Stdout, "Plan written to %s (target environment %s). Next: hermes migrate validate --plan %s --checks checks.yaml\n", out, p.Metadata.Target.Environment, out)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&aPath, "assessment", "assessment.json", "Assessment file")
	f.StringVar(&wfDir, "workflows", "", "MigrationWorkflow directory (default: embedded migration/workflows)")
	f.StringVar(&out, "out", "", "write the MigrationPlan YAML to this file (default: stdout)")
	f.StringVar(&md, "markdown", "", "also write a Markdown runbook")
	return cmd
}

func newMigrateValidateCmd(app *App) *cobra.Command {
	var planPath, checksPath, out string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Run validation checks for a plan and write a ValidationReport (exit 2 when a check fails)",
		Long: `Check types: spec-coverage (offline: the target spec covers every in-scope source resource),
bindings (hermes bindings -o json output), http (status and body text; TLS required unless
allow_insecure), tcp, dns (resolves to expected values) and command (a declared argv, never a shell
string; exit 0 passes).`,
		Example: `  hermes migrate validate --plan migration-plan.yaml --checks checks.yaml --out validation-report.json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := migration.LoadPlan(planPath)
			if err != nil {
				return err
			}
			c, err := migration.LoadChecks(checksPath)
			if err != nil {
				return Refused("%v", err)
			}
			rep := (&migration.Validator{Plan: p, Generator: "hermes/" + app.Build.Version, Now: migNow(app), Output: app.Stderr}).Run(cmd.Context(), c)
			data, err := migMarshalJSON(rep)
			if err != nil {
				return err
			}
			if out != "" && out != "-" {
				if err := migWriteFile(out, data); err != nil {
					return err
				}
			}
			switch {
			case migJSON(app) || out == "" || out == "-":
				if _, err := app.Stdout.Write(data); err != nil {
					return err
				}
			default:
				for _, r := range rep.Results {
					fmt.Fprintf(app.Stdout, "%-5s %-14s %-28s %s\n", strings.ToUpper(r.Status), r.Type, r.ID, r.Message)
				}
				fmt.Fprintf(app.Stdout, "%d/%d passed. Report written to %s (plan %s).\n", rep.Summary.Passed, rep.Summary.Total, out, p.Metadata.PlanID[:12])
			}
			if !rep.Passed {
				return &ExitError{Code: ExitRefused, Err: fmt.Errorf("validation failed: %d failed, %d error(s)", rep.Summary.Failed, rep.Summary.Errors)}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&planPath, "plan", "migration-plan.yaml", "MigrationPlan file")
	f.StringVar(&checksPath, "checks", "checks.yaml", "ValidationChecks file")
	f.StringVar(&out, "out", "", "write the ValidationReport JSON to this file (default: stdout)")
	return cmd
}

func newMigrateCutoverCmd(app *App) *cobra.Command {
	var (
		planPath, reportPath, hooksPath, approve, approveHooks, confirm, complete, note, step string
		approveSource                                                                         []string
		execute, status, rollback                                                             bool
		maxAge                                                                                time.Duration
	)
	cmd := &cobra.Command{
		Use:   "cutover",
		Short: "Gated cutover state machine (dry-run by default)",
		Long: `Show the cutover runbook and gates (default, dry-run), or execute it step by step.

--execute requires a passing validation report for the same plan, fresher than --max-report-age,
--approve <plan-id-prefix> (>= 8 chars) and --confirm-target <environment>. Execution only runs the hook
commands you declared per step in --hooks; Hermes has no built-in action that touches DNS, databases or
source resources. Steps that modify the source also need --approve-source-step <id>. Decommission steps
are never executed; record them with --complete-step. State: .hermes/migrations/<plan_id[:12]>/.

The dry-run records and prints the sha256 of the --hooks file; --execute and --rollback refuse a hooks
file whose digest differs and need --approve-hooks <prefix of that sha256> (re-run the dry-run, review
it and re-approve). Scripts invoked by the hooks are not hashed. Hook output is streamed to stderr only:
state.json and audit.jsonl keep the exit code, duration, output size and output sha256, never the output.
Hooks inherit your environment (they need credentials): use narrowly scoped, short-lived credentials.`,
		Example: `  hermes migrate cutover --plan migration-plan.yaml --report validation-report.json --hooks cutover-hooks.yaml
  hermes migrate cutover --plan migration-plan.yaml --report validation-report.json --hooks cutover-hooks.yaml \
      --execute --approve 3f2a9c1b --confirm-target production`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := migration.LoadPlan(planPath)
			if err != nil {
				return err
			}
			c, err := migration.OpenController(p, migWorkdir(app))
			if err != nil {
				return err
			}
			c.Now = migNow(app)
			c.Output = app.Stderr // hook output goes to the terminal only, never to state or audit
			if hooksPath != "" {
				if c.Hooks, err = migration.LoadHooks(hooksPath, p); err != nil {
					return Refused("%v", err)
				}
			}
			var rep *migration.Report
			if reportPath != "" {
				if rep, err = migration.LoadReport(reportPath); err != nil {
					return err
				}
			}
			gates := migration.ExecGates{Report: rep, MaxReportAge: maxAge, Approve: approve, ConfirmTarget: confirm, ApproveHooks: approveHooks}
			refusal := func(err error) error {
				if errors.Is(err, migration.ErrRefused) {
					return &ExitError{Code: ExitRefused, Err: err}
				}
				return err
			}
			switch {
			case complete != "":
				if err := c.CompleteStep(complete, approve, note); err != nil {
					return refusal(err)
				}
				fmt.Fprintf(app.Stdout, "Recorded %s as done (manual) by %s.\n", complete, c.User)
				return nil
			case rollback:
				res, err := c.Rollback(cmd.Context(), gates)
				if res != nil {
					migCutoverRollbackOut(app, res)
				}
				return refusal(err)
			case execute:
				res, err := c.Execute(cmd.Context(), gates, migSplit(approveSource), step)
				if res != nil {
					if migJSON(app) {
						_ = migPrintJSON(app, res)
					} else {
						for _, id := range res.Executed {
							fmt.Fprintf(app.Stdout, "executed %s\n", id)
						}
						if res.StoppedAt != "" {
							fmt.Fprintf(app.Stdout, "stopped at %s: %s\n", res.StoppedAt, res.Reason)
						}
						fmt.Fprintf(app.Stdout, "cutover status: %s\n", c.State.Status)
					}
				}
				return refusal(err)
			}
			// Dry-run / status: show gates and the runbook; never executes anything.
			// The dry-run (not --status) records the hooks file digest that --execute must match.
			if status {
				_ = c.Audit("status", "", "ok", "")
			} else if err := c.RecordDryRun(); err != nil {
				return err
			}
			gs := c.CheckGates(gates, true)
			rb := c.Runbook()
			if migJSON(app) {
				return migPrintJSON(app, map[string]any{"plan_id": p.Metadata.PlanID, "target_environment": p.Metadata.Target.Environment,
					"status": c.State.Status, "state_dir": c.Dir, "hooks_sha256": c.State.HooksSHA256, "gates": gs, "steps": rb})
			}
			fmt.Fprintf(app.Stdout, "Cutover for plan %s (target environment %s) — status %s. DRY-RUN: nothing is executed.\n\nGates for --execute:\n",
				p.Metadata.PlanID[:12], p.Metadata.Target.Environment, c.State.Status)
			for _, g := range gs {
				mark := "MISSING"
				if g.OK {
					mark = "ok"
				}
				fmt.Fprintf(app.Stdout, "  [%s] %s: %s\n", mark, g.Name, g.Detail)
			}
			fmt.Fprintln(app.Stdout, "\nRunbook:")
			for i, s := range rb {
				fmt.Fprintf(app.Stdout, "  %2d. [%s] %s (%s, gate %s, source impact %s)\n      %s\n      action: %s\n", i+1, s.Status, s.ID, s.Phase, s.Gate, s.SourceImpact, s.Title, s.Action)
			}
			if c.Hooks != nil {
				fmt.Fprintf(app.Stdout, "\nHooks file %s sha256 %s (recorded; --execute and --rollback refuse a different file)\n", hooksPath, c.Hooks.Digest())
				fmt.Fprintf(app.Stdout, "Approve these hooks with --approve-hooks %s. Scripts the hooks invoke are not hashed: review them too.\n", c.Hooks.Digest()[:12])
			}
			fmt.Fprintf(app.Stdout, "\nState and audit log: %s\n", c.Dir)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&planPath, "plan", "migration-plan.yaml", "MigrationPlan file")
	f.StringVar(&reportPath, "report", "", "ValidationReport for the same plan (required for --execute)")
	f.StringVar(&hooksPath, "hooks", "", "CutoverHooks file: step id -> run/rollback argv")
	f.BoolVar(&execute, "execute", false, "run declared hooks for pending cutover steps (gated)")
	f.StringVar(&approve, "approve", "", "plan id prefix (>= 8 characters) approving this plan")
	f.StringVar(&approveHooks, "approve-hooks", "", "hooks file sha256 prefix (>= 8 characters, printed by the dry-run) approving these hooks; required with --hooks for --execute/--rollback")
	f.StringVar(&confirm, "confirm-target", "", "target environment name, typed to confirm")
	f.StringArrayVar(&approveSource, "approve-source-step", nil, "approve one source-impacting step (repeatable)")
	f.StringVar(&step, "step", "", "with --execute: run only this step")
	f.StringVar(&complete, "complete-step", "", "record a manually performed step as done")
	f.StringVar(&note, "note", "", "note recorded with --complete-step")
	f.BoolVar(&status, "status", false, "show the recorded state (same as dry-run)")
	f.BoolVar(&rollback, "rollback", false, "run declared rollback hooks for completed reversible steps; list irreversible ones")
	f.DurationVar(&maxAge, "max-report-age", 4*time.Hour, "maximum age of the validation report for --execute")
	return cmd
}

func migCutoverRollbackOut(app *App, res *migration.RollbackResult) {
	if migJSON(app) {
		_ = migPrintJSON(app, res)
		return
	}
	for _, id := range res.RolledBack {
		fmt.Fprintf(app.Stdout, "rolled back %s\n", id)
	}
	for _, m := range res.ManualRequired {
		fmt.Fprintf(app.Stdout, "manual rollback required: %s\n", m)
	}
	for _, m := range res.Irreversible {
		fmt.Fprintf(app.Stdout, "irreversible (cannot be rolled back automatically): %s\n", m)
	}
	fmt.Fprintf(app.Stdout, "cutover status: %s\n", res.Status)
}
