package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/render"
	"github.com/nityam02/hermes-cloud-foundation/internal/state"
	"github.com/nityam02/hermes-cloud-foundation/internal/ui"
)

func init() { Register(newStateCmd) }

const bootstrapExplanation = `The state backend cannot hold its own state before it exists (chicken-and-egg), so the
bootstrap root uses LOCAL state under .hermes/state/_bootstrap/. After apply you can keep that
file safe (it describes only the state bucket) or migrate it into the new bucket; see
docs/operations/state-management.md.`

func newStateCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "state", Short: "State backend: show the location, bootstrap the bucket"}
	cmd.AddCommand(newStateShowCmd(app), newStateBootstrapCmd(app))
	return cmd
}

func newStateShowCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show where the state of an environment lives and how it is locked and encrypted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			l, err := app.load()
			if err != nil {
				return err
			}
			p := l.p
			be, err := state.ForPlatform(p, ".hermes/state/"+state.Key(p), false)
			if err != nil {
				return Refused("%v", err)
			}
			lock := map[string]string{
				config.BackendS3: "S3 native lockfile (use_lockfile)", config.BackendGCS: "GCS native locking",
				config.BackendLocal: "local file lock (single machine only)",
			}[be.Type]
			if be.Type == config.BackendS3 && p.State.LockTable != "" {
				lock = "DynamoDB table " + p.State.LockTable
			}
			if be.Type == config.BackendOSS {
				lock = "none (set extensions.alibaba.state_tablestore_*)"
				if _, ok := be.Config["tablestore_table"]; ok {
					lock = fmt.Sprintf("Tablestore table %v", be.Config["tablestore_table"])
				}
			}
			enc := "backend server-side encryption"
			if p.State.Encryption.ClientSide {
				enc += " + OpenTofu client-side encryption (" + p.State.Encryption.KMSKey + ")"
			}
			info := map[string]any{"environment": p.Environment, "backend": be.Type, "location": be.Location(), "config": be.Config, "locking": lock, "encryption": enc}
			if app.jsonOut() {
				return ui.JSON(app.Stdout, info)
			}
			fmt.Fprintf(app.Stdout, "environment %s\n  backend:    %s\n  location:   %s\n  locking:    %s\n  encryption: %s\n",
				p.Environment, be.Type, be.Location(), lock, enc)
			return nil
		},
	}
}

func newStateBootstrapCmd(app *App) *cobra.Command {
	var (
		apply bool
		af    applyFlags
		rf    renderFlags
	)
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Plan (then, with --apply --approve, create) the remote state bucket",
		Long: `Bootstrap renders infra/bootstrap/<provider>/state-backend into
.hermes/build/_bootstrap-<provider>/ with local state and saves a plan. Applying it goes
through the same saved-plan gates as 'hermes apply' and always needs --approve, because
the state bucket protects every environment.

` + bootstrapExplanation,
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
			if p.State.Backend == config.BackendLocal {
				return Refused("state.backend is local: there is no remote backend to bootstrap")
			}
			if p.Cloud.Provider == config.ProviderGCP && p.Cloud.Account == "" {
				if p.Cloud.Account = app.getenv("GOOGLE_CLOUD_PROJECT"); p.Cloud.Account == "" {
					return Refused("GCP needs a project: set cloud.account or export GOOGLE_CLOUD_PROJECT")
				}
			}
			env := render.BootstrapEnvironment(p.Cloud.Provider)
			ws := app.workspace()
			eng, err := app.engineFor(cmd.Context(), p, "")
			if err != nil {
				return err
			}
			opts := app.renderOptions(env, eng.Name, eng.Version, rf)

			if apply {
				sp, err := execution.LoadPlan(ws.PlanDir(env, execution.KindApply))
				if err != nil {
					return err
				}
				cur, err := render.BuildBootstrap(p, opts)
				if err != nil {
					return err
				}
				req := execution.ApplyRequest{
					Environment: env, Protected: true, CurrentDigest: cur.Digest, CurrentModulesDigest: cur.ModulesDigest,
					Approve: af.approve, MaxAge: af.maxAge, Now: app.now, Interactive: app.interactive(),
				}
				if err := execution.Apply(cmd.Context(), eng, cur.Dir, sp, req); err != nil {
					app.historyForError(env, "bootstrap-apply", sp.Meta.PlanID, err)
					return err
				}
				app.history(env, execution.HistoryEvent{Action: "bootstrap-apply", Result: "ok", PlanID: sp.Meta.PlanID, Engine: eng.Name,
					Changes: execution.CountsOf(sp.Meta.Summary)})
				out, err := eng.OutputJSON(cmd.Context(), cur.Dir)
				if err != nil {
					return err
				}
				cfg := outputValue(out, "backend_config")
				if app.jsonOut() {
					return ui.JSON(app.Stdout, map[string]any{"bucket": outputValue(out, "bucket"), "backend_config": cfg})
				}
				data, _ := json.MarshalIndent(cfg, "", "  ")
				fmt.Fprintf(app.Stdout, "State backend created. backend_config:\n%s\n", data)
				if p.Cloud.Provider == config.ProviderAlibaba {
					fmt.Fprintln(app.Stdout, "Copy tablestore_endpoint and tablestore_table into extensions.alibaba.state_tablestore_endpoint / state_tablestore_table.")
				}
				fmt.Fprintln(app.Stdout, bootstrapExplanation)
				return nil
			}

			res, err := render.RenderBootstrap(p, opts)
			if err != nil {
				return err
			}
			sp, err := execution.CreatePlan(cmd.Context(), eng, execution.PlanRequest{
				RootDir: res.Dir, PlanDir: ws.PlanDir(env, execution.KindApply), Now: app.now,
				Meta: execution.PlanMeta{Project: p.Project.Name, Environment: env, Provider: p.Cloud.Provider, Protected: true,
					RenderDigest: res.Digest, ModulesDigest: res.ModulesDigest, User: app.userName()},
			})
			if err != nil {
				app.historyForError(env, "bootstrap-plan", "", err)
				return err
			}
			app.history(env, execution.HistoryEvent{Action: "bootstrap-plan", Result: "ok", PlanID: sp.Meta.PlanID, Engine: eng.Name,
				Changes: execution.CountsOf(sp.Meta.Summary)})
			o := outcome(sp)
			o.Next = fmt.Sprintf("hermes state bootstrap --apply --approve %s", sp.Meta.PlanID[:12])
			if err := app.printPlan(o); err != nil {
				return err
			}
			if !app.jsonOut() {
				fmt.Fprintln(app.Stdout, bootstrapExplanation)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "apply the saved bootstrap plan (requires --approve)")
	cmd.Flags().StringVar(&af.approve, "approve", "", "plan-id prefix (>= 8 characters) of the saved bootstrap plan")
	cmd.Flags().DurationVar(&af.maxAge, "max-plan-age", execution.DefaultMaxPlanAge, "refuse saved plans older than this")
	cmd.Flags().BoolVar(&rf.refreshModules, "refresh-modules", false, "replace an extracted modules directory that differs from the embedded modules")
	return cmd
}
