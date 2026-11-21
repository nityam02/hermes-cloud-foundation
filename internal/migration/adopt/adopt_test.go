package adopt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

func sample(t *testing.T) *inventory.Inventory {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	inv, err := inventory.Load(filepath.Join(filepath.Dir(file), "..", "..", "..", "examples", "migration", "sample-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

// fakeEngine records calls and returns canned plan JSON.
type fakeEngine struct {
	planJSON string
	calls    []string
	applied  bool
}

func (f *fakeEngine) Init(_ context.Context, dir string, args ...string) error {
	f.calls = append(f.calls, "init")
	return nil
}
func (f *fakeEngine) Plan(_ context.Context, dir, planFile string, args ...string) (bool, error) {
	f.calls = append(f.calls, "plan "+strings.Join(args, " "))
	return true, os.WriteFile(filepath.Join(dir, planFile), []byte("binary-plan-"+f.planJSON), 0o600)
}
func (f *fakeEngine) ShowJSON(_ context.Context, dir, planFile string) ([]byte, error) {
	f.calls = append(f.calls, "show")
	return []byte(f.planJSON), nil
}
func (f *fakeEngine) ApplySaved(_ context.Context, dir, planFile string) error {
	f.calls = append(f.calls, "apply "+planFile)
	f.applied = true
	return nil
}

const importOnly = `{"format_version":"1.2","resource_changes":[
 {"address":"aws_s3_bucket.fleet_flight_logs_example","mode":"managed","type":"aws_s3_bucket","change":{"actions":["no-op"],"importing":{"id":"fleet-flight-logs-example"}}},
 {"address":"aws_sqs_queue.telemetry_ingest","mode":"managed","type":"aws_sqs_queue","change":{"actions":["no-op"],"importing":{"id":"https://sqs.eu-north-1.amazonaws.com/123456789012/telemetry-ingest"}}}]}`

const importWithUpdate = `{"format_version":"1.2","resource_changes":[
 {"address":"aws_s3_bucket.fleet_flight_logs_example","mode":"managed","type":"aws_s3_bucket","change":{"actions":["update"],"importing":{"id":"fleet-flight-logs-example"}}},
 {"address":"aws_db_instance.fleet_db","mode":"managed","type":"aws_db_instance","change":{"actions":["delete","create"]}}]}`

func TestGenerateImportBlocks(t *testing.T) {
	inv := sample(t)
	dir := t.TempDir()
	m, err := Generate(inv, dir, Options{AllowLocalState: true})
	if err != nil {
		t.Fatal(err)
	}
	imports, _ := os.ReadFile(filepath.Join(dir, "imports.tf"))
	for _, want := range []string{
		"to       = aws_s3_bucket.fleet_flight_logs_example\n  id       = \"fleet-flight-logs-example\"",
		"id       = \"https://sqs.eu-north-1.amazonaws.com/123456789012/telemetry-ingest\"",
		"id       = \"fleet/fleet-api\"", // ECS service cluster/service
		"id       = \"arn:aws:sns:eu-north-1:123456789012:telemetry\"",
		"to       = aws_db_instance.fleet_db\n  id       = \"fleet-db\"",
		"id       = \"vpc-0a1b2c3d4e5f60001\"",
		"id       = \"Z0EXAMPLE00001\"",
		"provider = aws.eu_north_1",
	} {
		if !strings.Contains(string(imports), want) {
			t.Errorf("imports.tf lacks %q", want)
		}
	}
	prov, _ := os.ReadFile(filepath.Join(dir, "providers.tf"))
	if !strings.Contains(string(prov), `allowed_account_ids = ["123456789012"]`) || !strings.Contains(string(prov), AWSProviderConstraint) {
		t.Errorf("providers.tf:\n%s", prov)
	}
	backend, _ := os.ReadFile(filepath.Join(dir, "backend.tf"))
	if !strings.Contains(string(backend), "source-adoption") {
		t.Errorf("backend must be a separate source-adoption state:\n%s", backend)
	}
	if len(m.Imports) < 30 {
		t.Errorf("only %d imports", len(m.Imports))
	}
	// IoT/Timestream have no mapping and are listed as skipped.
	skipped := map[string]bool{}
	for _, s := range m.Skipped {
		skipped[s.Type] = true
	}
	if !skipped["AWS::IoT::Thing"] {
		t.Error("unmapped types must be reported as skipped")
	}
}

func TestGenerateFiltersAndSafety(t *testing.T) {
	inv := sample(t)
	for i := range inv.Resources {
		if inv.Resources[i].Name == "fleet-db" {
			inv.Resources[i].ManagedBy, inv.Resources[i].ManagedByEvidence = "terraform", "found in terraform state x.tfstate"
		}
	}
	m, err := Generate(inv, t.TempDir(), Options{Types: []string{"AWS::RDS::DBInstance", "AWS::S3::Bucket"}, AllowLocalState: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, im := range m.Imports {
		if im.SourceType != "AWS::S3::Bucket" {
			t.Errorf("unexpected import %s", im.Address)
		}
	}
	if len(m.Skipped) != 1 || !strings.Contains(m.Skipped[0].Reason, "dual management") {
		t.Errorf("terraform-managed resource must be skipped: %+v", m.Skipped)
	}
	if _, err := Generate(inv, t.TempDir(), Options{Types: []string{"AWS::Nope::X"}, AllowLocalState: true}); err == nil {
		t.Error("unknown type must be rejected")
	}
	inventory.Redact(inv)
	if _, err := Generate(inv, t.TempDir(), Options{AllowLocalState: true}); err == nil || !strings.Contains(err.Error(), "redacted") {
		t.Errorf("redacted inventory must be refused: %v", err)
	}
}

func TestAdoptPlanVerifyApply(t *testing.T) {
	inv := sample(t)
	dir := t.TempDir()
	if _, err := Generate(inv, dir, Options{Types: []string{"AWS::S3::Bucket", "AWS::SQS::Queue"}, AllowLocalState: true}); err != nil {
		t.Fatal(err)
	}
	e := &fakeEngine{planJSON: importOnly}
	v, err := Plan(context.Background(), e, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK || v.Imports != 2 || e.calls[1] != "plan -generate-config-out=generated.tf" {
		t.Fatalf("verify: %+v calls=%v", v, e.calls)
	}
	sum := sha256.Sum256([]byte("binary-plan-" + importOnly))
	if v.PlanID != hex.EncodeToString(sum[:]) {
		t.Fatal("plan id must be the sha256 of the saved plan file")
	}
	if _, err := Apply(context.Background(), e, dir, "deadbeef"); !errors.Is(err, ErrApproval) || e.applied {
		t.Fatalf("wrong approval must refuse: %v", err)
	}
	// F-20: like hermes apply, the prefix is case-insensitive and trimmed (QA: adopt was not).
	if _, err := Apply(context.Background(), e, dir, " "+strings.ToUpper(v.PlanID[:8])+" "); err != nil || !e.applied {
		t.Fatalf("apply: %v", err)
	}

	// A plan that would modify or replace anything is refused with the offending addresses.
	e2 := &fakeEngine{planJSON: importWithUpdate}
	_ = os.WriteFile(filepath.Join(dir, GeneratedFile), []byte("# reviewed\n"), 0o600)
	v2, err := Plan(context.Background(), e2, dir)
	if !errors.Is(err, ErrNotImportOnly) || !strings.Contains(err.Error(), "aws_db_instance.fleet_db (replace)") || !strings.Contains(err.Error(), "aws_s3_bucket.fleet_flight_logs_example (update)") {
		t.Fatalf("want refusal naming offending addresses, got %v", err)
	}
	if v2.OK || strings.Contains(strings.Join(e2.calls, ","), "generate-config-out") {
		t.Errorf("config must not be regenerated when generated.tf exists: %v", e2.calls)
	}
	if _, err := Apply(context.Background(), e2, dir, v2.PlanID[:8]); !errors.Is(err, ErrNotImportOnly) || e2.applied {
		t.Fatal("apply must re-verify and refuse a non-import plan")
	}
}

// F-08 (a): types whose imported state would hold secret values are never adopted.
func TestSecretStateTypesAreNotAdoptable(t *testing.T) {
	inv := sample(t)
	inv.Resources = append(inv.Resources,
		inventory.Resource{ID: "arn:aws:ssm:eu-north-1:123456789012:parameter/app/api-key", Type: "AWS::SSM::Parameter", Name: "/app/api-key", Region: "eu-north-1"},
		inventory.Resource{ID: "arn:aws:ec2:eu-north-1:123456789012:vpn-connection/vpn-0123", Type: "AWS::EC2::VPNConnection", Name: "vpn-0123", Region: "eu-north-1", Attributes: map[string]any{"resource_id": "vpn-0123"}})
	dir := t.TempDir()
	m, err := Generate(inv, dir, Options{AllowLocalState: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, im := range m.Imports {
		if im.SourceType == "AWS::SSM::Parameter" || im.SourceType == "AWS::EC2::VPNConnection" {
			t.Errorf("%s must not be imported", im.SourceID)
		}
	}
	reasons := map[string]string{}
	for _, sk := range m.Skipped {
		reasons[sk.Type] = sk.Reason
	}
	for _, typ := range []string{"AWS::SSM::Parameter", "AWS::EC2::VPNConnection"} {
		if !strings.Contains(reasons[typ], "into state") {
			t.Errorf("%s must be skipped with the reason: %q", typ, reasons[typ])
		}
		if _, err := Generate(inv, t.TempDir(), Options{Types: []string{typ}, AllowLocalState: true}); err == nil || !strings.Contains(err.Error(), "not adoptable") {
			t.Errorf("requesting %s explicitly must be refused: %v", typ, err)
		}
		for _, st := range SupportedTypes() {
			if st == typ {
				t.Errorf("%s must not be listed as supported", typ)
			}
		}
	}
	imports, _ := os.ReadFile(filepath.Join(dir, "imports.tf"))
	if strings.Contains(string(imports), "aws_ssm_parameter") || strings.Contains(string(imports), "aws_vpn_connection") {
		t.Errorf("imports.tf must not import secret-bearing types:\n%s", imports)
	}
}

// F-08 (b): local adoption state needs --allow-local-state, also after backend.tf is edited.
func TestLocalAdoptionStateNeedsExplicitOptIn(t *testing.T) {
	inv := sample(t)
	opts := Options{Types: []string{"AWS::S3::Bucket"}}
	if _, err := Generate(inv, t.TempDir(), opts); !errors.Is(err, ErrLocalState) || !strings.Contains(err.Error(), "--allow-local-state") || !strings.Contains(err.Error(), "environment variable values") {
		t.Fatalf("a local adoption state must be refused with the reason: %v", err)
	}
	// Remote backend: allowed, encrypted.
	dir := t.TempDir()
	opts.BackendBucket, opts.BackendRegion = "example-adoption-state", "eu-north-1"
	m, err := Generate(inv, dir, opts)
	if err != nil || m.LocalStateAllowed {
		t.Fatalf("remote backend: %v %+v", err, m)
	}
	backend, _ := os.ReadFile(filepath.Join(dir, "backend.tf"))
	if !strings.Contains(string(backend), `backend "s3"`) || !strings.Contains(string(backend), "encrypt      = true") {
		t.Errorf("backend.tf:\n%s", backend)
	}
	readme, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	if !strings.Contains(string(readme), "must be stored like a secret") {
		t.Errorf("README must warn about state sensitivity:\n%s", readme)
	}
	// Switching the generated workspace to local state afterwards is refused by plan and apply.
	e := &fakeEngine{planJSON: importOnly}
	for name, body := range map[string]string{
		"local backend": "terraform {\n  backend \"local\" {\n    path = \"x.tfstate\"\n  }\n}\n",
		"no backend":    "# removed\n",
	} {
		_ = os.WriteFile(filepath.Join(dir, "backend.tf"), []byte(body), 0o600)
		if _, err := Plan(context.Background(), e, dir); !errors.Is(err, ErrLocalState) {
			t.Errorf("%s: plan must refuse: %v", name, err)
		}
		if _, err := Apply(context.Background(), e, dir, "deadbeefdeadbeef"); !errors.Is(err, ErrLocalState) || e.applied {
			t.Errorf("%s: apply must refuse: %v", name, err)
		}
	}
	if len(e.calls) != 0 {
		t.Errorf("the engine must not run: %v", e.calls)
	}
	// Explicit opt-in: allowed and recorded.
	local := t.TempDir()
	m, err = Generate(inv, local, Options{Types: []string{"AWS::S3::Bucket", "AWS::SQS::Queue"}, AllowLocalState: true})
	if err != nil || !m.LocalStateAllowed {
		t.Fatalf("--allow-local-state: %v %+v", err, m)
	}
	if _, err := Plan(context.Background(), e, local); err != nil {
		t.Errorf("an explicitly allowed local state must plan: %v", err)
	}
}
