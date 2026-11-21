// Package policy runs plan policies (Conftest/OPA Rego, package main) against the JSON
// plan produced by `tofu show -json`. Conftest is optional: when it is missing the check
// is a warning in non-protected environments and an error in protected environments
// unless explicitly skipped (and the skip is recorded in history).
//
// The embedded bundle always runs, in its own conftest invocation: --policy-dir bundles run in a
// second invocation, so they can add findings but can never remove or neutralise an embedded
// one (F-13). The sha256 of every bundle and --policy-data file, and every policy exception in
// effect, are recorded with the plan.
package policy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/render"
	"github.com/nityam02/hermes-cloud-foundation/internal/yamlsafe"
)

// Diagnostic codes.
const (
	CodeConftestMissing = "HCF301"
	CodeNoPolicies      = "HCF302"
	CodePolicyDeny      = "HCF303"
	CodePolicyWarn      = "HCF304"
	CodePolicySkipped   = "HCF305"
	CodePolicyException = "HCF306"
)

// MaxExceptionLifetime caps how far in the future a policy exception may expire.
const MaxExceptionLifetime = 90 * 24 * time.Hour

// Runner runs a command and returns stdout, stderr and the error (an exit error when the
// command exits non-zero). Replaced in tests.
type Runner func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)

func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// Options configure a policy check.
type Options struct {
	// PolicyDirs are additional policy directories (--policy-dir, repeatable). They are
	// evaluated in a separate conftest invocation (together with a copy of the embedded bundle,
	// so they can use its helpers); their findings are added to those of the embedded bundle,
	// which always runs on its own.
	PolicyDirs []string
	// DataFiles are extra conftest data files (--policy-data, repeatable; JSON or YAML), for
	// example the time-boxed exceptions under hermes.exceptions. They may not set the keys
	// Hermes generates from the spec (see GeneratedDataKeys).
	DataFiles []string
	HermesDir string // embedded policies are extracted to <HermesDir>/policies/<Version>
	Version   string
	Protected bool
	Skip      bool // --skip-policy
	// Data is written to a JSON data file passed with --data (data.hermes.* in Rego).
	Data map[string]any

	Embedded fs.FS                        // nil = Embedded
	LookPath func(string) (string, error) // nil = exec.LookPath
	Run      Runner                       // nil = real process
	Now      func() time.Time             // nil = time.Now (exception expiry checks)
}

