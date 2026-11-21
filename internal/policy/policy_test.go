package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
)

var bundle = fstest.MapFS{"rules.rego": {Data: []byte("package main\n")}}

func fixedNow() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }

func found(string) (string, error)    { return "/fake/conftest", nil }
func notFound(string) (string, error) { return "", errors.New("not found") }

func TestMissingConftest(t *testing.T) {
	for _, protected := range []bool{false, true} {
		rec, ds, err := Check(context.Background(), "plan.json", Options{Protected: protected, LookPath: notFound, Embedded: bundle})
		if err != nil || rec.Ran {
			t.Fatalf("err=%v rec=%+v", err, rec)
		}
		if ds.HasErrors() != protected {
			t.Errorf("protected=%v: errors=%v (%v)", protected, ds.HasErrors(), ds)
		}
	}
}

func TestNoBundledPolicies(t *testing.T) {
	_, ds, err := Check(context.Background(), "plan.json", Options{Protected: true, LookPath: found, Embedded: fstest.MapFS{}})
	if err != nil || !ds.HasErrors() || ds[0].Code != CodeNoPolicies {
		t.Fatalf("expected HCF302 error: %v %v", err, ds)
	}
}

func TestSkip(t *testing.T) {
	rec, ds, err := Check(context.Background(), "plan.json", Options{Protected: true, Skip: true})
	if err != nil || !rec.Skipped || ds.HasErrors() {
		t.Fatalf("skip: %v %+v %v", err, rec, ds)
	}
}

