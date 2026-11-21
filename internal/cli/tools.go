package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nityam02/hermes-cloud-foundation/infra"
	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diagnostics"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/providers"
	"github.com/nityam02/hermes-cloud-foundation/internal/render"
	"github.com/nityam02/hermes-cloud-foundation/internal/ui"
)

func init() {
	Register(newDoctorCmd)
	Register(newProvidersCmd)
	Register(newVersionCmd)
	Register(newCostCmd)
}

func newDoctorCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the local toolchain, credential sources, spec and workspace",
		Long: `Doctor checks the engine (and warns below OpenTofu 1.12), optional tools (presence
only), credential sources (environment variable NAMES and whether well-known files exist;
values are never read and no cloud API is called), the spec, that .hermes/ is gitignored,
and free disk space. Exit code 2 when a required tool is missing or the spec is invalid.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			r := diagnostics.Run(cmd.Context(), diagnostics.Env{
				WorkDir: app.workDir(), ConfigPath: app.configPath(), Getenv: app.getenv, LookPath: app.lookPath,
				Detect: app.detectEngine,
			})
			if app.jsonOut() {
				if err := ui.JSON(app.Stdout, r); err != nil {
					return err
				}
			} else {
				rows := [][]string{{"STATUS", "CATEGORY", "CHECK", "DETAIL"}}
				for _, c := range r.Checks {
					rows = append(rows, []string{c.Status, c.Category, c.Name, c.Detail})
				}
				ui.Table(app.Stdout, rows)
			}
			if r.Failed {
				return &ExitError{Code: ExitRefused}
			}
			return nil
		},
	}
}

func newProvidersCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "providers [name]",
		Short: "List providers, their capability matrix, extension keys and pins",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			list := providers.All()
			if len(args) == 1 {
				a, ok := providers.Get(args[0])
				if !ok {
					return Refused("unknown provider %q (known: %s)", args[0], strings.Join(config.Providers, ", "))
				}
				list = []providers.Adapter{a}
			}
			type entry struct {
				Name          string                        `json:"name"`
				DisplayName   string                        `json:"display_name"`
				Providers     []providers.TerraformProvider `json:"engine_providers"`
				Capabilities  providers.CapabilityMatrix    `json:"capabilities"`
				ExtensionKeys []providers.ExtensionKey      `json:"extension_keys"`
				Regions       []string                      `json:"regions"`
			}
			var out []entry
			for _, a := range list {
				out = append(out, entry{a.Name(), a.DisplayName(), append(a.Providers(), providers.SharedProviders()...),
					a.Capabilities(), a.ExtensionKeys(), a.Regions()})
			}
			if app.jsonOut() {
				return ui.JSON(app.Stdout, map[string]any{"verification": providers.MatrixVerification, "providers": out})
			}
			if len(list) > 1 {
				rows := [][]string{append([]string{"FEATURE"}, names(list)...)}
				for _, f := range providers.AllFeatures {
					row := []string{string(f)}
					for _, a := range list {
						row = append(row, string(a.Capabilities().Get(f).Status))
					}
					rows = append(rows, row)
				}
				ui.Table(app.Stdout, rows)
				fmt.Fprintf(app.Stdout, "\nNote: %s.\nDetails: hermes providers <name>\n", providers.MatrixVerification)
				return nil
			}
			e := out[0]
			fmt.Fprintf(app.Stdout, "%s (%s)\n", e.DisplayName, e.Name)
			for _, p := range e.Providers {
				fmt.Fprintf(app.Stdout, "  engine provider %s %s\n", p.Source, p.Version)
			}
			rows := [][]string{{"FEATURE", "STATUS", "NOTE"}}
			for _, f := range providers.AllFeatures {
				c := e.Capabilities.Get(f)
				rows = append(rows, []string{string(f), string(c.Status), c.Note})
			}
			ui.Table(app.Stdout, rows)
			fmt.Fprintln(app.Stdout, "\nExtension keys (extensions."+e.Name+".*):")
			if len(e.ExtensionKeys) == 0 {
				fmt.Fprintln(app.Stdout, "  none")
			}
			for _, k := range e.ExtensionKeys {
				fmt.Fprintf(app.Stdout, "  %-26s %s\n", k.Name, k.Description)
			}
			fmt.Fprintf(app.Stdout, "\nKnown regions: %s\nNote: %s.\n", strings.Join(e.Regions, ", "), providers.MatrixVerification)
			return nil
		},
	}
}

func names(as []providers.Adapter) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Name())
	}
	return out
}

func newVersionCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the hermes version, embedded modules digest and engine provider pins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := app.format("text", "json"); err != nil {
				return err
			}
			digest, err := render.DigestFS(infra.FS)
			if err != nil {
				return err
			}
			pins := []providers.TerraformProvider{}
			for _, a := range providers.All() {
				pins = append(pins, a.Providers()...)
			}
			pins = append(pins, providers.SharedProviders()...)
			info := map[string]any{
				"version": app.Build.Version, "commit": app.Build.Commit, "date": app.Build.Date,
				"modules_digest": digest, "engine_providers": pins,
				"engine_minimums": map[string]string{"tofu": execution.MinOpenTofu.String(), "terraform": execution.MinTerraform.String()},
			}
			if app.jsonOut() {
				return ui.JSON(app.Stdout, info)
			}
			fmt.Fprintf(app.Stdout, "hermes %s (commit %s, built %s)\nembedded modules %s\nengines: OpenTofu >= %s, Terraform >= %s\n",
				app.Build.Version, app.Build.Commit, app.Build.Date, short(digest), execution.MinOpenTofu, execution.MinTerraform)
			for _, p := range pins {
				fmt.Fprintf(app.Stdout, "  %s %s\n", p.Source, p.Version)
			}
			return nil
		},
	}
}

func newCostCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "cost",
		Short: "Estimate the cost of the saved plan with Infracost (when installed)",
		Long: `Cost runs 'infracost breakdown --path <plan.json>' on the saved plan of the
