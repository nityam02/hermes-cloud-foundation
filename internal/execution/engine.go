// Package execution runs the infrastructure engine (OpenTofu or Terraform).
//
// Hermes never reimplements planning, applying, state or import: it shells out to the
// engine binary and adds safety gates around it. This file is the low-level runner;
// gates live in gates.go.
package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Engine binary names.
const (
	BinaryOpenTofu  = "tofu"
	BinaryTerraform = "terraform"
)

// Minimum supported engine versions. The floor is set by S3 native state locking
// (use_lockfile: OpenTofu 1.10, Terraform 1.11); mock_provider tests, import blocks and
// -generate-config-out are available in both at these versions. `hermes doctor` additionally
// warns when the engine is older than the oldest release still receiving security fixes.
var (
	MinOpenTofu  = Version{1, 10, 0}
	MinTerraform = Version{1, 11, 0}
)

// ErrEngineNotFound is returned when no supported engine binary is on PATH.
var ErrEngineNotFound = errors.New("no infrastructure engine found: install OpenTofu (https://opentofu.org/docs/intro/install/) or Terraform")

// Version is a semantic version triple.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v < o.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion extracts the first x.y.z from s.
func ParseVersion(s string) (Version, error) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("no version in %q", s)
	}
	maj, _ := strconv.Atoi(m[1])
	mnr, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return Version{maj, mnr, pat}, nil
}

// Engine is a resolved engine binary.
type Engine struct {
	Name    string // tofu | terraform
	Path    string // absolute path to the binary
	Version Version

	// Env is appended to a sanitized copy of the process environment for every invocation
	// (see environment).
	Env []string
	// Stdout/Stderr receive streamed engine output (nil discards).
	Stdout io.Writer
	Stderr io.Writer

	// lookPath and runner are replaceable for tests.
	runner Runner
	// warnedCLIArgs: the TF_CLI_ARGS warning is printed once per engine.
	warnedCLIArgs bool
}

// Runner executes a command; replaced in tests.
type Runner func(ctx context.Context, dir string, env []string, stdout, stderr io.Writer, name string, args ...string) error

func defaultRunner(ctx context.Context, dir string, env []string, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// Detect resolves the engine according to preference: "auto" (prefer tofu, then terraform),
// "tofu" or "terraform".
func Detect(ctx context.Context, preference string) (*Engine, error) {
	return detect(ctx, preference, exec.LookPath, defaultRunner)
}

func detect(ctx context.Context, preference string, lookPath func(string) (string, error), run Runner) (*Engine, error) {
	var candidates []string
	switch preference {
	case "", "auto":
		candidates = []string{BinaryOpenTofu, BinaryTerraform}
	case BinaryOpenTofu, BinaryTerraform:
		candidates = []string{preference}
	default:
		return nil, fmt.Errorf("unknown engine %q (want auto, tofu or terraform)", preference)
	}
	for _, name := range candidates {
		p, err := lookPath(name)
		if err != nil {
			continue
		}
		e := &Engine{Name: name, Path: p, runner: run}
		v, err := e.probeVersion(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s found at %s but its version could not be determined: %w", name, p, err)
		}
		e.Version = v
		minV := MinOpenTofu
		if name == BinaryTerraform {
			minV = MinTerraform
		}
		if v.Less(minV) {
			return nil, fmt.Errorf("%s %s is older than the minimum supported %s", name, v, minV)
		}
		return e, nil
	}
	if preference == BinaryOpenTofu || preference == BinaryTerraform {
		return nil, fmt.Errorf("%s not found on PATH: %w", preference, ErrEngineNotFound)
	}
	return nil, ErrEngineNotFound
}

func (e *Engine) probeVersion(ctx context.Context) (Version, error) {
	var out bytes.Buffer
	if err := e.run(ctx, "", &out, io.Discard, "version", "-json"); err != nil {
		return Version{}, err
	}
	var v struct {
		TerraformVersion string `json:"terraform_version"`
	}
	if err := json.Unmarshal(out.Bytes(), &v); err == nil && v.TerraformVersion != "" {
		return ParseVersion(v.TerraformVersion)
	}
	return ParseVersion(out.String())
}

// IsOpenTofu reports whether the engine is OpenTofu.
func (e *Engine) IsOpenTofu() bool { return e.Name == BinaryOpenTofu }

// environment returns the environment for engine invocations: the process environment WITHOUT
// TF_CLI_ARGS and TF_CLI_ARGS_<command> (F-05: they inject arguments that Hermes neither sees
// nor records, for example TF_CLI_ARGS_plan=-target=... or TF_CLI_ARGS_init=-backend-config=...,
// silently changing what is planned and against which state), plus TF_IN_AUTOMATION=1,
// TF_INPUT=0 and e.Env. Everything else passes through unchanged: credentials, proxies and
// other TF_* variables (TF_LOG, TF_PLUGIN_CACHE_DIR, TF_WORKSPACE, TF_DATA_DIR; see
// docs/limitations.md).
func (e *Engine) environment() []string {
	var env, stripped []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "TF_CLI_ARGS" || strings.HasPrefix(name, "TF_CLI_ARGS_") {
			stripped = append(stripped, name)
			continue
		}
		env = append(env, kv)
	}
	if len(stripped) > 0 && !e.warnedCLIArgs {
		e.warnedCLIArgs = true
		w := e.Stderr
		if w == nil {
			w = os.Stderr
		}
		fmt.Fprintf(w, "warning: ignoring %s: hermes passes every engine argument explicitly (and records the plan it applies)\n", strings.Join(stripped, ", "))
	}
	env = append(env, "TF_IN_AUTOMATION=1", "TF_INPUT=0")
	return append(env, e.Env...)
}