func TestConftestResults(t *testing.T) {
	dir := t.TempDir()
	planJSON := filepath.Join(dir, "plan.json")
	_ = os.WriteFile(planJSON, []byte("{}"), 0o600)
	var gotArgs []string
	run := func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotArgs = args
		out := `[{"filename":"plan.json","namespace":"main","successes":2,
		  "warnings":[{"msg":"HCF-POL-010: replacing a database"}],
		  "failures":[{"msg":"HCF-POL-001: public bucket"}]}]`
		return []byte(out), nil, &exitErr{}
	}
	rec, ds, err := Check(context.Background(), planJSON, Options{
		HermesDir: dir, Version: "t", LookPath: found, Run: run, Embedded: bundle,
		Data: map[string]any{"hermes": map[string]any{"protected_environments": []string{"production"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Ran || rec.Passed || len(rec.Failures) != 1 || len(rec.Warnings) != 1 {
		t.Errorf("record: %+v", rec)
	}
	if ds.Count(diag.SeverityError) != 1 || ds.Count(diag.SeverityWarning) != 1 {
		t.Errorf("diagnostics: %v", ds)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.HasPrefix(joined, "test --policy "+filepath.Join(dir, "policies", "t")+" --no-color -o json --data ") || !strings.HasSuffix(joined, planJSON) {
		t.Errorf("conftest args: %s", joined)
	}
	if _, err := os.Stat(filepath.Join(dir, "policies", "t", "rules.rego")); err != nil {
		t.Errorf("embedded policies must be extracted: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "policy-data.json")); !strings.Contains(string(data), "protected_environments") {
		t.Errorf("data file: %s", data)
	}

	pass := func(context.Context, string, ...string) ([]byte, []byte, error) {
		return []byte(`[{"filename":"plan.json","namespace":"main","successes":5}]`), nil, nil
	}
	rec, ds, err = Check(context.Background(), planJSON, Options{HermesDir: dir, Version: "t", Embedded: bundle, LookPath: found, Run: pass})
	if err != nil || !rec.Passed || len(ds) != 0 {
		t.Errorf("pass: %v %+v %v", err, rec, ds)
	}
	broken := func(context.Context, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("rego_parse_error: unexpected token"), &exitErr{}
	}
	if _, _, err := Check(context.Background(), planJSON, Options{HermesDir: dir, Version: "t", Embedded: bundle, LookPath: found, Run: broken}); err == nil || !strings.Contains(err.Error(), "rego_parse_error") {
		t.Errorf("broken policies must be an error: %v", err)
	}
}

type exitErr struct{}

func (*exitErr) Error() string { return "exit status 1" }

func TestEmbeddedBundleHasPolicies(t *testing.T) {
	if !hasRego(Embedded) {
		t.Fatal("the embedded bundle must contain .rego policies")
	}
}

// --policy-dir adds bundles; the embedded bundle always runs first, and every bundle's
// digest is recorded.
func TestPolicyDirIsAdditiveAndDigested(t *testing.T) {
	dir := t.TempDir()
	planJSON := filepath.Join(dir, "plan.json")
	_ = os.WriteFile(planJSON, []byte("{}"), 0o600)
	custom := filepath.Join(dir, "custom")
	_ = os.MkdirAll(custom, 0o750)
	_ = os.WriteFile(filepath.Join(custom, "team.rego"), []byte("package main\n"), 0o600)
	var calls [][]string
	pass := func(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, args)
		return []byte(`[{"filename":"plan.json","namespace":"main","successes":1}]`), nil, nil
	}
	check := func() (string, string) {
		t.Helper()
		rec, _, err := Check(context.Background(), planJSON, Options{HermesDir: dir, Version: "t", Embedded: bundle, PolicyDirs: []string{custom}, LookPath: found, Run: pass})
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.Bundles) != 2 || rec.Bundles[0].Source != "embedded" || rec.Bundles[1].Source != "policy-dir" || rec.Bundles[1].Path != custom {
			t.Fatalf("bundles: %+v", rec.Bundles)
		}
		for _, b := range rec.Bundles {
			if len(b.SHA256) != 64 {
				t.Fatalf("bundle digest: %+v", b)
			}
		}
		return rec.Bundles[0].SHA256, rec.Bundles[1].SHA256
	}
	emb, first := check()
	// F-13: the embedded bundle runs alone; custom bundles run in a second invocation.
	embeddedDir := filepath.Join(dir, "policies", "t")
	if len(calls) != 2 {
		t.Fatalf("want 2 conftest invocations (embedded alone, then custom), got %d: %v", len(calls), calls)
	}
	if got, want := strings.Join(calls[0], " "), "test --policy "+embeddedDir+" --no-color"; !strings.HasPrefix(got, want) || strings.Contains(got, custom) {
		t.Fatalf("first invocation must evaluate only the embedded bundle:\n got %s", got)
	}
	if got, want := strings.Join(calls[1], " "), "test --policy "+embeddedDir+" --policy "+custom+" --no-color"; !strings.HasPrefix(got, want) {
		t.Fatalf("second invocation must add the custom bundle:\n got %s\nwant %s...", got, want)
	}
	if d, _ := digestTree(bundle); d != emb {
		t.Errorf("embedded digest %s, want the digest of the embedded bundle %s", emb, d)
	}
	_ = os.WriteFile(filepath.Join(custom, ".hidden.rego"), []byte("package main\n"), 0o600)
	if _, second := check(); second == first {
		t.Error("adding a (hidden) policy file must change the bundle digest")
	}
	if err := os.Symlink(planJSON, filepath.Join(custom, "link.rego")); err == nil {
		if _, _, err := Check(context.Background(), planJSON, Options{HermesDir: dir, Version: "t", Embedded: bundle, PolicyDirs: []string{custom}, LookPath: found, Run: pass}); err == nil || !strings.Contains(err.Error(), "symbolic links") {
			t.Errorf("symlinks in a policy dir must be refused: %v", err)
		}
	}
	// A build without embedded policies cannot be "completed" by --policy-dir.
	_, ds, err := Check(context.Background(), planJSON, Options{HermesDir: dir, Protected: true, Embedded: fstest.MapFS{}, PolicyDirs: []string{custom}, LookPath: found, Run: pass})
	if err != nil || !ds.HasErrors() || ds[0].Code != CodeNoPolicies {
		t.Errorf("--policy-dir must not replace a missing embedded bundle: %v %v", err, ds)
	}
}

// --policy-data files are passed to conftest after the generated data, digested, and may
// not override the keys hermes derives from the spec.
func TestPolicyDataFiles(t *testing.T) {
	dir := t.TempDir()
	planJSON := filepath.Join(dir, "plan.json")
	_ = os.WriteFile(planJSON, []byte("{}"), 0o600)
	exceptions := filepath.Join(dir, "exceptions.yaml")
	content := []byte("hermes:\n  exceptions:\n    - {rule: HCF-POL-009, address: module.platform.x, reason: \"ticket EX-1\", expires: \"2027-01-31\"}\n")
	_ = os.WriteFile(exceptions, content, 0o600)
	var gotArgs []string
	calls := 0
	pass := func(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
		calls++
		gotArgs = args
		return []byte(`[{"filename":"plan.json","namespace":"main","successes":1}]`), nil, nil
	}
	opts := Options{HermesDir: dir, Version: "t", Embedded: bundle, LookPath: found, Run: pass, Now: fixedNow,
		Data: map[string]any{"hermes": map[string]any{"protected": true}}, DataFiles: []string{exceptions}}
	rec, _, err := Check(context.Background(), planJSON, opts)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--data "+filepath.Join(dir, "policy-data.json")+" --data "+exceptions+" "+planJSON) {
		t.Errorf("data files must follow the generated data: %s", joined)
	}
	sum := sha256.Sum256(content)
	if len(rec.DataFiles) != 1 || rec.DataFiles[0].SHA256 != hex.EncodeToString(sum[:]) || rec.DataFiles[0].Source != "policy-data" {
		t.Errorf("data file digest: %+v", rec.DataFiles)
	}
	if src := rec.Sources(); len(src) != 2 || src[1].Path != exceptions {
		t.Errorf("sources for history: %+v", src)
	}
	calls = 0
	override := filepath.Join(dir, "override.json")
	_ = os.WriteFile(override, []byte(`{"hermes": {"protected": false, "exceptions": []}}`), 0o600)
	for name, f := range map[string]string{"override": override, "missing": filepath.Join(dir, "nope.yaml"), "extension": planJSON + ".txt"} {
		if name == "extension" {
			_ = os.WriteFile(f, []byte("{}"), 0o600)
		}
		o := opts
		o.DataFiles = []string{f}
		if _, _, err := Check(context.Background(), planJSON, o); err == nil {
			t.Errorf("%s: want an error", name)
		} else if name == "override" && !strings.Contains(err.Error(), "hermes.protected") {
			t.Errorf("override error must name the key: %v", err)
		}
	}
	if calls != 0 {
		t.Errorf("conftest must not run with an invalid data file (%d calls)", calls)
	}
}

// F-13 reproduction (fake conftest): a custom bundle that neutralises the embedded denies (QA:
// `package main` + `excepted(_, _) if true`) only "works" when it is evaluated in the same
// conftest invocation. The fake reports the embedded failure unless the custom directory is
// part of the same invocation, exactly like real conftest did. The failure must survive.
func TestCustomBundleCannotNeutraliseEmbeddedDenies(t *testing.T) {
	dir := t.TempDir()
	planJSON := filepath.Join(dir, "plan.json")
	_ = os.WriteFile(planJSON, []byte("{}"), 0o600)
	evil := filepath.Join(dir, "evilpol")
	_ = os.MkdirAll(evil, 0o750)
	_ = os.WriteFile(filepath.Join(evil, "extra.rego"), []byte("package main\n\nexcepted(_, _) if true\n"), 0o600)
	run := func(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
		if strings.Contains(strings.Join(args, " "), evil) {
			return []byte(`[{"filename":"plan.json","namespace":"main","successes":70}]`), nil, nil
		}
		return []byte(`[{"filename":"plan.json","namespace":"main","successes":1,"failures":[{"msg":"HCF-POL-002: module.platform.x: unencrypted"}]}]`), nil, &exitErr{}
	}
	rec, ds, err := Check(context.Background(), planJSON, Options{HermesDir: dir, Version: "t", Embedded: bundle, PolicyDirs: []string{evil}, LookPath: found, Run: run})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Passed || len(rec.Failures) != 1 || ds.Count(diag.SeverityError) != 1 {
		t.Fatalf("a custom bundle must not remove embedded failures: %+v %v", rec, ds)
	}
}

// Custom bundles add findings; findings reported by both invocations are not duplicated.
func TestCustomBundleFindingsAreAddedOnce(t *testing.T) {
	dir := t.TempDir()
	planJSON := filepath.Join(dir, "plan.json")
	_ = os.WriteFile(planJSON, []byte("{}"), 0o600)
	team := filepath.Join(dir, "team")
	_ = os.MkdirAll(team, 0o750)
	_ = os.WriteFile(filepath.Join(team, "team.rego"), []byte("package main\n"), 0o600)
	run := func(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
		out := `[{"filename":"plan.json","namespace":"main","warnings":[{"msg":"HCF-POL-008: w"}],"failures":[{"msg":"HCF-POL-001: a"}]}]`
		if strings.Contains(strings.Join(args, " "), team) {
			out = `[{"filename":"plan.json","namespace":"main","warnings":[{"msg":"HCF-POL-008: w"}],"failures":[{"msg":"HCF-POL-001: a"},{"msg":"TEAM-1: b"}]}]`
		}
		return []byte(out), nil, &exitErr{}
	}
	rec, _, err := Check(context.Background(), planJSON, Options{HermesDir: dir, Version: "t", Embedded: bundle, PolicyDirs: []string{team}, LookPath: found, Run: run})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rec.Failures, ",") != "HCF-POL-001: a,TEAM-1: b" || len(rec.Warnings) != 1 {
		t.Fatalf("failures %v warnings %v", rec.Failures, rec.Warnings)
	}
}

// --policy-data exceptions: one exact address, a reason and an expiry of at most 90 days.
// Applied exceptions are recorded (and whether their address is in the plan).
func TestPolicyExceptions(t *testing.T) {
	dir := t.TempDir()
	planJSON := filepath.Join(dir, "plan.json")
	_ = os.WriteFile(planJSON, []byte(`{"resource_changes":[{"address":"module.platform.module.secrets.aws_secretsmanager_secret_version.x"}]}`), 0o600)
	calls := 0
	pass := func(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
		calls++
		return []byte(`[{"filename":"plan.json","namespace":"main","successes":1}]`), nil, nil
	}
	write := func(name, body string) string {
		f := filepath.Join(dir, name)
		_ = os.WriteFile(f, []byte("hermes:\n  exceptions:\n"+body), 0o600)
		return f
	}
	check := func(f string) (execution.PolicyRecord, diag.Diagnostics, error) {
		return Check(context.Background(), planJSON, Options{HermesDir: dir, Version: "t", Embedded: bundle, LookPath: found, Run: pass, Now: fixedNow, DataFiles: []string{f}})
	}
	for name, body := range map[string]string{
		"star address":    `    - {rule: HCF-POL-002, address: "*", reason: r, expires: "2026-12-01"}` + "\n",
		"prefix wildcard": `    - {rule: HCF-POL-002, address: "module.platform.*", reason: r, expires: "2026-12-01"}` + "\n",
		"rule wildcard":   `    - {rule: "HCF-POL-*", address: module.platform.x, reason: r, expires: "2026-12-01"}` + "\n",
		"too far ahead":   `    - {rule: HCF-POL-002, address: module.platform.x, reason: r, expires: "2261-01-01"}` + "\n",
		"91 days":         `    - {rule: HCF-POL-002, address: module.platform.x, reason: r, expires: "2027-02-20"}` + "\n",
		"no reason":       `    - {rule: HCF-POL-002, address: module.platform.x, reason: " ", expires: "2026-12-01"}` + "\n",
		"bad date":        `    - {rule: HCF-POL-002, address: module.platform.x, reason: r, expires: "next week"}` + "\n",
		"not an object":   `    - HCF-POL-002` + "\n",
	} {
		calls = 0
		if _, _, err := check(write("bad.yaml", body)); err == nil || calls != 0 {
			t.Errorf("%s: want an error before conftest runs (err=%v, calls=%d)", name, err, calls)
		}
	}
	f := write("ok.yaml", `    - {rule: HCF-POL-009, address: module.platform.module.secrets.aws_secretsmanager_secret_version.x, reason: "ticket EX-1", expires: "2027-02-17"}
    - {rule: HCF-POL-002, address: module.platform.y, reason: "ticket EX-2", expires: "2026-12-01"}
    - {rule: HCF-POL-003, address: module.platform.z, reason: "old", expires: "2026-11-01"}
`)
	rec, ds, err := check(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Exceptions) != 2 || !rec.Exceptions[0].InPlan || rec.Exceptions[1].InPlan || rec.Exceptions[0].Reason != "ticket EX-1" || rec.Exceptions[0].Expires != "2027-02-17" || rec.Exceptions[0].File != f {
		t.Errorf("exceptions in effect: %+v", rec.Exceptions)
	}
	if ds.Count(diag.SeverityWarning) != 1 || ds[0].Code != CodePolicyException || !strings.Contains(ds[0].Message, "expired") {
		t.Errorf("an expired exception must warn: %v", ds)
	}
}

// F-13 reproduction with the real conftest (skipped when it is not installed): the QA bundle
// that neutralised every embedded deny now changes nothing.
func TestRealConftestCustomBundleCannotNeutralise(t *testing.T) {
	if _, err := exec.LookPath("conftest"); err != nil {
		t.Skip("conftest not on PATH")
	}
	dir := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("..", "..", "policies", "tests", "fixtures", "aws_violations", "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	planJSON := filepath.Join(dir, "plan.json")
	_ = os.WriteFile(planJSON, fixture, 0o600)
	evil := filepath.Join(dir, "evilpol")
	_ = os.MkdirAll(evil, 0o750)
	_ = os.WriteFile(filepath.Join(evil, "extra.rego"), []byte("package main\n\nexcepted(_, _) if true\n"), 0o600)
	check := func(dirs []string) execution.PolicyRecord {
		t.Helper()
		rec, _, err := Check(context.Background(), planJSON, Options{HermesDir: dir, Version: "t", PolicyDirs: dirs,
			Data: map[string]any{"hermes": map[string]any{"protected": true}}})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	embedded := check(nil)
	if embedded.Passed || len(embedded.Failures) < 30 {
		t.Fatalf("the violations fixture must fail the embedded policies: %d failures", len(embedded.Failures))
	}
	withEvil := check([]string{evil})
	if withEvil.Passed || strings.Join(withEvil.Failures, "\n") != strings.Join(embedded.Failures, "\n") {
		t.Fatalf("a custom bundle changed the embedded findings: %d -> %d failures", len(embedded.Failures), len(withEvil.Failures))
	}
}

// F-01: --policy-data YAML files reject anchors/aliases (and files over 1 MiB) before conftest runs.
func TestPolicyDataRejectsAliases(t *testing.T) {
	dir := t.TempDir()
	planJSON := filepath.Join(dir, "plan.json")
	_ = os.WriteFile(planJSON, []byte("{}"), 0o600)
	f := filepath.Join(dir, "data.yaml")
	_ = os.WriteFile(f, []byte("hermes:\n  secret_placeholders: &p [a, b]\n  other: *p\n"), 0o600)
	calls := 0
	run := func(context.Context, string, ...string) ([]byte, []byte, error) {
		calls++
		return []byte("[]"), nil, nil
	}
	_, _, err := Check(context.Background(), planJSON, Options{HermesDir: dir, Version: "t", Embedded: bundle, LookPath: found, Run: run, DataFiles: []string{f}})
	if err == nil || !strings.Contains(err.Error(), "anchors and aliases") || calls != 0 {
		t.Errorf("want an anchors/aliases error before conftest runs: %v (calls=%d)", err, calls)
	}
}
