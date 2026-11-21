package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution/enginetest"
)

var testNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

type harness struct {
	t      *testing.T
	dir    string
	spec   string
	fake   *enginetest.Fake
	env    map[string]string
	tools  map[string]bool // tools found on PATH
	toolFn func(name string, args ...string) ([]byte, []byte, error)
	noEng  bool
	now    time.Time
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newHarness(t *testing.T, example string) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), env: map[string]string{}, tools: map[string]bool{}, now: testNow,
		fake: &enginetest.Fake{PlanJSON: enginetest.PlanJSON(map[string][]string{"module.platform.x.a": {"create"}})}}
	if example != "" {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", example, "hermes.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		h.writeSpec(string(data))
	}
	return h
}

func (h *harness) writeSpec(s string) {
	h.spec = filepath.Join(h.dir, "hermes.yaml")
	if err := os.WriteFile(h.spec, []byte(s), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) editSpec(old, new string) {
	data, _ := os.ReadFile(h.spec)
	if !strings.Contains(string(data), old) {
		h.t.Fatalf("spec does not contain %q", old)
	}
	h.writeSpec(strings.Replace(string(data), old, new, 1))
}

func (h *harness) run(args ...string) int {
	h.t.Helper()
	h.stdout.Reset()
	h.stderr.Reset()
	app := &App{
		Build: BuildInfo{Version: "test", Commit: "c", Date: "d"}, WorkDir: h.dir,
		Stdin: strings.NewReader(""), Stdout: &h.stdout, Stderr: &h.stderr,
		DetectEngine: func(context.Context, string) (*execution.Engine, error) {
			if h.noEng {
				return nil, execution.ErrEngineNotFound
			}
			return h.fake.Engine("tofu"), nil
		},
		Getenv: func(k string) string { return h.env[k] },
		LookPath: func(n string) (string, error) {
			if h.tools[n] {
				return "/fake/" + n, nil
			}
			return "", errors.New("not found")
		},
		Now:         func() time.Time { return h.now },
		UserName:    func() string { return "tester" },
		Interactive: func() bool { return false },
		RunTool: func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
			if h.toolFn == nil {
				return nil, nil, errors.New("no tool expected")
			}
			return h.toolFn(name, args...)
		},
	}
	return Run(app, args)
}

func (h *harness) must(code int, args ...string) {
	h.t.Helper()
	if got := h.run(args...); got != code {
		h.t.Fatalf("hermes %v: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", args, got, code, h.stdout.String(), h.stderr.String())
	}
}

func (h *harness) meta(env, kind string) execution.PlanMeta {
	h.t.Helper()
	sp, err := execution.LoadPlan(execution.Workspace{Dir: filepath.Join(h.dir, ".hermes")}.PlanDir(env, kind))
	if err != nil {
		h.t.Fatal(err)
	}
	return sp.Meta
}

func (h *harness) history(env string) []execution.HistoryEvent {
	evs, err := execution.ReadHistory(filepath.Join(h.dir, ".hermes", "history", env+".jsonl"))
	if err != nil {
		h.t.Fatal(err)
	}
	return evs
}