environment. Hermes never invents numbers: without Infracost, or for providers Infracost
does not price (Alibaba Cloud), it explains what is missing. Infracost uses its own pricing
API and API key; see https://www.infracost.io/docs/.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := app.format("text", "json")
			if err != nil {
				return err
			}
			l, err := app.load()
			if err != nil {
				return err
			}
			if l.p.Cloud.Provider == config.ProviderAlibaba {
				fmt.Fprintln(app.Stdout, "Infracost does not price Alibaba Cloud resources, so hermes cannot estimate this environment.\n"+
					"Use the Alibaba Cloud pricing calculator with the resources listed in .hermes/plans/"+l.p.Environment+"/plan.json.")
				return nil
			}
			sp, err := execution.LoadPlan(app.workspace().PlanDir(l.p.Environment, execution.KindApply))
			if err != nil {
				return err
			}
			if _, err := os.Stat(sp.PlanJSON); err != nil {
				return Refused("saved plan JSON missing; run `hermes plan`")
			}
			bin, err := app.lookPath("infracost")
			if err != nil {
				fmt.Fprintln(app.Stdout, "Infracost is not installed, so no estimate was produced (hermes never invents numbers).\n"+
					"Install it from https://www.infracost.io/docs/ and run `hermes cost` again; it will read "+sp.PlanJSON+".")
				return nil
			}
			ifmt := "table"
			if format == "json" {
				ifmt = "json"
			}
			out, stderr, err := app.runTool(cmd.Context(), bin, "breakdown", "--path", sp.PlanJSON, "--format", ifmt, "--no-color")
			if err != nil {
				return fmt.Errorf("infracost: %w: %s", err, strings.TrimSpace(string(stderr)))
			}
			_, err = app.Stdout.Write(out)
			return err
		},
	}
}
