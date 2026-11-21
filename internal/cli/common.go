package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/providers"
	"github.com/nityam02/hermes-cloud-foundation/internal/render"
	"github.com/nityam02/hermes-cloud-foundation/internal/ui"
)

// HermesDirName is the working directory for generated files (gitignored).
const HermesDirName = ".hermes"

func (app *App) workDir() string {
	if app.WorkDir != "" {
		return app.WorkDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func (app *App) workspace() execution.Workspace {
	return execution.Workspace{Dir: filepath.Join(app.workDir(), HermesDirName)}
}

func (app *App) configPath() string {
	p := app.ConfigPath
	if p == "" {
		p = "hermes.yaml"
	}
	if !filepath.IsAbs(p) && app.WorkDir != "" {
		p = filepath.Join(app.WorkDir, p)
	}
	return p
}

// format validates --output against the allowed values.
func (app *App) format(allowed ...string) (string, error) {
	f := app.Output
	if f == "" {
		f = "text"
	}
	for _, x := range allowed {
		if f == x {
			return f, nil
		}
	}
	return "", Refused("--output %s is not supported by this command (use %v)", f, allowed)
}

func (app *App) jsonOut() bool { return app.Output == "json" }

// loaded is a resolved environment with its provider adapter and diagnostics.
type loaded struct {
	doc     *config.Document
	p       *config.Platform
	adapter providers.Adapter
	diags   diag.Diagnostics
}

// load parses the spec and resolves one environment (app.Env or project.environment),
// including provider validation. Errors are returned as a refusal after printing the
// diagnostics (text mode) so every command reports problems the same way.
func (app *App) load() (*loaded, error) {
	path := app.configPath()
	doc, ds, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	if doc == nil || ds.HasErrors() {
		return nil, app.diagRefusal(ds, "the spec %s is invalid", path)
	}
	p, rds, err := doc.Resolve(app.Env)
	if err != nil {
		return nil, err
	}
	ds = append(ds, rds...)
	if p == nil {
		return nil, app.diagRefusal(ds, "the spec %s is invalid", path)
	}
	ad, ok := providers.Get(p.Cloud.Provider)
	if !ok {
		return nil, fmt.Errorf("no adapter for provider %q", p.Cloud.Provider)
	}
	ds = append(ds, ad.Validate(p)...)
	if ds.HasErrors() {
		return nil, app.diagRefusal(ds, "the spec %s is invalid for environment %s", path, p.Environment)
	}
	return &loaded{doc: doc, p: p, adapter: ad, diags: ds}, nil
}

// diagRefusal prints diagnostics to stderr and returns a refusal (exit 2).
func (app *App) diagRefusal(ds diag.Diagnostics, format string, args ...any) error {
	ui.Diagnostics(app.Stderr, ds)
	return Refused(format, args...)
}

// printWarnings writes non-error diagnostics to stderr (data stays on stdout).
func (app *App) printWarnings(ds diag.Diagnostics) {
	var out diag.Diagnostics
	for _, d := range ds {
		if d.Severity == diag.SeverityWarning || (app.Verbose && d.Severity == diag.SeverityInfo) {
			out = append(out, d)
		}
	}
	for _, d := range out.Sorted() {
		fmt.Fprintln(app.Stderr, d.String())
	}
}

// renderOptions builds render options for an environment.
type renderFlags struct {
	refreshModules bool
}

func (app *App) renderOptions(env, engine string, v execution.Version, rf renderFlags) render.Options {
	ws := app.workspace()
	return render.Options{
		OutDir: ws.BuildDir(env), HermesDir: ws.Dir, Version: app.Build.Version,
		Engine: engine, EngineVersion: v, ReplaceModules: rf.refreshModules,
	}
}

// engineFor detects the engine for a platform and enforces engine-specific rules.
func (app *App) engineFor(ctx context.Context, p *config.Platform, preference string) (*execution.Engine, error) {
	if preference == "" {
		preference = p.Engine.Binary
	}
	eng, err := app.detectEngine(ctx, preference)
	if err != nil {
		return nil, err
	}
	if p.State.Encryption.ClientSide && !eng.IsOpenTofu() {
		return nil, Refused("state.encryption.client_side requires OpenTofu; detected %s", eng.Name)
	}
	return eng, nil
}

// planChecks runs the provider's plan-time rules (and resolves the GCP project from
// GOOGLE_CLOUD_PROJECT when the spec has none, so the rendered root is explicit).
func (app *App) planChecks(l *loaded) error {
	ds := l.adapter.PlanChecks(l.p, app.getenv)
	if ds.HasErrors() {
		return app.diagRefusal(ds, "environment %s cannot be planned", l.p.Environment)
	}
	if l.p.Cloud.Provider == config.ProviderGCP && l.p.Cloud.Account == "" {
		l.p.Cloud.Account = app.getenv("GOOGLE_CLOUD_PROJECT")
		fmt.Fprintf(app.Stderr, "note: using GCP project %s from GOOGLE_CLOUD_PROJECT\n", l.p.Cloud.Account)
	}
	app.printWarnings(ds)
	return nil
}

func (app *App) history(env string, ev execution.HistoryEvent) {
	ev.Environment = env
	if ev.User == "" {
		ev.User = app.userName()
	}
	if ev.Time.IsZero() {
		ev.Time = app.now().UTC()
	}
	if err := execution.AppendHistory(app.workspace().HistoryFile(env), ev); err != nil {
		fmt.Fprintln(app.Stderr, "warning: could not write history:", err)
	}
}

// historyForError records a refusal or failure.
func (app *App) historyForError(env, action, planID string, err error) {
	ev := execution.HistoryEvent{Action: action, PlanID: planID, Result: "failed", Reason: err.Error()}
	var ge *execution.GateError
	if errors.As(err, &ge) {
		ev.Result, ev.Code, ev.Reason = "refused", ge.Code, ge.Reason
	}
	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == ExitRefused {
		ev.Result = "refused"
	}
	app.history(env, ev)
}