// Check evaluates the policies against planJSON. It returns the record stored with the
// saved plan, and diagnostics. Error diagnostics mean the plan must not be saved as
// applicable (missing tooling in a protected environment); policy denials are recorded
// in the PolicyRecord (Passed=false) so the plan can be inspected but never applied.
func Check(ctx context.Context, planJSON string, o Options) (execution.PolicyRecord, diag.Diagnostics, error) {
	if o.Skip {
		sev := diag.Warnf(CodePolicySkipped, "", "policy check skipped (--skip-policy); recorded in history")
		return execution.PolicyRecord{Skipped: true, Note: "skipped with --skip-policy"}, diag.Diagnostics{sev}, nil
	}
	missing := func(code, msg, hint string) (execution.PolicyRecord, diag.Diagnostics, error) {
		d := diag.Warnf(code, "", "%s", msg).WithHint("%s", hint)
		if o.Protected {
			d = diag.Errorf(code, "", "%s; protected environments require a policy check", msg).WithHint("%s, or pass --skip-policy (recorded)", hint)
		}
		return execution.PolicyRecord{Note: msg}, diag.Diagnostics{d}, nil
	}

	lookPath := o.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	conftest, err := lookPath("conftest")
	if err != nil {
		return missing(CodeConftestMissing, "conftest is not installed, so plan policies were not evaluated", "install conftest (https://www.conftest.dev)")
	}

	// The embedded bundle always runs: custom directories are added to it, never replace it.
	src := o.Embedded
	if src == nil {
		src = Embedded
	}
	if !hasRego(src) {
		return missing(CodeNoPolicies, "this hermes build bundles no plan policies", "use a release build of hermes (--policy-dir adds policies to the bundle; it cannot replace it)")
	}
	version := o.Version
	if version == "" {
		version = "dev"
	}
	embeddedDir := filepath.Join(o.HermesDir, "policies", version)
	if _, err := render.ExtractModules(src, embeddedDir, version, false); err != nil {
		return execution.PolicyRecord{}, nil, fmt.Errorf("policy: extract embedded policies: %w", err)
	}
	embeddedDigest, err := digestTree(src)
	if err != nil {
		return execution.PolicyRecord{}, nil, fmt.Errorf("policy: digest embedded policies: %w", err)
	}
	bundles := []execution.PolicySource{{Source: "embedded", Path: embeddedDir, SHA256: embeddedDigest}}
	for _, dir := range o.PolicyDirs {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return execution.PolicyRecord{}, nil, fmt.Errorf("policy: --policy-dir %s is not a directory", dir)
		}
		d, err := digestTree(os.DirFS(dir))
		if err != nil {
			return execution.PolicyRecord{}, nil, fmt.Errorf("policy: digest --policy-dir %s: %w", dir, err)
		}
		bundles = append(bundles, execution.PolicySource{Source: "policy-dir", Path: dir, SHA256: d})
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	var (
		dataFiles  []execution.PolicySource
		exceptions []execution.PolicyException
		ds         diag.Diagnostics
	)
	for _, f := range o.DataFiles {
		d, excs, eds, err := checkDataFile(f, now())
		if err != nil {
			return execution.PolicyRecord{}, nil, err
		}
		dataFiles = append(dataFiles, execution.PolicySource{Source: "policy-data", Path: f, SHA256: d})
		exceptions = append(exceptions, excs...)
		ds = append(ds, eds...)
	}
	if len(exceptions) > 0 {
		addrs, err := planAddresses(planJSON)
		if err != nil {
			return execution.PolicyRecord{}, nil, err
		}
		for i := range exceptions {
			exceptions[i].InPlan = addrs[exceptions[i].Address]
		}
	}

	var dataArgs []string
	if o.Data != nil {
		dataFile := filepath.Join(filepath.Dir(planJSON), "policy-data.json")
		data, _ := json.MarshalIndent(o.Data, "", "  ")
		if err := os.WriteFile(dataFile, data, 0o600); err != nil {
			return execution.PolicyRecord{}, nil, fmt.Errorf("policy: %w", err)
		}
		dataArgs = append(dataArgs, "--data", dataFile)
	}
	for _, f := range dataFiles {
		dataArgs = append(dataArgs, "--data", f.Path)
	}
	run := o.Run
	if run == nil {
		run = defaultRunner
	}

	// 1. The embedded bundle on its own: nothing a custom bundle declares can change its rules.
	results, err := runConftest(ctx, run, conftest, []string{embeddedDir}, dataArgs, planJSON)
	if err != nil {
		return execution.PolicyRecord{}, nil, err
	}
	// 2. Custom bundles in a separate invocation, with a copy of the embedded bundle so they can
	// use its helpers. Their findings are added; a custom file that redefines an embedded rule
	// (for example `excepted(_, _) if true`) only affects this second run, whose embedded
	// findings are already covered by run 1.
	if len(bundles) > 1 {
		dirs := []string{embeddedDir}
		for _, b := range bundles[1:] {
			dirs = append(dirs, b.Path)
		}
		custom, err := runConftest(ctx, run, conftest, dirs, dataArgs, planJSON)
		if err != nil {
			return execution.PolicyRecord{}, nil, err
		}
		results = append(results, custom...)
	}

	rec := execution.PolicyRecord{Ran: true, Passed: true, Tool: "conftest", Bundles: bundles, DataFiles: dataFiles, Exceptions: exceptions}
	seenFail, seenWarn := map[string]bool{}, map[string]bool{}
	for _, r := range results {
		for _, f := range r.Failures {
			if seenFail[f.Msg] {
				continue
			}
			seenFail[f.Msg] = true
			rec.Failures = append(rec.Failures, f.Msg)
			ds = append(ds, diag.Errorf(CodePolicyDeny, "", "policy: %s", f.Msg))
		}
		for _, w := range r.Warnings {
			if seenWarn[w.Msg] {
				continue
			}
			seenWarn[w.Msg] = true
			rec.Warnings = append(rec.Warnings, w.Msg)
			ds = append(ds, diag.Warnf(CodePolicyWarn, "", "policy: %s", w.Msg))
		}
	}
	rec.Passed = len(rec.Failures) == 0
	return rec, ds, nil
}

// runConftest runs one `conftest test` invocation and parses its JSON results. A non-zero exit
// with parseable results is a normal policy failure; anything else is an error.
func runConftest(ctx context.Context, run Runner, conftest string, policyDirs, dataArgs []string, planJSON string) ([]result, error) {
	args := []string{"test"}
	for _, d := range policyDirs {
		args = append(args, "--policy", d)
	}
	args = append(args, "--no-color", "-o", "json")
	args = append(args, dataArgs...)
	args = append(args, planJSON)
	stdout, stderr, runErr := run(ctx, conftest, args...)
	results, perr := parseResults(stdout)
	if perr != nil {
		msg := strings.TrimSpace(string(stderr))
		if runErr != nil && msg != "" {
			return nil, fmt.Errorf("policy: conftest failed: %s", msg)
		}
		return nil, fmt.Errorf("policy: conftest output: %w", perr)
	}
	failed := false
	for _, r := range results {
		failed = failed || len(r.Failures) > 0
	}
	var ee *exec.ExitError
	if runErr != nil && !failed && !errors.As(runErr, &ee) {
		return nil, fmt.Errorf("policy: conftest: %w", runErr)
	}
	return results, nil
}

