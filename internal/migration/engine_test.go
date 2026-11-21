package migration

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func samplePlan(t *testing.T) (*Assessment, *Plan) {
	t.Helper()
	_, a := assessSample(t, "gcp")
	_, ws, _ := realData(t)
	p, err := BuildPlan(a, ws, PlanOptions{Environment: "production", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	return a, p
}

func TestPlanFromSample(t *testing.T) {
	a, p := samplePlan(t)
	_, ws, _ := realData(t)
	p2, _ := BuildPlan(a, ws, PlanOptions{Environment: "production", Now: func() time.Time { return fixedNow().Add(time.Hour) }})
	if p.Metadata.PlanID != p2.Metadata.PlanID || len(p.Metadata.PlanID) != 64 {
		t.Fatalf("plan_id must be a deterministic sha256 of the body: %s vs %s", p.Metadata.PlanID, p2.Metadata.PlanID)
	}
	seen := map[string]bool{}
	lastRank := -1
	for _, ph := range p.Phases {
		if PhaseRank(ph.ID) < lastRank {
			t.Errorf("phases out of order at %s", ph.ID)
		}
		lastRank = PhaseRank(ph.ID)
		for _, s := range ph.Steps {
			seen[s.Workflow] = true
			if SourceImpactGated(s.SourceImpact) && s.Gate != "approval" {
				t.Errorf("%s touches the source without an approval gate", s.ID)
			}
			if (s.Phase == "cutover" || s.Phase == "decommission") && s.Gate != "approval" {
				t.Errorf("%s: %s step without approval gate", s.ID, s.Phase)
			}
			if s.Phase == "decommission" && s.Automation != "manual" {
				t.Errorf("%s: decommission must be manual", s.ID)
			}
		}
	}
	for _, wf := range []string{"foundation", "hybrid-connectivity", "traffic-cutover", "decommission", "relational-database-postgresql", "object-storage", "messaging"} {
		if !seen[wf] {
			t.Errorf("plan lacks workflow %s", wf)
		}
	}
	if len(p.Coverage) == 0 {
		t.Error("plan coverage is empty")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.yaml")
	y, err := p.YAML()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, y, 0o600); err != nil {
		t.Fatal(err)
	}
	lp, err := LoadPlan(path)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if lp.Metadata.PlanID != p.Metadata.PlanID {
		t.Fatal("plan id changed on round trip")
	}
	tampered := strings.Replace(string(y), "gate: approval", "gate: none", 1)
	_ = os.WriteFile(path, []byte(tampered), 0o600)
	if _, err := LoadPlan(path); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered plan must be rejected, got %v", err)
	}
	if md := PlanMarkdown(p); !strings.Contains(md, "## 1. foundation") {
		t.Error("plan markdown")
	}
}

func TestGenerateAndSpecCoverage(t *testing.T) {
	a, p := samplePlan(t)
	dir := t.TempDir()
	res, err := Generate(a, filepath.Join(dir, "target"), "fleet-platform", "staging")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 3 {
		t.Fatalf("files: %v", res.Files)
	}
	spec, _ := os.ReadFile(filepath.Join(dir, "target", "hermes.yaml"))
	for _, want := range []string{"# DRAFT — requires human review", "hermes render -c", "environment: staging", "engine: postgresql", "provider: gcp"} {
		if !strings.Contains(string(spec), want) {
			t.Errorf("hermes.yaml lacks %q", want)
		}
	}
	// F-17: source-registry (ECR) images are replaced by a placeholder and listed for copying.
	if strings.Contains(string(spec), "image: 123456789012.dkr.ecr") || !strings.Contains(string(spec), "image: REPLACE-WITH-TARGET-REGISTRY/fleet/api:2.8.1") ||
		!strings.Contains(string(spec), "image 123456789012.dkr.ecr.eu-north-1.amazonaws.com/fleet/api:2.8.1 is in the source registry") {
		t.Errorf("hermes.yaml must use the placeholder and flag the image for review:\n%s", spec)
	}
	mapping, _ := os.ReadFile(filepath.Join(dir, "target", "MAPPING.md"))
	if !strings.Contains(string(mapping), "## Container images to copy to the target registry") ||
		!strings.Contains(string(mapping), "| `123456789012.dkr.ecr.eu-north-1.amazonaws.com/fleet/api:2.8.1` | `REPLACE-WITH-TARGET-REGISTRY/fleet/api:2.8.1` |") {
		t.Errorf("MAPPING.md must list the images to copy:\n%s", mapping)
	}
	unsup, _ := os.ReadFile(filepath.Join(dir, "target", "UNSUPPORTED.md"))
	if !strings.Contains(string(unsup), "AWS::Route53::HostedZone") || !strings.Contains(string(unsup), "AWS::DynamoDB::Table") {
		t.Errorf("UNSUPPORTED.md incomplete:\n%s", unsup)
	}
	checks := writeChecks(t, dir, "  - {id: coverage, type: spec-coverage, spec: target/hermes.yaml}\n")
	rep := (&Validator{Plan: p, Now: fixedNow}).Run(context.Background(), checks)
	if !rep.Passed {
		t.Fatalf("spec-coverage should pass on the generated spec: %+v", rep.Results)
	}
	// Remove a bucket: coverage must fail and name it.
	edited := strings.Replace(string(spec), "name: fleet-mission", "name: renamed-bucket", 1)
	_ = os.WriteFile(filepath.Join(dir, "target", "hermes.yaml"), []byte(edited), 0o600)
	rep = (&Validator{Plan: p}).Run(context.Background(), checks)
	if rep.Passed || !strings.Contains(fmt.Sprint(rep.Results[0].Evidence["missing"]), "bucket") {
		t.Fatalf("coverage should fail: %+v", rep.Results[0])
	}
}

func writeChecks(t *testing.T, dir, body string) *Checks {
	t.Helper()
	path := filepath.Join(dir, "checks.yaml")
	doc := "apiVersion: hermes/v1alpha1\nkind: ValidationChecks\nmetadata: {name: test}\nchecks:\n" + body
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadChecks(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestValidationCheckTypes(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte("status: ok"))
			return
		}
		w.WriteHeader(503)
	}))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	_ = os.WriteFile(filepath.Join(dir, "ca.pem"), ca, 0o600)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedAddr := closed.Addr().String()
	_ = closed.Close()
	_ = os.WriteFile(filepath.Join(dir, "bindings.json"), []byte(`{"provider":"gcp","buckets":{"images":{"name":"x","url":"gs://x"}},"topics":{"telemetry":{"id":"t","subscriptions":{"ingest":"s"}}},"database":{"endpoint":"10.0.0.5","port":5432}}`), 0o600)
	body := fmt.Sprintf(`  - {id: http-ok, type: http, url: "%[1]s/healthz", body_contains: ok, ca_file: ca.pem}
  - {id: http-bad, type: http, url: "%[1]s/down", ca_file: ca.pem}
  - {id: http-untrusted, type: http, url: "%[1]s/healthz"}
  - {id: tcp-ok, type: tcp, address: "%[2]s"}
  - {id: tcp-closed, type: tcp, address: "%[3]s", timeout_seconds: 2}
  - {id: dns-local, type: dns, name: localhost, expect: {values: ["127.0.0.1"]}}
  - {id: cmd-ok, type: command, argv: ["true"]}
  - {id: cmd-fail, type: command, argv: ["false"]}
  - {id: bindings-ok, type: bindings, file: bindings.json, expect: {buckets: [images], topics: [telemetry], subscriptions: [ingest], database: true}}
  - {id: bindings-missing, type: bindings, file: bindings.json, expect: {secrets: [db-password]}}
`, srv.URL, ln.Addr().String(), closedAddr)
	c := writeChecks(t, dir, body)
	rep := (&Validator{Plan: &Plan{Metadata: PlanMetadata{PlanID: "abc"}}}).Run(context.Background(), c)
	want := map[string]string{"http-ok": "pass", "http-bad": "fail", "http-untrusted": "fail", "tcp-ok": "pass", "tcp-closed": "fail",
		"dns-local": "pass", "cmd-ok": "pass", "cmd-fail": "fail", "bindings-ok": "pass", "bindings-missing": "fail"}
	for _, r := range rep.Results {
		if want[r.ID] != r.Status {
			t.Errorf("%s: got %s (%s) want %s", r.ID, r.Status, r.Message, want[r.ID])
		}
	}
	if rep.Passed || rep.Metadata.PlanID != "abc" || rep.Summary.Total != len(want) {
		t.Errorf("report summary: %+v", rep.Summary)
	}
	// Plain http is refused at load time unless allow_insecure.
	path := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(path, []byte("apiVersion: hermes/v1alpha1\nkind: ValidationChecks\nmetadata: {name: x}\nchecks:\n  - {id: h, type: http, url: \"http://127.0.0.1:1/\"}\n"), 0o600)
	if _, err := LoadChecks(path); err == nil || !strings.Contains(err.Error(), "allow_insecure") {
		t.Fatalf("plain http must require allow_insecure: %v", err)
	}
}

