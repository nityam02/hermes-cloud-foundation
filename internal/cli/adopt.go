package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
	"github.com/nityam02/hermes-cloud-foundation/internal/migration/adopt"
	"github.com/nityam02/hermes-cloud-foundation/internal/policy"
)

func init() { Register(newAdoptCmd) }

// migAdoptEngine resolves the engine for adoption; replaced in tests with a fake.
var migAdoptEngine = func(ctx context.Context, app *App, preference string) (adopt.Engine, error) {
	if app.DetectEngine != nil {
		return app.DetectEngine(ctx, preference)
	}
	e, err := execution.Detect(ctx, preference)
	if err != nil {
		return nil, err
	}
	e.Stdout, e.Stderr = app.Stderr, app.Stderr
	return e, nil
}

func newAdoptCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Adopt existing SOURCE resources into IaC with import-only plans (never modifies them)",
		Long: `IaC adoption imports existing, possibly hand-built source resources into a separate
"source adoption" state. It is not migration and never touches the target state. Every plan is
verified to be import-only: any create, update, replace or delete is refused.`,
	}
	cmd.AddCommand(newAdoptAWSCmd(app), newAdoptPlanCmd(app), newAdoptVerifyCmd(app), newAdoptApplyCmd(app))
	return cmd
}

func newAdoptAWSCmd(app *App) *cobra.Command {
	var invPath, out string
	var o adopt.Options
	var types, ids []string
	cmd := &cobra.Command{
		Use:     "aws",
		Short:   "Generate import blocks for AWS resources from an inventory",
		Example: `  hermes adopt aws --inventory inventory.json --types AWS::S3::Bucket,AWS::SQS::Queue --out adopt/`,
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if out == "" {
				return Refused("--out <dir> is required")
			}
			inv, err := inventory.Load(invPath)
			if err != nil {
				return err
			}
			o.Types, o.IDs = migSplit(types), migSplit(ids)
			m, err := adopt.Generate(inv, out, o)
			if err != nil {
				return Refused("%v", err)
			}
			fmt.Fprintf(app.Stderr, "warning: %s.\n", adopt.StateWarning)
			if migJSON(app) {
				return migPrintJSON(app, m)
			}
			fmt.Fprintf(app.Stdout, "Wrote %d import block(s) to %s (providers.tf, imports.tf, backend.tf, README.md, adoption.json); %d resource(s) skipped.\n", len(m.Imports), out, len(m.Skipped))
			fmt.Fprintf(app.Stdout, "Next (needs read-only source credentials): hermes adopt plan --dir %s\n", out)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&invPath, "inventory", "inventory.json", "Inventory file")
	f.StringSliceVar(&types, "types", nil, "only these inventory types (comma-separated)")
	f.StringSliceVar(&ids, "ids", nil, "only these resource ids")
	f.BoolVar(&o.IncludeManaged, "include-managed", false, "also adopt resources already managed by Terraform/CloudFormation (creates dual management)")
	f.StringVar(&o.BackendBucket, "backend-bucket", "", "S3 bucket for the separate source-adoption state (default: local state)")
	f.StringVar(&o.BackendRegion, "backend-region", "", "region of --backend-bucket")
	f.BoolVar(&o.AllowLocalState, "allow-local-state", false, "accept a LOCAL, unencrypted adoption state (it holds full resource configuration; default: refused, use --backend-bucket)")
	f.StringVar(&out, "out", "", "output directory")
	return cmd
}

func adoptDirFlags(cmd *cobra.Command, dir, engine *string) {
	cmd.Flags().StringVar(dir, "dir", "", "adoption workspace directory (from hermes adopt aws)")
	cmd.Flags().StringVar(engine, "engine", "auto", "engine: auto, tofu or terraform")
	_ = cmd.MarkFlagRequired("dir")
}