// planAddresses returns the resource addresses of a plan JSON file.
func planAddresses(planJSON string) (map[string]bool, error) {
	data, err := os.ReadFile(planJSON)
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	var plan struct {
		ResourceChanges []struct {
			Address string `json:"address"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("policy: read plan JSON: %w", err)
	}
	out := map[string]bool{}
	for _, rc := range plan.ResourceChanges {
		out[rc.Address] = true
	}
	return out, nil
}

// GeneratedDataKeys are the data.hermes keys Hermes writes from the spec. A --policy-data
// file may not set them (conftest would also refuse the conflicting merge), so a data file
// can add exceptions or placeholders but cannot, for example, mark a protected plan as
// unprotected.
var GeneratedDataKeys = []string{"project", "environment", "provider", "protected", "protected_environments"}

// checkDataFile validates a --policy-data file and returns its sha256 and the exceptions in
// effect. Exceptions must name one exact resource address (no wildcards), a rule, a reason and
// an expiry date at most MaxExceptionLifetime from now; anything else is an error. Expired
// exceptions are ignored by the policies and reported as warnings.
func checkDataFile(path string, now time.Time) (string, []execution.PolicyException, diag.Diagnostics, error) {
	data, err := yamlsafe.ReadFile(path, yamlsafe.MaxBytes)
	if err != nil {
		return "", nil, nil, fmt.Errorf("policy: --policy-data: %w", err)
	}
	var doc map[string]any
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		err = json.Unmarshal(data, &doc)
	case ".yaml", ".yml":
		err = yamlsafe.Unmarshal(data, yamlsafe.MaxBytes, &doc)
	default:
		return "", nil, nil, fmt.Errorf("policy: --policy-data %s: use a .json, .yaml or .yml file", path)
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("policy: --policy-data %s: must be a JSON/YAML object: %w", path, err)
	}
	var (
		excs []execution.PolicyException
		ds   diag.Diagnostics
	)
	if h, ok := doc["hermes"].(map[string]any); ok {
		var set []string
		for _, k := range GeneratedDataKeys {
			if _, ok := h[k]; ok {
				set = append(set, "hermes."+k)
			}
		}
		if len(set) > 0 {
			return "", nil, nil, fmt.Errorf("policy: --policy-data %s sets %s, which hermes derives from the spec; remove them (data files may add hermes.exceptions and hermes.secret_placeholders)", path, strings.Join(set, ", "))
		}
		if raw, present := h["exceptions"]; present {
			excs, ds, err = parseExceptions(path, raw, now)
			if err != nil {
				return "", nil, nil, err
			}
		}
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), excs, ds, nil
}

// parseExceptions validates hermes.exceptions (see checkDataFile).
func parseExceptions(path string, raw any, now time.Time) ([]execution.PolicyException, diag.Diagnostics, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("policy: --policy-data %s: hermes.exceptions must be a list", path)
	}
	var (
		out []execution.PolicyException
		ds  diag.Diagnostics
	)
	limit := now.Add(MaxExceptionLifetime)
	for i, x := range list {
		m, _ := x.(map[string]any)
		str := func(k string) string { v, _ := m[k].(string); return strings.TrimSpace(v) }
		e := execution.PolicyException{Rule: str("rule"), Address: str("address"), Reason: str("reason"), Expires: str("expires"), File: path}
		bad := func(format string, a ...any) error {
			return fmt.Errorf("policy: --policy-data %s: hermes.exceptions[%d]: %s", path, i, fmt.Sprintf(format, a...))
		}
		switch {
		case m == nil:
			return nil, nil, bad("must be an object with rule, address, reason and expires")
		case e.Rule == "" || e.Address == "" || e.Reason == "" || e.Expires == "":
			return nil, nil, bad("rule, address, reason and expires are all required")
		case strings.ContainsAny(e.Rule+e.Address, "*?[]"):
			return nil, nil, bad("wildcards are not allowed; name one rule and one exact resource address (got rule %q, address %q)", e.Rule, e.Address)
		}
		exp, err := time.Parse("2006-01-02", e.Expires)
		if err != nil {
			return nil, nil, bad("expires %q is not a date (YYYY-MM-DD)", e.Expires)
		}
		if exp.After(limit) {
			return nil, nil, bad("expires %s is more than %d days from now (latest allowed: %s); exceptions are time-boxed", e.Expires, int(MaxExceptionLifetime.Hours()/24), limit.UTC().Format("2006-01-02"))
		}
		if !exp.After(now) {
			ds = append(ds, diag.Warnf(CodePolicyException, "", "policy exception for %s at %s expired on %s and is ignored (%s)", e.Rule, e.Address, e.Expires, path))
			continue
		}
		out = append(out, e)
	}
	return out, ds, nil
}

// digestTree is the sha256 over every regular file of a policy tree (relative path, size,
// content), hidden files included, since conftest may load them. Symbolic links are refused
// so the digest covers what conftest reads.
func digestTree(fsys fs.FS) (string, error) {
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s: symbolic links are not allowed in policy directories", p)
		case d.Type().IsRegular():
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type result struct {
	Filename  string    `json:"filename"`
	Namespace string    `json:"namespace"`
	Successes int       `json:"successes"`
	Warnings  []message `json:"warnings"`
	Failures  []message `json:"failures"`
}

type message struct {
	Msg string `json:"msg"`
}

func parseResults(stdout []byte) ([]result, error) {
	var rs []result
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

func hasRego(fsys fs.FS) bool {
	found := false
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".rego") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}