// cutoverPlan is a small hand-built plan with cutover, monitor and decommission steps.
func cutoverPlan() *Plan {
	step := func(id, phase, impact string, rev bool, deps ...string) PlanStep {
		gate := "approval"
		if phase == "monitor" {
			gate = "none"
		}
		return PlanStep{ID: id, Workflow: "traffic-cutover", WorkflowStep: id, Title: id, Phase: phase, Automation: "manual", Gate: gate,
			SourceImpact: impact, Reversible: rev, Rollback: "undo " + id, Validation: []string{}, Risks: []string{}, ResourceRefs: []string{}, DependsOn: append([]string{}, deps...)}
	}
	p := &Plan{APIVersion: APIVersion, Kind: "MigrationPlan", Notes: []string{}, Coverage: []CoverageItem{},
		Metadata: PlanMetadata{Target: PlanEndpoint{Provider: "gcp", Environment: "production"}},
		Phases: []PlanPhase{
			{ID: "validate", Order: 5, Steps: []PlanStep{step("pre-check", "validate", "none", true)}},
			{ID: "cutover", Order: 6, Steps: []PlanStep{
				step("shift-10", "cutover", "none", true, "pre-check"),
				step("freeze-writes", "cutover", "modifies-source-data", true, "shift-10"),
				step("promote", "cutover", "none", false, "freeze-writes"),
			}},
			{ID: "monitor", Order: 7, Steps: []PlanStep{step("watch", "monitor", "none", true, "promote")}},
			{ID: "decommission", Order: 8, Steps: []PlanStep{step("delete-source", "decommission", "modifies-source-config", false, "watch")}},
		}}
	p.Metadata.PlanID = p.ComputeID()
	return p
}