// adoptPolicy evaluates the embedded plan policies on the adoption plan (F-08). Findings are
// advisory (printed as warnings, recorded in the verification): verify already refuses every
// non-import change, so a finding describes the existing source configuration being imported
// (for example a public bucket or a secret in state), not a change Hermes would make.
func adoptPolicy(ctx context.Context, app *App, dir string, v *adopt.Verification) {
	if v == nil {
		return
	}
	planJSON := filepath.Join(dir, adopt.PlanJSONFile)
	if _, err := os.Stat(planJSON); err != nil {
		return
	}
	rec, ds, err := policy.Check(ctx, planJSON, policy.Options{HermesDir: app.workspace().Dir, Version: app.Build.Version,
		LookPath: app.lookPath, Run: policy.Runner(app.runTool), Now: app.now,
		Data: map[string]any{"hermes": map[string]any{"protected": false}}})
	if err != nil {
		fmt.Fprintf(app.Stderr, "warning: the adoption plan policy check failed: %v\n", err)
		return
	}
	v.Policy = &rec
	for _, d := range ds.Sorted() {
		d.Severity = diag.SeverityWarning // advisory for adoption
		fmt.Fprintln(app.Stderr, d.String()+" (advisory: existing source configuration)")
	}
}

func adoptReport(app *App, v *adopt.Verification, err error) error {
	if v != nil && migJSON(app) {
		_ = migPrintJSON(app, v)
	} else if v != nil {
		fmt.Fprintf(app.Stdout, "Adoption plan %s: %d import(s), %d non-import change(s).\n", v.PlanID[:12], v.Imports, len(v.Offending)+len(v.Unexpected))
		for _, it := range v.Offending {
			fmt.Fprintf(app.Stdout, "  REFUSED %s: %s\n", it.Address, it.Action)
		}
		for _, a := range v.Unexpected {
			fmt.Fprintf(app.Stdout, "  REFUSED %s: import not in adoption.json\n", a)
		}
	}
	switch {
	case errors.Is(err, adopt.ErrNotImportOnly):
		return &ExitError{Code: ExitRefused, Err: fmt.Errorf("%w (an adoption must never create, modify, replace or delete; fix generated.tf and re-plan)", err)}
	case errors.Is(err, adopt.ErrApproval):
		return &ExitError{Code: ExitRefused, Err: fmt.Errorf("%w (copy the plan id printed by hermes adopt plan or verify)", err)}
	case errors.Is(err, adopt.ErrLocalState):
		return &ExitError{Code: ExitRefused, Err: err}
	}
	return err
}

func newAdoptPlanCmd(app *App) *cobra.Command {
	var dir, engine string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "init + plan -generate-config-out (first run) into adopt.tfplan, then verify it is import-only",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := migAdoptEngine(cmd.Context(), app, engine)
			if err != nil {
				return err
			}
			fmt.Fprintf(app.Stderr, "warning: %s.\n", adopt.StateWarning)
			v, err := adopt.Plan(cmd.Context(), e, dir)
			adoptPolicy(cmd.Context(), app, dir, v)
			if err == nil && !migJSON(app) {
				defer fmt.Fprintf(app.Stdout, "Review generated.tf (it may contain sensitive values), then: hermes adopt apply --dir %s --approve %s\n", dir, v.PlanID[:8])
			}
			return adoptReport(app, v, err)
		},
	}
	adoptDirFlags(cmd, &dir, &engine)
	return cmd
}

func newAdoptVerifyCmd(app *App) *cobra.Command {
	var dir, engine string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Refuse unless the saved adoption plan only imports (show -json; no create/update/replace/delete)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := migAdoptEngine(cmd.Context(), app, engine)
			if err != nil {
				return err
			}
			v, err := adopt.Verify(cmd.Context(), e, dir)
			adoptPolicy(cmd.Context(), app, dir, v)
			return adoptReport(app, v, err)
		},
	}
	adoptDirFlags(cmd, &dir, &engine)
	return cmd
}

func newAdoptApplyCmd(app *App) *cobra.Command {
	var dir, engine, approve string
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a verified import-only adoption plan (requires --approve <plan-id-prefix>)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := migAdoptEngine(cmd.Context(), app, engine)
			if err != nil {
				return err
			}
			v, err := adopt.Apply(cmd.Context(), e, dir, approve)
			if err == nil && !migJSON(app) {
				defer fmt.Fprintln(app.Stdout, "Imported into the source adoption state. Nothing was created, changed or deleted.")
			}
			return adoptReport(app, v, err)
		},
	}
	adoptDirFlags(cmd, &dir, &engine)
	cmd.Flags().StringVar(&approve, "approve", "", "adoption plan id prefix (>= 8 characters)")
	return cmd
}