func TestValidateExamples(t *testing.T) {
	for _, ex := range []string{"basic", "containerized", "database", "multi-cloud", "autonomous-logistics"} {
		h := newHarness(t, ex)
		h.must(ExitOK, "validate")
		h.must(ExitOK, "validate", "-o", "json", "--portability")
		var out struct {
			Valid        bool `json:"valid"`
			Environments []struct {
				Environment string `json:"environment"`
				Portability *struct {
					Providers []any `json:"providers"`
				} `json:"portability"`
			} `json:"environments"`
		}
		if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil || !out.Valid || len(out.Environments) == 0 {
			t.Fatalf("%s: %v %s", ex, err, h.stdout.String())
		}
		if out.Environments[0].Portability == nil || len(out.Environments[0].Portability.Providers) != 3 {
			t.Errorf("%s: --portability must score all providers", ex)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	h := newHarness(t, "containerized")
	h.editSpec("type: containerized", "type: kubernetes")
	h.must(ExitRefused, "validate")
	if !strings.Contains(h.stdout.String(), "HCF111") || !strings.Contains(h.stdout.String(), "hermes.yaml:") {
		t.Errorf("expected HCF111 with a line number:\n%s", h.stdout.String())
	}
	h.must(ExitRefused, "validate", "-o", "yaml")
}

func TestRenderAndPrint(t *testing.T) {
	h := newHarness(t, "containerized")
	h.must(ExitOK, "render", "--env", "production")
	root := filepath.Join(h.dir, ".hermes", "build", "production", "main.tf.json")
	data, err := os.ReadFile(root)
	if err != nil || !strings.Contains(string(data), `"version": "6.73.0"`) {
		t.Fatalf("rendered root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".hermes", "modules", "test", "contract", "variables.tf")); err != nil {
		t.Errorf("embedded modules must be extracted: %v", err)
	}
	h.must(ExitOK, "render", "--env", "production", "--print")
	if !strings.Contains(h.stdout.String(), "customer_managed_keys: true") || !strings.Contains(h.stdout.String(), "protected: true") {
		t.Errorf("normalized spec must show protected defaults:\n%s", h.stdout.String())
	}
}

func TestPlanApplyFlow(t *testing.T) {
	h := newHarness(t, "containerized")
	h.must(ExitOK, "plan")
	m := h.meta("dev", execution.KindApply)
	if !m.HasChanges || m.Summary.Create != 1 || m.User != "tester" || m.Policy.Ran {
		t.Errorf("meta: %+v", m)
	}
	if !strings.Contains(h.stderr.String(), "HCF301") {
		t.Errorf("missing conftest must warn in non-protected envs:\n%s", h.stderr.String())
	}
	// Non-interactive without --yes: refused and recorded.
	h.must(ExitRefused, "apply")
	if h.fake.Called("apply -input=false") {
		t.Fatal("apply must not run without confirmation")
	}
	h.must(ExitOK, "apply", "--yes")
	if !h.fake.Called("apply -input=false -no-color " + filepath.Join(h.dir, ".hermes", "plans", "dev", "plan.tfplan")) {
		t.Errorf("must apply exactly the saved plan file: %v", h.fake.Calls)
	}
	evs := h.history("dev")
	got := []string{}
	for _, e := range evs {
		got = append(got, e.Action+":"+e.Result)
	}
	if strings.Join(got, ",") != "plan:ok,apply:refused,apply:ok" {
		t.Errorf("history = %v", got)
	}
	h.must(ExitRefused, "apply", "--yes") // a plan applies only once
	for _, c := range h.fake.Calls {
		if strings.Contains(c, "-auto-approve") {
			t.Fatalf("-auto-approve passed: %s", c)
		}
	}
}

func TestStalePlanRefused(t *testing.T) {
	h := newHarness(t, "containerized")
	h.must(ExitOK, "plan")
	h.editSpec("LOG_LEVEL: info", "LOG_LEVEL: debug")
	h.must(ExitRefused, "apply", "--yes")
	if !strings.Contains(h.stderr.String(), execution.CodeStalePlan) {
		t.Errorf("expected stale-plan refusal:\n%s", h.stderr.String())
	}
}

func TestExpiredPlanRefused(t *testing.T) {
	h := newHarness(t, "containerized")
	h.must(ExitOK, "plan")
	h.now = testNow.Add(2 * time.Hour)
	h.must(ExitRefused, "apply", "--yes", "--max-plan-age", "1h")
	if !strings.Contains(h.stderr.String(), execution.CodeExpiredPlan) {
		t.Errorf("expected expiry refusal: %s", h.stderr.String())
	}
	h.now = testNow.Add(25 * time.Hour)
	h.must(ExitRefused, "apply", "--yes") // default maximum 24h
}

func TestProtectedEnvironment(t *testing.T) {
	h := newHarness(t, "containerized")
	// Missing conftest is an error in protected environments.
	h.must(ExitRefused, "plan", "--env", "production")
	h.must(ExitOK, "plan", "--env", "production", "--skip-policy")
	m := h.meta("production", execution.KindApply)
	if !m.Protected || !m.Policy.Skipped {
		t.Fatalf("meta: %+v", m)
	}
	h.must(ExitRefused, "apply", "--env", "production", "--yes")
	h.must(ExitRefused, "apply", "--env", "production", "--approve", m.PlanID[:7])
	h.must(ExitRefused, "apply", "--env", "production", "--approve", "0000000000")
	h.must(ExitOK, "apply", "--env", "production", "--approve", m.PlanID[:8])
	evs := h.history("production")
	last := evs[len(evs)-1]
	if last.Action != "apply" || last.Result != "ok" || !last.SkipPolicy {
		t.Errorf("history must record the skipped policy: %+v", last)
	}
}

func TestDestructiveNeedsFlag(t *testing.T) {
	h := newHarness(t, "containerized")
	h.fake.PlanJSON = enginetest.PlanJSON(map[string][]string{"module.platform.aws_db_instance.db": {"delete", "create"}})
	h.must(ExitOK, "plan")
	if !strings.Contains(h.stdout.String(), "--allow-destructive") {
		t.Errorf("plan output must say --allow-destructive is needed:\n%s", h.stdout.String())
	}
	h.must(ExitRefused, "apply", "--yes")
	h.must(ExitOK, "apply", "--yes", "--allow-destructive")
}

func TestDetailedExitCode(t *testing.T) {
	h := newHarness(t, "containerized")
	h.must(ExitChanges, "plan", "--detailed-exitcode")
	h.fake.PlanJSON = `{"format_version":"1.2","resource_changes":[]}`
	h.must(ExitOK, "plan", "--detailed-exitcode")
}

func TestPolicyFailureBlocksApply(t *testing.T) {
	h := newHarness(t, "containerized")
	h.tools["conftest"] = true
	policyDir := filepath.Join(h.dir, "policies-under-test")
	_ = os.MkdirAll(policyDir, 0o750)
	exceptions := filepath.Join(h.dir, "policy-exceptions.yaml")
	_ = os.WriteFile(exceptions, []byte("hermes:\n  exceptions:\n    - {rule: HCF-POL-009, address: module.platform.module.secrets.x, reason: \"ticket EX-7\", expires: \"2026-12-31\"}\n"), 0o600)
	var calls []string
	h.toolFn = func(name string, args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		// The embedded bundle (extracted under .hermes/policies/) always runs; data files go to both runs.
		if !strings.Contains(joined, "--policy "+filepath.Join(h.dir, ".hermes", "policies")) || !strings.Contains(joined, "--data "+exceptions) {
			t.Errorf("conftest args: %v", args)
		}
		return []byte(`[{"filename":"plan.json","namespace":"main","failures":[{"msg":"HCF-POL-001: public bucket"}]}]`), nil, errors.New("exit status 1")
	}
	h.must(ExitRefused, "plan", "--policy-dir", policyDir, "--policy-data", exceptions)
	// F-13: the embedded bundle runs on its own; the custom bundle in a second invocation.
	if len(calls) != 2 || strings.Contains(calls[0], policyDir) || !strings.Contains(calls[1], "--policy "+policyDir) {
		t.Errorf("want the embedded bundle alone, then the custom bundle: %q", calls)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "policy: FAILED (1 failure(s))") {
		t.Errorf("plan output:\n%s", out)
	}
	if !strings.Contains(out, "policy exception: HCF-POL-009 module.platform.module.secrets.x (reason: ticket EX-7; expires 2026-12-31; address not in this plan; from "+exceptions+")") {
		t.Errorf("every exception in effect must be printed with the plan:\n%s", out)
	}
	m := h.meta("dev", "plan")
	if len(m.Policy.Bundles) != 2 || m.Policy.Bundles[0].Source != "embedded" || m.Policy.Bundles[1].Path != policyDir || len(m.Policy.DataFiles) != 1 || len(m.Policy.DataFiles[0].SHA256) != 64 {
		t.Errorf("meta.json must record every policy bundle and data file digest: %+v", m.Policy)
	}
	if len(m.Policy.Exceptions) != 1 || m.Policy.Exceptions[0].Reason != "ticket EX-7" || m.Policy.Exceptions[0].InPlan {
		t.Errorf("meta.json must record the exceptions in effect: %+v", m.Policy.Exceptions)
	}
	evs := h.history("dev")
	if last := evs[len(evs)-1]; last.Action != "plan" || len(last.Policies) != 3 || last.Policies[0].SHA256 != m.Policy.Bundles[0].SHA256 || len(last.PolicyExceptions) != 1 || last.PolicyExceptions[0].Expires != "2026-12-31" {
		t.Errorf("history must record the policy digests and exceptions: %+v", last)
	}
	h.must(ExitRefused, "apply", "--yes")
	if !strings.Contains(h.stderr.String(), execution.CodePolicyFailed) {
		t.Errorf("apply must be refused by the policy gate:\n%s", h.stderr.String())
	}

	// A wildcard exception (QA: address "*") or one beyond 90 days is refused before conftest runs.
	for _, body := range []string{
		`{rule: HCF-POL-002, address: "*", reason: r, expires: "2026-12-31"}`,
		`{rule: HCF-POL-002, address: module.platform.x, reason: r, expires: "2261-01-01"}`,
	} {
		calls = nil
		_ = os.WriteFile(exceptions, []byte("hermes:\n  exceptions:\n    - "+body+"\n"), 0o600)
		h.must(ExitFailure, "plan", "--policy-data", exceptions)
		if len(calls) != 0 {
			t.Errorf("conftest must not run with an invalid exception: %q", calls)
		}
	}
}

func TestDestroyFlow(t *testing.T) {
	h := newHarness(t, "containerized")
	h.fake.PlanJSON = enginetest.PlanJSON(map[string][]string{"module.platform.x.a": {"delete"}})
	h.must(ExitOK, "destroy")
	if h.fake.Called("apply") {
		t.Fatal("destroy without --approve must not apply")
	}
	m := h.meta("dev", execution.KindDestroy)
	if m.Kind != execution.KindDestroy || !strings.Contains(strings.Join(h.fake.Calls, "|"), "-destroy") {
		t.Fatalf("destroy plan: %+v %v", m, h.fake.Calls)
	}
	h.must(ExitRefused, "apply", "--yes") // no apply plan; a destroy plan is not applied by apply
	h.must(ExitRefused, "destroy", "--approve", "ffffffffffff")
	h.must(ExitOK, "destroy", "--approve", m.PlanID[:12])

	// Protected: also --confirm-environment.
	h.must(ExitOK, "destroy", "--env", "production", "--skip-policy")
	pm := h.meta("production", execution.KindDestroy)
	h.must(ExitRefused, "destroy", "--env", "production", "--approve", pm.PlanID[:12])
	h.must(ExitRefused, "destroy", "--env", "production", "--approve", pm.PlanID[:12], "--confirm-environment", "dev")
	h.must(ExitOK, "destroy", "--env", "production", "--approve", pm.PlanID[:12], "--confirm-environment", "production")
}

func TestPlanTimeChecks(t *testing.T) {
	h := newHarness(t, "basic") // production on gcp without account and bucket
	h.must(ExitRefused, "plan", "--skip-policy")
	if !strings.Contains(h.stderr.String(), "HCF206") || !strings.Contains(h.stderr.String(), "HCF212") {
		t.Errorf("expected account and bucket plan errors:\n%s", h.stderr.String())
	}
	if h.fake.Called("init") {
		t.Error("the engine must not run when plan-time checks fail")
	}
}

func TestStatusAndBindings(t *testing.T) {
	h := newHarness(t, "containerized")
	h.must(ExitOK, "status")
	if !strings.Contains(h.stdout.String(), "s3://example-hermes-state/hermes/sample-shop/dev/aws/terraform.tfstate") {
		t.Errorf("status must show the state location:\n%s", h.stdout.String())
	}
	h.must(ExitRefused, "bindings", "-o", "json")
	h.must(ExitOK, "plan")
	h.must(ExitOK, "apply", "--yes")
	_ = os.MkdirAll(filepath.Join(h.dir, ".hermes", "build", "dev", ".terraform"), 0o750)
	h.fake.OutputJSON = `{"bindings":{"value":{"provider":"aws","services":{}}},"service_environment":{"value":{"api":{"HERMES_PROVIDER":"aws","LOG_LEVEL":"info level"}}},"secret_environment":{"value":{"api":{"PAYMENT_API_KEY":"arn:aws:secretsmanager:eu-north-1:123456789012:secret:x"}}}}`
	h.fake.StateJSON = `{"format_version":"1.0","values":{"root_module":{"child_modules":[{"resources":[{"mode":"managed"},{"mode":"data"},{"mode":"managed"}]}]}}}`
	h.must(ExitOK, "status", "-o", "json")
	var st statusReport
	if err := json.Unmarshal(h.stdout.Bytes(), &st); err != nil || st.Resources == nil || *st.Resources != 2 || st.LastApply == nil || st.Bindings == nil {
		t.Fatalf("status: %v %s", err, h.stdout.String())
	}
	h.must(ExitOK, "status", "--drift")
	h.fake.Drift = true
	h.must(ExitChanges, "status", "--drift")
	h.must(ExitOK, "bindings", "-o", "env", "--service", "api")
	if h.stdout.String() != "HERMES_PROVIDER=aws\nLOG_LEVEL='info level'\n" {
		t.Errorf("env output: %q", h.stdout.String())
	}
	h.must(ExitOK, "bindings", "-o", "json")
	if !strings.Contains(h.stdout.String(), `"secret_environment"`) {
		t.Errorf("json bindings: %s", h.stdout.String())
	}
	h.must(ExitRefused, "bindings", "-o", "env")
}

// F-15: the output of `hermes init` passes a plain `hermes validate` (every environment;
// warnings allowed, no errors: an error exits 2) for EVERY template x provider combination.
func TestInitTemplates(t *testing.T) {
	for _, tmpl := range Templates {
		for _, provider := range []string{"aws", "gcp", "alibaba"} {
			t.Run(tmpl+"/"+provider, func(t *testing.T) {
				h := newHarness(t, "")
				h.spec = filepath.Join(h.dir, "hermes.yaml")
				h.must(ExitOK, "init", "--name", "demo-app", "--provider", provider, "--template", tmpl, "--account", map[string]string{"aws": "123456789012", "gcp": "demo-project-1", "alibaba": ""}[provider])
				h.must(ExitOK, "validate")
				h.must(ExitOK, "validate", "--env", "dev")
				if !strings.Contains(readFile(t, filepath.Join(h.dir, ".gitignore")), ".hermes/") {
					t.Errorf("%s/%s: .gitignore not updated", tmpl, provider)
				}
				h.must(ExitRefused, "init", "--name", "demo-app", "--provider", provider, "--template", tmpl)
				h.must(ExitOK, "init", "--name", "demo-app", "--provider", provider, "--template", tmpl, "--force")
				if n := strings.Count(readFile(t, filepath.Join(h.dir, ".gitignore")), ".hermes/"); n != 1 {
					t.Errorf(".hermes/ added %d times", n)
				}
			})
		}
	}
	h := newHarness(t, "")
	h.must(ExitRefused, "init", "--name", "Bad_Name")
	h.must(ExitRefused, "init", "--name", "demo-app", "--template", "nope")
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDoctor(t *testing.T) {
	h := newHarness(t, "containerized")
	h.must(ExitOK, "doctor", "-o", "json")
	h.noEng = true
	h.must(ExitRefused, "doctor")
}

func TestStateCommands(t *testing.T) {
	h := newHarness(t, "containerized")
	h.must(ExitOK, "state", "show")
	if !strings.Contains(h.stdout.String(), "use_lockfile") && !strings.Contains(h.stdout.String(), "S3 native lockfile") {
		t.Errorf("state show:\n%s", h.stdout.String())
	}
	h.must(ExitOK, "state", "bootstrap")
	m := h.meta("_bootstrap-aws", execution.KindApply)
	if !m.Protected {
		t.Error("bootstrap plans always require approval")
	}
	root := readFile(t, filepath.Join(h.dir, ".hermes", "build", "_bootstrap-aws", "main.tf.json"))
	if !strings.Contains(root, `"local"`) || !strings.Contains(root, "bootstrap/aws/state-backend") {
		t.Errorf("bootstrap root:\n%s", root)
	}
	h.must(ExitRefused, "state", "bootstrap", "--apply")
	h.fake.OutputJSON = `{"bucket":{"value":"example-hermes-state"},"backend_config":{"value":{"bucket":"example-hermes-state","encrypt":true}}}`
	h.must(ExitOK, "state", "bootstrap", "--apply", "--approve", m.PlanID[:8])
	if !strings.Contains(h.stdout.String(), "chicken-and-egg") {
		t.Errorf("bootstrap must explain local state:\n%s", h.stdout.String())
	}
}

func TestInfoCommands(t *testing.T) {
	h := newHarness(t, "multi-cloud")
	h.must(ExitOK, "providers")
	h.must(ExitOK, "providers", "gcp", "-o", "json")
	h.must(ExitRefused, "providers", "azure")
	h.must(ExitOK, "version", "-o", "json")
	if !strings.Contains(h.stdout.String(), "hashicorp/google-beta") {
		t.Errorf("version must list pins: %s", h.stdout.String())
	}
	h.must(ExitOK, "portability", "-o", "json")
	// cost: no infracost installed → explains, never invents numbers.
	h.must(ExitOK, "plan")
	h.must(ExitOK, "cost")
	if !strings.Contains(h.stdout.String(), "never invents numbers") {
		t.Errorf("cost: %s", h.stdout.String())
	}
	h.must(ExitOK, "cost", "--env", "alidev")
	if !strings.Contains(h.stdout.String(), "does not price Alibaba") {
		t.Errorf("cost alibaba: %s", h.stdout.String())
	}
}

func TestGCPProjectFromEnvironment(t *testing.T) {
	h := newHarness(t, "multi-cloud")
	h.editSpec("account: example-project-1234", "account: \"\"")
	h.must(ExitRefused, "plan", "--env", "gcpdev")
	h.env["GOOGLE_CLOUD_PROJECT"] = "demo-project-from-env"
	h.must(ExitOK, "plan", "--env", "gcpdev")
	root := readFile(t, filepath.Join(h.dir, ".hermes", "build", "gcpdev", "main.tf.json"))
	if !strings.Contains(root, `"project": "demo-project-from-env"`) {
		t.Error("the project from GOOGLE_CLOUD_PROJECT must be rendered explicitly")
	}
	h.must(ExitOK, "apply", "--env", "gcpdev", "--yes")
}
