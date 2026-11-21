package migration

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func sampleInventory(t *testing.T) *inventory.Inventory {
	t.Helper()
	inv, err := inventory.Load(filepath.Join(repoRoot(), "examples", "migration", "sample-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func realData(t *testing.T) (*Catalog, *Workflows, *Pricing) {
	t.Helper()
	c, err := LoadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	w, err := LoadWorkflows("")
	if err != nil {
		t.Fatal(err)
	}
	p, err := LoadPricing("")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckReferences(c, w); err != nil {
		t.Fatal(err)
	}
	return c, w, p
}

var fixedNow = func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }

func assessSample(t *testing.T, target string) (*inventory.Inventory, *Assessment) {
	t.Helper()
	inv := sampleInventory(t)
	c, _, p := realData(t)
	a, err := Assess(inv, c, p, AssessOptions{Target: target, Now: fixedNow, Project: "fleet-platform", Env: "production"})
	if err != nil {
		t.Fatal(err)
	}
	return inv, a
}

func TestRealDataFilesLoad(t *testing.T) {
	c, w, p := realData(t)
	if len(c.Entries) < 50 || len(w.ByID) < 13 || len(p.Rates) < 10 {
		t.Fatalf("unexpectedly small data: %d entries, %d workflows, %d rates", len(c.Entries), len(w.ByID), len(p.Rates))
	}
	for _, id := range ReservedWorkflows {
		if w.ByID[id] == nil {
			t.Errorf("reserved workflow %s missing", id)
		}
	}
}

func TestCatalogMatchSemantics(t *testing.T) {
	c, _, _ := realData(t)
	cases := []struct {
		typ   string
		attrs map[string]any
		want  string
	}{
		{"AWS::RDS::DBInstance", map[string]any{"engine": "postgres"}, "aws-rds-postgresql"},
		{"AWS::RDS::DBInstance", map[string]any{"engine": "POSTGRES"}, "aws-rds-postgresql"}, // case-insensitive
		{"AWS::RDS::DBInstance", map[string]any{"engine": "db2-se"}, "aws-rds-other"},        // fallback without match
		{"AWS::SQS::Queue", map[string]any{"fifo_queue": true}, "aws-sqs-fifo-queue"},
		{"AWS::SQS::Queue", map[string]any{"fifo_queue": "TRUE"}, "aws-sqs-fifo-queue"},
		{"AWS::SQS::Queue", map[string]any{"fifo_queue": false}, "aws-sqs-queue"},
		{"AWS::EC2::Instance", map[string]any{"source_dest_check": false}, "aws-ec2-network-appliance"},
		{"AWS::EC2::Instance", map[string]any{"source_dest_check": true}, "aws-ec2-instance"},
		{"AWS::EC2::Instance", map[string]any{}, "aws-ec2-instance"},
		{"AWS::ElasticLoadBalancingV2::LoadBalancer", map[string]any{"type": "network"}, "aws-nlb"},
	}
	for _, tc := range cases {
		e := c.Match(tc.typ, tc.attrs)
		if e == nil || e.ID != tc.want {
			got := "<nil>"
			if e != nil {
				got = e.ID
			}
			t.Errorf("%s %v: got %s want %s", tc.typ, tc.attrs, got, tc.want)
		}
	}
	if c.Match("AWS::Nope::Thing", nil) != nil {
		t.Error("unknown type must not match")
	}
	if Stringify(12.0) != "12" || Stringify(0.5) != "0.5" || Stringify(int64(3)) != "3" || Stringify(false) != "false" {
		t.Error("Stringify shortest decimal / boolean form")
	}
}

func TestCatalogTieIsLoadError(t *testing.T) {
	doc := `apiVersion: hermes/v1alpha1
kind: ServiceCatalog
metadata: {reviewed_at: "2026-11-18", disclaimer: "x"}
entries:
` + entryYAML("a", `{engine: ["postgres"]}`) + entryYAML("b", `{engine: ["Postgres", "x"]}`)
	if _, err := ParseCatalog([]byte(doc)); err == nil || !strings.Contains(err.Error(), "tie") {
		t.Fatalf("want tie error, got %v", err)
	}
	ok := strings.Replace(doc, `{engine: ["Postgres", "x"]}`, `{engine: ["mysql"]}`, 1)
	if _, err := ParseCatalog([]byte(ok)); err != nil {
		t.Fatalf("disjoint values must not tie: %v", err)
	}
	more := strings.Replace(doc, `{engine: ["Postgres", "x"]}`, `{engine: ["postgres"], multi_az: [true]}`, 1)
	c, err := ParseCatalog([]byte(more))
	if err != nil {
		t.Fatal(err)
	}
	if e := c.Match("AWS::RDS::DBInstance", map[string]any{"engine": "postgres", "multi_az": true}); e == nil || e.ID != "b" {
		t.Fatalf("entry with most keys must win, got %+v", e)
	}
}

func entryYAML(id, match string) string {
	tgt := `{service: S, alternatives: [], compatibility: compatible, complexity: low, data_migration: none, workflow: foundation, tooling: [], lock_in: low, hermes_support: implemented, differences: [], issues: [], references: []}`
	return `  - id: ` + id + `
    category: relational-database
    capability: database
    source: {provider: aws, service: RDS, resource_types: ["AWS::RDS::DBInstance"], match: ` + match + `}
    targets: {gcp: ` + tgt + `, alibaba: ` + tgt + `}
`
}

func TestWorkflowGateRules(t *testing.T) {
	bad := `apiVersion: hermes/v1alpha1
kind: MigrationWorkflow
metadata: {id: x, title: X, applies_to: [other]}
spec:
  steps:
    - {id: drain, phase: synchronize, title: t, description: d, automation: manual, gate: none, reversible: true, rollback: r, risks: [], validation: [], source_impact: modifies-source-data}
`
	if _, err := ParseWorkflow([]byte(bad)); err == nil || !strings.Contains(err.Error(), "requires gate: approval") {
		t.Fatalf("modifies-source-data without approval must be rejected: %v", err)
	}
}

func TestAssessSample(t *testing.T) {
	inv, a := assessSample(t, "gcp")
	if a.Summary.ResourceCount != len(inv.Resources) || len(a.Gaps) != len(inv.Gaps) {
		t.Fatalf("counts: %+v", a.Summary)
	}
	if a.Summary.PortabilityScore <= 0 || a.Summary.PortabilityScore >= 100 {
		t.Errorf("portability score %v", a.Summary.PortabilityScore)
	}
	byID := map[string]AssessedResource{}
	for _, r := range a.Resources {
		byID[r.ID] = r
	}
	relay := byID["arn:aws:ec2:eu-north-1:123456789012:instance/i-0a00000000000003"]
	if relay.MappingID != "aws-ec2-network-appliance" {
		t.Errorf("VPN relay mapped to %s", relay.MappingID)
	}
	if byID["arn:aws:sqs:eu-north-1:123456789012:dispatch-commands.fifo"].MappingID != "aws-sqs-fifo-queue" {
		t.Error("FIFO queue not matched")
	}
	if a.Summary.DataVolume.RequiredBytes < 1_000_000_000_000 {
		t.Errorf("data volume too small: %d", a.Summary.DataVolume.RequiredBytes)
	}
	tc := a.Summary.TransferCost
	if tc.LowUSD <= 0 || tc.HighUSD < tc.LowUSD || len(tc.RatesUsed) == 0 || len(tc.ExitProgrammes) == 0 {
		t.Errorf("transfer cost: %+v", tc)
	}
	codes := map[string]bool{}
	for _, b := range a.Blockers {
		codes[b.Code] = true
	}
	for _, c := range []string{CodeDiscoveryGaps, CodePartial, CodeResourceRisk} {
		if !codes[c] {
			t.Errorf("expected finding %s", c)
		}
	}
	if len(a.Dependencies.Waves) < 3 {
		t.Errorf("waves: %d", len(a.Dependencies.Waves))
	}
	// Every resource lands in exactly one of mapping / unsupported.
	seen := map[string]int{}
	for _, m := range a.SpecMapping {
		seen[m.SourceID]++
	}
	for _, u := range a.Unsupported {
		seen[u.SourceID]++
	}
	for _, r := range inv.Resources {
		if seen[r.ID] != 1 {
			t.Errorf("%s appears %d times in mapping+unsupported", r.ID, seen[r.ID])
		}
	}
	raw, _ := json.Marshal(a.DraftSpec)
	for _, want := range []string{`"engine":"postgresql"`, `"version":"15"`, `"high_availability":true`, `"postgis"`, `"provider":"gcp"`, `"region":"europe-north1"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("draft spec missing %s: %s", want, raw)
		}
	}
	if strings.Contains(string(raw), `"10.0.0.0/16"`) {
		t.Error("target CIDR must not overlap the source VPC")
	}
}

func TestAssessDeterministic(t *testing.T) {
	_, a1 := assessSample(t, "alibaba")
	_, a2 := assessSample(t, "alibaba")
	if a1.Digest() != a2.Digest() {
		t.Fatal("assessment is not deterministic")
	}
}

func TestSanitizeName(t *testing.T) {
	for in, prefix := range map[string]string{"fleet-flight-logs-example": "fleet-flight-", "Telemetry_Ingest": "telemetry-ingest", "1abc": "r-1abc", "--": "res"} {
		got := SanitizeName(in, 20)
		if !nameRe.MatchString(got) || !strings.HasPrefix(got, prefix) {
			t.Errorf("%q -> %q (want prefix %q and the spec pattern)", in, got, prefix)
		}
	}
	if a, b := SanitizeName("fleet-flight-logs-example", 20), SanitizeName("fleet-flight-logs-example-2", 20); a == b {
		t.Error("truncated names must stay distinct")
	}
}