func (e *Engine) run(ctx context.Context, dir string, stdout, stderr io.Writer, args ...string) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	runner := e.runner
	if runner == nil {
		runner = defaultRunner
	}
	for _, a := range args {
		if a == "-auto-approve" {
			// Defence in depth: Hermes only applies saved plans; it never auto-approves.
			return fmt.Errorf("refusing to pass -auto-approve to %s", e.Name)
		}
	}
	if err := runner(ctx, dir, e.environment(), stdout, stderr, e.Path, args...); err != nil {
		return fmt.Errorf("%s %s: %w", e.Name, strings.Join(args, " "), err)
	}
	return nil
}

// Run executes an arbitrary engine sub-command in dir, streaming output.
func (e *Engine) Run(ctx context.Context, dir string, args ...string) error {
	return e.run(ctx, dir, e.Stdout, e.Stderr, args...)
}

// Init runs `init -input=false` with extra args (e.g. -backend=false, -backend-config=...).
func (e *Engine) Init(ctx context.Context, dir string, args ...string) error {
	return e.Run(ctx, dir, append([]string{"init", "-input=false", "-no-color"}, args...)...)
}

// Validate runs `validate -no-color`.
func (e *Engine) Validate(ctx context.Context, dir string) error {
	return e.Run(ctx, dir, "validate", "-no-color")
}

// Plan writes a saved plan to planFile and reports whether it contains changes
// (using -detailed-exitcode semantics).
func (e *Engine) Plan(ctx context.Context, dir, planFile string, args ...string) (bool, error) {
	full := append([]string{"plan", "-input=false", "-no-color", "-detailed-exitcode", "-out=" + planFile}, args...)
	err := e.Run(ctx, dir, full...)
	if err == nil {
		return false, nil
	}
	if exitCode(err) == 2 {
		return true, nil
	}
	return false, err
}

// PlanRefreshOnly runs `plan -refresh-only -detailed-exitcode` (no plan file) and reports
// whether the real infrastructure drifted from the state.
func (e *Engine) PlanRefreshOnly(ctx context.Context, dir string) (bool, error) {
	err := e.Run(ctx, dir, "plan", "-refresh-only", "-input=false", "-no-color", "-detailed-exitcode")
	if err == nil {
		return false, nil
	}
	if exitCode(err) == 2 {
		return true, nil
	}
	return false, err
}

// exitCode returns the process exit code carried by err (via an ExitCode() method, as
// implemented by *exec.ExitError and ExitStatusError), or -1.
func exitCode(err error) int {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return -1
}

// ExitStatusError is an error carrying a process exit code; fake Runners return it to
// simulate engine exit statuses (e.g. 2 = changes present with -detailed-exitcode).
type ExitStatusError struct{ Code int }

func (e *ExitStatusError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitCode returns the exit code.
func (e *ExitStatusError) ExitCode() int { return e.Code }

// NewEngine returns an Engine with an explicit binary, version and Runner (nil = run the
// real binary). Tests in other packages use it with a fake Runner.
func NewEngine(name, path string, version Version, run Runner) *Engine {
	return &Engine{Name: name, Path: path, Version: version, runner: run}
}

// ShowJSON returns `show -json <planFile>` output (or the state when planFile is empty).
func (e *Engine) ShowJSON(ctx context.Context, dir, planFile string) ([]byte, error) {
	var out bytes.Buffer
	args := []string{"show", "-json", "-no-color"}
	if planFile != "" {
		args = append(args, planFile)
	}
	if err := e.run(ctx, dir, &out, e.Stderr, args...); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// OutputJSON returns `output -json`.
func (e *Engine) OutputJSON(ctx context.Context, dir string) ([]byte, error) {
	var out bytes.Buffer
	if err := e.run(ctx, dir, &out, e.Stderr, "output", "-json", "-no-color"); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ApplySaved applies a previously saved plan file. Applying a saved plan never prompts
// and never re-plans, so what is applied is exactly what was reviewed.
func (e *Engine) ApplySaved(ctx context.Context, dir, planFile string) error {
	if planFile == "" {
		return errors.New("apply requires a saved plan file")
	}
	return e.Run(ctx, dir, "apply", "-input=false", "-no-color", planFile)
}