func passingReport(p *Plan, finished time.Time) *Report {
	return &Report{APIVersion: APIVersion, Kind: "ValidationReport", Passed: true, Summary: ReportSummary{Total: 1, Passed: 1},
		Metadata: ReportMetadata{PlanID: p.Metadata.PlanID, FinishedAt: finished.UTC().Format(time.RFC3339)},
		Results:  []CheckResult{{ID: "http-health", Type: "http", Status: "pass"}}}
}

func TestCutoverStateMachine(t *testing.T) {
	p := cutoverPlan()
	work := t.TempDir()
	hooksPath := filepath.Join(work, "hooks.yaml")
	_ = os.WriteFile(hooksPath, []byte(`apiVersion: hermes/v1alpha1
kind: CutoverHooks
hooks:
  shift-10: {run: ["./shift.sh", "10"], rollback: ["./shift.sh", "0"]}
  freeze-writes: {run: ["./freeze.sh"], rollback: ["./unfreeze.sh"]}
  promote: {run: ["./promote.sh"]}
`), 0o600)
	hooks, err := LoadHooks(hooksPath, p)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	var ran []string
	open := func() *Controller {
		c, err := OpenController(p, work)
		if err != nil {
			t.Fatal(err)
		}
		c.Hooks, c.Now, c.User = hooks, func() time.Time { return now }, "tester"
		c.Runner = func(_ context.Context, argv []string, _ string, env []string, out io.Writer) error {
			ran = append(ran, strings.Join(argv, " "))
			if !strings.Contains(strings.Join(env, " "), "HERMES_PLAN_ID="+p.Metadata.PlanID) {
				return errors.New("missing env")
			}
			_, _ = io.WriteString(out, "ok")
			return nil
		}
		return c
	}
	c := open()
	if err := c.RecordDryRun(); err != nil { // binds the hooks file digest
		t.Fatal(err)
	}
	if rb := c.Runbook(); len(rb) != 5 || !strings.Contains(rb[4].Action, "never executed") {
		t.Fatalf("runbook: %+v", rb)
	}
	good := ExecGates{Report: passingReport(p, now.Add(-time.Hour)), MaxReportAge: 4 * time.Hour, Approve: p.Metadata.PlanID[:8], ConfirmTarget: "production",
		ApproveHooks: c.Hooks.Digest()[:8]}
	refusals := map[string]ExecGates{
		"no report":     {MaxReportAge: 4 * time.Hour, Approve: good.Approve, ConfirmTarget: "production"},
		"stale report":  {Report: passingReport(p, now.Add(-5*time.Hour)), MaxReportAge: 4 * time.Hour, Approve: good.Approve, ConfirmTarget: "production"},
		"short approve": {Report: good.Report, MaxReportAge: 4 * time.Hour, Approve: p.Metadata.PlanID[:6], ConfirmTarget: "production"},
		"wrong target":  {Report: good.Report, MaxReportAge: 4 * time.Hour, Approve: good.Approve, ConfirmTarget: "staging"},
	}
	other := *good.Report
	other.Metadata.PlanID = "0000000000000000"
	refusals["other plan"] = ExecGates{Report: &other, MaxReportAge: 4 * time.Hour, Approve: good.Approve, ConfirmTarget: "production"}
	// F-10, the QA forgery: a failing report edited to passed=true, summary.failed=0. The
	// per-check results still say fail; the gate recomputes from them.
	forged := *good.Report
	forged.Passed, forged.Summary = true, ReportSummary{Total: 2, Passed: 2}
	forged.Results = []CheckResult{{ID: "http-health", Status: "pass"}, {ID: "tcp-db", Status: "fail"}}
	refusals["forged passed flag"] = ExecGates{Report: &forged, MaxReportAge: 4 * time.Hour, Approve: good.Approve, ConfirmTarget: "production"}
	errored := forged
	errored.Results = []CheckResult{{ID: "http-health", Status: "pass"}, {ID: "dns", Status: "error"}}
	refusals["check errored"] = ExecGates{Report: &errored, MaxReportAge: 4 * time.Hour, Approve: good.Approve, ConfirmTarget: "production"}
	empty := forged
	empty.Results = nil
	refusals["no results"] = ExecGates{Report: &empty, MaxReportAge: 4 * time.Hour, Approve: good.Approve, ConfirmTarget: "production"}
	upper := good
	upper.Approve = strings.ToUpper(good.Approve)
	upper.ApproveHooks = " " + strings.ToUpper(good.ApproveHooks) + " "
	for _, gs := range c.CheckGates(upper, true) {
		if !gs.OK {
			t.Errorf("F-20: upper-case or padded prefixes must be accepted like hermes apply: %+v", gs)
		}
	}
	if gs := c.CheckGates(refusals["forged passed flag"], true); gs[0].OK || !strings.Contains(gs[0].Detail, "1/2 checks passed (1 failed, 0 errors), recomputed") {
		t.Errorf("forged report gate: %+v", gs[0])
	}
	for name, g := range refusals {
		if _, err := c.Execute(context.Background(), g, nil, ""); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: want refusal, got %v", name, err)
		}
	}
	if len(ran) != 0 {
		t.Fatalf("hooks ran despite refused gates: %v", ran)
	}
	// Executes shift-10, then refuses the source-impacting step without --approve-source-step.
	res, err := c.Execute(context.Background(), good, nil, "")
	if !errors.Is(err, ErrRefused) || len(res.Executed) != 1 || res.Executed[0] != "shift-10" {
		t.Fatalf("want shift-10 then refusal, got %+v %v", res, err)
	}
	c = open() // state persisted
	if c.State.Steps["shift-10"].Status != StepDone {
		t.Fatal("state not persisted")
	}
	res, err = c.Execute(context.Background(), good, []string{"freeze-writes"}, "")
	if err != nil || len(res.Executed) != 2 || res.StoppedAt != "watch" {
		t.Fatalf("execute: %+v %v", res, err)
	}
	// watch has no hook: manual completion (no approval gate on monitor steps).
	if err := c.CompleteStep("watch", "", "SLOs green for 24h"); err != nil {
		t.Fatal(err)
	}
	if c.State.Status != "completed" {
		t.Errorf("status %s", c.State.Status)
	}
	// Decommission is never executed; recording it needs the approval prefix.
	if err := c.CompleteStep("delete-source", "", "done in console"); !errors.Is(err, ErrRefused) {
		t.Fatalf("approval-gated complete-step without --approve: %v", err)
	}
	rb, err := c.Rollback(context.Background(), ExecGates{Approve: good.Approve, ConfirmTarget: "production", ApproveHooks: good.ApproveHooks})
	if err != nil {
		t.Fatal(err)
	}
	if len(rb.RolledBack) != 2 || len(rb.Irreversible) != 1 || len(rb.ManualRequired) != 1 || rb.Status != "rollback-incomplete" {
		t.Fatalf("rollback: %+v", rb)
	}
	wantRan := []string{"./shift.sh 10", "./freeze.sh", "./promote.sh", "./unfreeze.sh", "./shift.sh 0"}
	if strings.Join(ran, "|") != strings.Join(wantRan, "|") {
		t.Fatalf("hook order: %v", ran)
	}
	audit, _ := os.ReadFile(filepath.Join(StateDir(work, p.Metadata.PlanID), "audit.jsonl"))
	if n := strings.Count(string(audit), "\n"); n < 10 || !strings.Contains(string(audit), `"action":"refused"`) {
		t.Fatalf("audit log (%d lines):\n%s", n, audit)
	}
	// A hook for a decommission step is rejected at load.
	_ = os.WriteFile(hooksPath, []byte("apiVersion: hermes/v1alpha1\nkind: CutoverHooks\nhooks:\n  delete-source: {run: [\"./rm.sh\"]}\n"), 0o600)
	if _, err := LoadHooks(hooksPath, p); err == nil || !strings.Contains(err.Error(), "never executes decommission") {
		t.Fatalf("decommission hook must be rejected: %v", err)
	}
}

func TestCatalogMarkdownGolden(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	got := CatalogMarkdown(c, Targets)
	golden := filepath.Join("testdata", "catalog.golden.md")
	if os.Getenv("HERMES_UPDATE_GOLDEN") == "1" {
		_ = os.WriteFile(golden, []byte(got), 0o600)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("catalog markdown differs from %s (regenerate with HERMES_UPDATE_GOLDEN=1)\n%s", golden, got)
	}
	if !strings.HasPrefix(got, "<!-- Generated by `hermes migrate catalog --format markdown`; do not edit. -->") {
		t.Error("missing generated header")
	}
}
