package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
	awsinv "github.com/nityam02/hermes-cloud-foundation/internal/inventory/aws"
	"github.com/nityam02/hermes-cloud-foundation/internal/migration/adopt"
)

func migRepo() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func migSample() string {
	return filepath.Join(migRepo(), "examples", "migration", "sample-inventory.json")
}

func migRun(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	return migRunApp(t, dir, nil, args...)
}

// migRunApp runs the CLI; conftest is "not installed" unless configure provides tools.
func migRunApp(t *testing.T, dir string, configure func(*App), args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	app := &App{Build: BuildInfo{Version: "test"}, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, WorkDir: dir,
		Now: func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) },
		LookPath: func(name string) (string, error) {
			if name == "conftest" {
				return "", errors.New("not found")
			}
			return exec.LookPath(name)
		}}
	if configure != nil {
		configure(app)
	}
	code := Run(app, args)
	return code, out.String(), errb.String()
}

func TestMigInventoryAWSUsesDiscoverer(t *testing.T) {
	dir := t.TempDir()
	orig := migDiscoverAWS
	defer func() { migDiscoverAWS = orig }()
	var gotSO awsinv.SessionOptions
	var gotO awsinv.Options
	migDiscoverAWS = func(_ context.Context, so awsinv.SessionOptions, o awsinv.Options) (*inventory.Inventory, error) {
		gotSO, gotO = so, o
		return inventory.Load(migSample())
	}
	out := filepath.Join(dir, "inv.json")
	code, stdout, stderr := migRun(t, dir, "inventory", "aws", "--role-arn", "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly",
		"--external-id", "demo-external-id", "--regions", "eu-north-1,eu-west-1", "--services", "ec2,s3", "--redact", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if gotSO.ExternalID != "demo-external-id" || !gotO.ExternalIDUsed || len(gotO.Regions) != 2 || len(gotO.Services) != 2 || gotSO.Recorder == nil {
		t.Fatalf("options not passed through: %+v %+v", gotSO, gotO)
	}
	if !strings.Contains(stdout, "Discovered 62 resource(s)") || !strings.Contains(stdout, "gap: kinesis") {
		t.Errorf("summary: %s", stdout)
	}
	data, _ := os.ReadFile(out)
	if strings.Contains(string(data), "123456789012") {
		t.Error("--redact output contains the account id")
	}
	if code, _, _ := migRun(t, dir, "inventory", "aws", "--external-id", "x"); code != ExitRefused {
		t.Errorf("--external-id without --role-arn: exit %d", code)
	}
}

func TestMigInventoryPolicyGraphImport(t *testing.T) {
	dir := t.TempDir()
	code, stdout, _ := migRun(t, dir, "inventory", "policy")
	want, _ := os.ReadFile(filepath.Join(migRepo(), "migration", "discovery", "aws-readonly-discovery-policy.json"))
	if code != 0 || stdout != string(want) {
		t.Fatal("inventory policy must print the committed policy file")
	}
	code, stdout, _ = migRun(t, dir, "inventory", "graph", "--inventory", migSample(), "--format", "dot")
	if code != 0 || !strings.HasPrefix(stdout, "digraph inventory") {
		t.Fatalf("dot: %d %s", code, stdout[:40])
	}
	code, stdout, _ = migRun(t, dir, "inventory", "graph", "--inventory", migSample(), "-o", "json", "--focus", "arn:aws:ec2:eu-north-1:123456789012:vpc/vpc-0a1b2c3d4e5f60001")
	var doc struct {
		Radius struct {
			Dependents map[string]int `json:"dependents"`
		} `json:"blast_radius"`
	}
	if code != 0 || json.Unmarshal([]byte(stdout), &doc) != nil || len(doc.Radius.Dependents) < 20 {
		t.Fatalf("json focus: %d %d", code, len(doc.Radius.Dependents))
	}
	if code, _, _ := migRun(t, dir, "inventory", "graph", "--inventory", migSample(), "--focus", "nope"); code != ExitRefused {
		t.Errorf("unknown focus: exit %d", code)
	}
	cfg := filepath.Join(dir, "config.json")
	_ = os.WriteFile(cfg, []byte(`{"Results":["{\"resourceId\":\"i-1\",\"resourceType\":\"AWS::EC2::Instance\",\"awsRegion\":\"eu-north-1\",\"arn\":\"arn:aws:ec2:eu-north-1:123456789012:instance/i-1\",\"accountId\":\"123456789012\",\"configuration\":{\"sourceDestCheck\":false,\"instanceType\":\"t3.small\"},\"relationships\":[{\"resourceId\":\"vpc-1\",\"resourceType\":\"AWS::EC2::VPC\",\"relationshipName\":\"Is contained in Vpc\"}]}"]}`), 0o600)
	code, stdout, stderr := migRun(t, dir, "inventory", "import", "--format", "aws-config", cfg)
	if code != 0 {
		t.Fatalf("import: %s", stderr)
	}
	inv, err := inventory.Decode(strings.NewReader(stdout))
	if err != nil || inv.Metadata.Collector != "aws-config" || inv.Resources[0].Completeness != "partial" || inv.Resources[0].Attr("source_dest_check") != false {
		t.Fatalf("imported inventory: %v %+v", err, inv)
	}
}

func TestMigPipeline(t *testing.T) {
	dir := t.TempDir()
	p := func(n string) string { return filepath.Join(dir, n) }
	steps := [][]string{
		{"migrate", "catalog", "--format", "markdown"},
		{"migrate", "assess", "--inventory", migSample(), "--target", "alibaba", "--out", p("a.json"), "--markdown", p("a.md")},
		{"migrate", "plan", "--assessment", p("a.json"), "--env", "prod", "--out", p("plan.yaml"), "--markdown", p("plan.md")},
		{"migrate", "generate", "--assessment", p("a.json"), "--env", "prod", "--out", p("target")},
	}
	for _, args := range steps {
		if code, out, errs := migRun(t, dir, args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s\n%s", args, code, out, errs)
		}
	}
	checks := "apiVersion: hermes/v1alpha1\nkind: ValidationChecks\nmetadata: {name: t}\nchecks:\n  - {id: cov, type: spec-coverage, spec: target/hermes.yaml}\n"
	_ = os.WriteFile(p("checks.yaml"), []byte(checks), 0o600)
	if code, out, errs := migRun(t, dir, "migrate", "validate", "--plan", p("plan.yaml"), "--checks", p("checks.yaml"), "--out", p("report.json")); code != 0 {
		t.Fatalf("validate: %d %s %s", code, out, errs)
	}
	failing := checks + "  - {id: no, type: command, argv: [\"false\"]}\n"
	_ = os.WriteFile(p("checks-fail.yaml"), []byte(failing), 0o600)
	if code, _, _ := migRun(t, dir, "migrate", "validate", "--plan", p("plan.yaml"), "--checks", p("checks-fail.yaml"), "--out", p("bad.json")); code != ExitRefused {
		t.Errorf("failing validation must exit 2, got %d", code)
	}
	code, out, _ := migRun(t, dir, "migrate", "cutover", "--plan", p("plan.yaml"), "--report", p("report.json"))
	if code != 0 || !strings.Contains(out, "DRY-RUN") || !strings.Contains(out, "[MISSING] approval") {
		t.Fatalf("dry-run: %d %s", code, out)
	}
	if code, _, _ := migRun(t, dir, "migrate", "cutover", "--plan", p("plan.yaml"), "--report", p("report.json"), "--execute"); code != ExitRefused {
		t.Errorf("execute without approval: exit %d", code)
	}
	if code, _, _ := migRun(t, dir, "migrate", "cutover", "--plan", p("plan.yaml"), "--report", p("bad.json"), "--execute", "--approve", "00000000", "--confirm-target", "prod"); code != ExitRefused {
		t.Errorf("execute with failing report: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".hermes", "migrations")); err != nil {
		t.Errorf("state dir not under --workdir: %v", err)
	}
	// The dry-run binds the hooks file digest; --execute refuses an edited file.
	_ = os.WriteFile(p("hooks.yaml"), []byte("apiVersion: hermes/v1alpha1\nkind: CutoverHooks\nhooks: {}\n"), 0o600)
	code, out, _ = migRun(t, dir, "migrate", "cutover", "--plan", p("plan.yaml"), "--report", p("report.json"), "--hooks", p("hooks.yaml"))
	if code != 0 || !strings.Contains(out, "sha256") || !strings.Contains(out, "Approve these hooks with --approve-hooks ") || !strings.Contains(out, "not hashed") {
		t.Fatalf("dry-run with hooks: %d %s", code, out)
	}
	planID := strings.Fields(strings.TrimPrefix(out, "Cutover for plan "))[0]
	_ = os.WriteFile(p("hooks.yaml"), []byte("apiVersion: hermes/v1alpha1\nkind: CutoverHooks\nhooks: {} # edited after review\n"), 0o600)
	code, _, errs := migRun(t, dir, "migrate", "cutover", "--plan", p("plan.yaml"), "--report", p("report.json"), "--hooks", p("hooks.yaml"),
		"--execute", "--approve", planID[:8], "--confirm-target", "prod")
	if code != ExitRefused || !strings.Contains(errs, "re-run the dry-run") {
		t.Errorf("execute with a hooks file edited after the dry-run: exit %d %s", code, errs)
	}
	if code, _, _ := migRun(t, dir, "migrate", "assess", "--inventory", migSample(), "--target", "aws"); code != ExitRefused {
		t.Errorf("bad target: exit %d", code)
	}
}

type migFakeEngine struct{ plan string }

func (f *migFakeEngine) Init(context.Context, string, ...string) error { return nil }
func (f *migFakeEngine) Plan(_ context.Context, dir, planFile string, _ ...string) (bool, error) {
	return true, os.WriteFile(filepath.Join(dir, planFile), []byte(f.plan), 0o600)
}
func (f *migFakeEngine) ShowJSON(_ context.Context, dir, planFile string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, planFile)) // like the engine: show the SAVED plan
}
func (f *migFakeEngine) ApplySaved(context.Context, string, string) error { return nil }

