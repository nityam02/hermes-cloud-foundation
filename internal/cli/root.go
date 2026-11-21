// Package cli implements the hermes command-line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
)

// Process exit codes. They are part of the CLI contract and are documented in docs/reference/cli.md.
const (
	ExitOK      = 0 // success
	ExitFailure = 1 // unexpected error
	ExitRefused = 2 // validation failed or a safety gate refused the operation
	ExitChanges = 3 // plan succeeded and contains changes (only with --detailed-exitcode)
)

// BuildInfo is injected by the linker at build time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// App carries global options and I/O shared by all commands.
type App struct {
	Build BuildInfo

	ConfigPath string // --config (default hermes.yaml)
	Env        string // --env (default: project.environment from the spec)
	Output     string // --output text|json
	WorkDir    string // --workdir (default: current directory)
	NoColor    bool
	Verbose    bool

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Hooks with real defaults (see hooks.go); tests replace them. Nil means default.
	DetectEngine func(ctx context.Context, preference string) (*execution.Engine, error)
	Getenv       func(string) string
	LookPath     func(string) (string, error)
	Now          func() time.Time
	UserName     func() string // OS user + git user.name, for plan metadata and history
	Interactive  func() bool   // stdin and stdout are terminals
	// RunTool runs an auxiliary tool (conftest, infracost) and returns stdout/stderr.
	RunTool func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// ExitError lets a command choose the process exit code while still returning an error.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// Refused returns an ExitError with ExitRefused, for validation failures and safety gates.
func Refused(format string, args ...any) error {
	return &ExitError{Code: ExitRefused, Err: fmt.Errorf(format, args...)}
}

// NewRootCommand builds the root command with every registered sub-command.
func NewRootCommand(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:   "hermes",
		Short: "Cloud-independent infrastructure and migration platform",
		Long: `Hermes Cloud Foundation: describe infrastructure once in a cloud-independent spec,
deploy it to AWS, GCP or Alibaba Cloud through OpenTofu/Terraform, and plan
Terraform-independent migrations between clouds with explicit safety gates.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", app.Build.Version, app.Build.Commit, app.Build.Date),
	}
	root.SetIn(app.Stdin)
	root.SetOut(app.Stdout)
	root.SetErr(app.Stderr)

	// Flag defaults are the App's preset values (pflag assigns the default immediately),
	// so callers such as tests can preset WorkDir and friends.
	if app.ConfigPath == "" {
		app.ConfigPath = "hermes.yaml"
	}
	if app.Output == "" {
		app.Output = "text"
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&app.ConfigPath, "config", "c", app.ConfigPath, "path to the platform spec")
	pf.StringVarP(&app.Env, "env", "e", app.Env, "environment to operate on (default: project.environment)")
	pf.StringVarP(&app.Output, "output", "o", app.Output, "output format: text or json")
	pf.StringVar(&app.WorkDir, "workdir", app.WorkDir, "working directory (default: current directory)")
	pf.BoolVar(&app.NoColor, "no-color", app.NoColor, "disable colored output")
	pf.BoolVarP(&app.Verbose, "verbose", "v", app.Verbose, "verbose output")

	for _, f := range registeredFactories() {
		root.AddCommand(f(app))
	}
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute(build BuildInfo) int {
	app := &App{Build: build, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	return Run(app, os.Args[1:])
}

// Run executes the CLI with explicit arguments; used by tests.
func Run(app *App, args []string) int {
	root := NewRootCommand(app)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		if ee.Err != nil {
			fmt.Fprintln(app.Stderr, "Error:", ee.Err)
		}
		return ee.Code
	}
	var ge *execution.GateError
	if errors.As(err, &ge) {
		fmt.Fprintln(app.Stderr, "Refused:", ge)
		return ExitRefused
	}
	fmt.Fprintln(app.Stderr, "Error:", err)
	return ExitFailure
}