func TestMigAdoptCommands(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "adopt")
	// F-08: a local adoption state needs an explicit opt-in; the state warning is always shown.
	if code, _, errs := migRun(t, dir, "adopt", "aws", "--inventory", migSample(), "--types", "AWS::S3::Bucket", "--out", ws); code != ExitRefused || !strings.Contains(errs, "--allow-local-state") {
		t.Fatalf("a local adoption state must be refused: %d %s", code, errs)
	}
	if code, _, errs := migRun(t, dir, "adopt", "aws", "--inventory", migSample(), "--types", "AWS::SSM::Parameter", "--allow-local-state", "--out", ws); code != ExitRefused || !strings.Contains(errs, "not adoptable") {
		t.Fatalf("SSM parameters must never be adopted: %d %s", code, errs)
	}
	if code, _, errs := migRun(t, dir, "adopt", "aws", "--inventory", migSample(), "--types", "AWS::S3::Bucket", "--allow-local-state", "--out", ws); code != 0 || !strings.Contains(errs, "must be stored like a secret") {
		t.Fatalf("adopt aws: %d %s", code, errs)
	}
	orig := migAdoptEngine
	defer func() { migAdoptEngine = orig }()
	bad := &migFakeEngine{plan: `{"format_version":"1.2","resource_changes":[{"address":"aws_s3_bucket.fleet_static_site_example","mode":"managed","type":"aws_s3_bucket","change":{"actions":["update"],"importing":{"id":"x"}}}]}`}
	migAdoptEngine = func(context.Context, *App, string) (adopt.Engine, error) { return bad, nil }
	code, out, errs := migRun(t, dir, "adopt", "plan", "--dir", ws)
	if code != ExitRefused || !strings.Contains(out, "REFUSED aws_s3_bucket.fleet_static_site_example") || !strings.Contains(errs, "never create, modify") {
		t.Fatalf("non-import plan must be refused: %d\n%s\n%s", code, out, errs)
	}
	good := &migFakeEngine{plan: `{"format_version":"1.2","resource_changes":[{"address":"aws_s3_bucket.fleet_static_site_example","mode":"managed","type":"aws_s3_bucket","change":{"actions":["no-op"],"importing":{"id":"fleet-static-site-example"}}}]}`}
	migAdoptEngine = func(context.Context, *App, string) (adopt.Engine, error) { return good, nil }
	if code, _, errs := migRun(t, dir, "adopt", "verify", "--dir", ws); code != ExitRefused {
		t.Fatalf("verify must re-read the saved (bad) plan: %d %s", code, errs)
	}
	// F-08 (c): the embedded policies run on the adoption plan; findings are advisory warnings.
	var conftestArgs []string
	withConftest := func(app *App) {
		app.LookPath = func(string) (string, error) { return "/fake/conftest", nil }
		app.RunTool = func(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
			conftestArgs = args
			return []byte(`[{"filename":"adopt-plan.json","namespace":"main","failures":[{"msg":"HCF-POL-001: aws_s3_bucket.fleet_static_site_example: public bucket"}]}]`), nil, errors.New("exit status 1")
		}
	}
	code, out, errs = migRunApp(t, dir, withConftest, "adopt", "plan", "--dir", ws)
	if code != 0 || !strings.Contains(out, "1 import(s), 0 non-import") {
		t.Fatalf("good plan: %d %s %s", code, out, errs)
	}
	if !strings.Contains(strings.Join(conftestArgs, " "), filepath.Join(ws, adopt.PlanJSONFile)) {
		t.Errorf("the policies must evaluate the adoption plan JSON: %v", conftestArgs)
	}
	if !strings.Contains(errs, "warning HCF303") || !strings.Contains(errs, "public bucket") || !strings.Contains(errs, "advisory") || !strings.Contains(errs, "must be stored like a secret") {
		t.Errorf("policy findings must be printed as advisory warnings, with the state warning:\n%s", errs)
	}
	if code, _, _ := migRun(t, dir, "adopt", "apply", "--dir", ws, "--approve", "short"); code != ExitRefused {
		t.Errorf("apply without valid approval: %d", code)
	}
}
