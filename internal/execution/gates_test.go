package execution_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution/enginetest"
)

var t0 = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func plan(t *testing.T, f *enginetest.Fake, kind string, protected bool, policy *execution.PolicyRecord) *execution.SavedPlan {
	t.Helper()
	dir := t.TempDir()
	req := execution.PlanRequest{
		Kind: kind, RootDir: dir, PlanDir: filepath.Join(dir, "plans"),
		Meta: execution.PlanMeta{Project: "demo", Environment: "dev", Provider: "aws", Protected: protected, RenderDigest: "d1", ModulesDigest: "m1", User: "tester"},
		Now:  func() time.Time { return t0 },
	}
	if policy != nil {
		req.Policy = func(context.Context, string) (execution.PolicyRecord, error) { return *policy, nil }
	}
	sp, err := execution.CreatePlan(context.Background(), f.Engine("tofu"), req)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func base(sp *execution.SavedPlan) execution.ApplyRequest {
	return execution.ApplyRequest{
		Kind: sp.Meta.Kind, Environment: "dev", Protected: sp.Meta.Protected,
		CurrentDigest: "d1", CurrentModulesDigest: "m1", Yes: true,
		Now: func() time.Time { return t0.Add(time.Hour) },
	}
}

func gateCode(err error) string {
	var ge *execution.GateError
	if errors.As(err, &ge) {
		return ge.Code
	}
	return ""
}

func TestCreatePlanSavesArtifacts(t *testing.T) {
	f := &enginetest.Fake{PlanJSON: enginetest.PlanJSON(map[string][]string{"module.platform.aws_s3_bucket.a": {"create"}})}
	sp := plan(t, f, "", false, nil)
	for _, p := range []string{sp.PlanFile, sp.PlanJSON, filepath.Join(sp.Dir, "meta.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	if len(sp.Meta.PlanID) != 64 || sp.Meta.Summary.Create != 1 || !sp.Meta.HasChanges || sp.Meta.Kind != execution.KindApply {
		t.Errorf("meta: %+v", sp.Meta)
	}
	loaded, err := execution.LoadPlan(sp.Dir)
	if err != nil || loaded.Meta.PlanID != sp.Meta.PlanID || loaded.Meta.EngineVersion != "1.13.0" {
		t.Fatalf("LoadPlan: %v %+v", err, loaded)
	}
	if !f.Called("init -input=false") || !f.Called("plan -input=false -no-color -detailed-exitcode -out=") {
		t.Errorf("calls: %v", f.Calls)
	}
	for _, c := range f.Calls {
		if strings.Contains(c, "-auto-approve") {
			t.Errorf("never pass -auto-approve: %s", c)
		}
	}
}

func TestDestroyPlanUsesDestroyFlag(t *testing.T) {
	f := &enginetest.Fake{PlanJSON: enginetest.PlanJSON(map[string][]string{"a.b": {"delete"}})}
	sp := plan(t, f, execution.KindDestroy, false, nil)
	if !f.Called("plan -input=false -no-color -detailed-exitcode -out=") || !strings.Contains(strings.Join(f.Calls, "|"), "-destroy") {
		t.Errorf("destroy plan must pass -destroy: %v", f.Calls)
	}
	if sp.Meta.Kind != execution.KindDestroy {
		t.Errorf("kind %s", sp.Meta.Kind)
	}
}

func TestApplyGates(t *testing.T) {
	createOnly := enginetest.PlanJSON(map[string][]string{"a.create": {"create"}})
	destructive := enginetest.PlanJSON(map[string][]string{"a.replace": {"delete", "create"}})
	failed := &execution.PolicyRecord{Ran: true, Passed: false, Failures: []string{"HCF-POL-001: public bucket"}}
	cases := []struct {
		name      string
		planJSON  string
		protected bool
		policy    *execution.PolicyRecord
		mutate    func(sp *execution.SavedPlan, r *execution.ApplyRequest)
		want      string // gate code, "" = allowed
	}{
		{"allowed with --yes", createOnly, false, nil, nil, ""},
		{"stale render", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) { r.CurrentDigest = "d2" }, execution.CodeStalePlan},
		{"stale modules", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) { r.CurrentModulesDigest = "m2" }, execution.CodeStalePlan},
		{"expired", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) {
			r.Now = func() time.Time { return t0.Add(25 * time.Hour) }
		}, execution.CodeExpiredPlan},
		{"custom max age", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) { r.MaxAge = 30 * time.Minute }, execution.CodeExpiredPlan},
		// F-04 (QA: created_at 2099 made a plan look fresh forever).
		{"created_at in the future", createOnly, false, nil, func(sp *execution.SavedPlan, _ *execution.ApplyRequest) {
			sp.Meta.CreatedAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
		}, execution.CodeExpiredPlan},
		{"created_at beyond clock skew", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) {
			r.Now = func() time.Time { return t0.Add(-10 * time.Minute) }
		}, execution.CodeExpiredPlan},
		{"small clock skew allowed", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) {
			r.Now = func() time.Time { return t0.Add(-2 * time.Minute) }
		}, ""},
		{"tampered", createOnly, false, nil, func(sp *execution.SavedPlan, _ *execution.ApplyRequest) {
			_ = os.WriteFile(sp.PlanFile, []byte("x"), 0o600)
		}, execution.CodeTamperedPlan},
		{"destructive refused", destructive, false, nil, nil, execution.CodeDestructive},
		{"destructive allowed", destructive, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) { r.AllowDestructive = true }, ""},
		{"policy failed", createOnly, false, failed, nil, execution.CodePolicyFailed},
		{"protected needs approve", createOnly, true, nil, nil, execution.CodeApproval},
		{"protected --yes is not approval", createOnly, true, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) { r.Yes = true }, execution.CodeApproval},
		{"protected short prefix", createOnly, true, nil, func(sp *execution.SavedPlan, r *execution.ApplyRequest) { r.Approve = sp.Meta.PlanID[:7] }, execution.CodeApproval},
		{"protected wrong prefix", createOnly, true, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) { r.Approve = "deadbeefdeadbeef" }, execution.CodeApproval},
		{"protected approved", createOnly, true, nil, func(sp *execution.SavedPlan, r *execution.ApplyRequest) { r.Approve = sp.Meta.PlanID[:8] }, ""},
		// F-20: the same prefix rule everywhere (>= 8 hex characters, case-insensitive, trimmed).
		{"protected approved upper-case", createOnly, true, nil, func(sp *execution.SavedPlan, r *execution.ApplyRequest) {
			r.Approve = " " + strings.ToUpper(sp.Meta.PlanID[:10]) + " "
		}, ""},
		{"protected non-hex prefix", createOnly, true, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) { r.Approve = "zzzzzzzzzz" }, execution.CodeApproval},
		{"non-interactive without yes", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) { r.Yes = false }, execution.CodeDeclined},
		{"interactive declined", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) {
			r.Yes, r.Interactive = false, true
			r.Confirm = func(string) (bool, error) { return false, nil }
		}, execution.CodeDeclined},
		{"interactive accepted", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) {
			r.Yes, r.Interactive = false, true
			r.Confirm = func(string) (bool, error) { return true, nil }
		}, ""},
		{"wrong environment", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) { r.Environment = "staging" }, execution.CodeWrongPlan},
		{"destroy plan via apply", createOnly, false, nil, func(_ *execution.SavedPlan, r *execution.ApplyRequest) { r.Kind = execution.KindDestroy }, execution.CodeWrongPlan},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &enginetest.Fake{PlanJSON: tc.planJSON}
			sp := plan(t, f, "", tc.protected, tc.policy)
			req := base(sp)
			if tc.protected {
				req.Yes = false
			}
			if tc.mutate != nil {
				tc.mutate(sp, &req)
			}
			err := execution.Apply(context.Background(), f.Engine("tofu"), sp.Dir, sp, req)
			if got := gateCode(err); got != tc.want || (tc.want == "" && err != nil) {
				t.Fatalf("gate = %q (err %v), want %q", got, err, tc.want)
			}
			applied := f.Called("apply -input=false -no-color")
			if applied != (tc.want == "") {
				t.Errorf("apply called = %v, want %v", applied, tc.want == "")
			}
		})
	}
}

func TestApplyOnlyOnce(t *testing.T) {
	f := &enginetest.Fake{PlanJSON: enginetest.PlanJSON(map[string][]string{"a.b": {"create"}})}
	sp := plan(t, f, "", false, nil)
	if err := execution.Apply(context.Background(), f.Engine("tofu"), sp.Dir, sp, base(sp)); err != nil {
		t.Fatal(err)
	}
	again, err := execution.LoadPlan(sp.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.Apply(context.Background(), f.Engine("tofu"), sp.Dir, again, base(again)); gateCode(err) != execution.CodeAlreadyDone {
		t.Fatalf("second apply must be refused, got %v", err)
	}
}

func TestDestroyGates(t *testing.T) {
	pj := enginetest.PlanJSON(map[string][]string{"a.b": {"delete"}})
	cases := []struct {
		name      string
		protected bool
		mutate    func(sp *execution.SavedPlan, r *execution.ApplyRequest)
		want      string
	}{
		{"always needs approve", false, nil, execution.CodeApproval},
		{"approved", false, func(sp *execution.SavedPlan, r *execution.ApplyRequest) { r.Approve = sp.Meta.PlanID[:10] }, ""},
		{"protected needs confirm-environment", true, func(sp *execution.SavedPlan, r *execution.ApplyRequest) { r.Approve = sp.Meta.PlanID[:10] }, execution.CodeConfirmEnv},
		{"protected wrong confirm", true, func(sp *execution.SavedPlan, r *execution.ApplyRequest) {
			r.Approve, r.ConfirmEnvironment = sp.Meta.PlanID[:10], "prod"
		}, execution.CodeConfirmEnv},
		{"protected approved", true, func(sp *execution.SavedPlan, r *execution.ApplyRequest) {
			r.Approve, r.ConfirmEnvironment = sp.Meta.PlanID[:10], "dev"
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &enginetest.Fake{PlanJSON: pj}
			sp := plan(t, f, execution.KindDestroy, tc.protected, nil)
			req := base(sp)
			if tc.mutate != nil {
				tc.mutate(sp, &req)
			}
			err := execution.Apply(context.Background(), f.Engine("tofu"), sp.Dir, sp, req)
			if got := gateCode(err); got != tc.want || (tc.want == "" && err != nil) {
				t.Fatalf("gate = %q (err %v), want %q", got, err, tc.want)
			}
		})
	}
}

func TestLoadPlanMissing(t *testing.T) {
	_, err := execution.LoadPlan(t.TempDir())
	if gateCode(err) != execution.CodeNoPlan {
		t.Fatalf("got %v", err)
	}
}

func TestAutoApproveIsRefusedByEngine(t *testing.T) {
	f := &enginetest.Fake{}
	if err := f.Engine("tofu").Run(context.Background(), t.TempDir(), "apply", "-auto-approve"); err == nil {
		t.Fatal("-auto-approve must be refused")
	}
	if f.Called("apply") {
		t.Fatal("the engine must not be invoked with -auto-approve")
	}
}

func TestRefreshOnlyDrift(t *testing.T) {
	f := &enginetest.Fake{Drift: true}
	drift, err := f.Engine("tofu").PlanRefreshOnly(context.Background(), t.TempDir())
	if err != nil || !drift {
		t.Fatalf("drift=%v err=%v", drift, err)
	}
	f.Drift = false
	if drift, _ := f.Engine("tofu").PlanRefreshOnly(context.Background(), t.TempDir()); drift {
		t.Fatal("no drift expected")
	}
}

func TestHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history", "dev.jsonl")
	if evs, err := execution.ReadHistory(path); err != nil || len(evs) != 0 {
		t.Fatalf("empty history: %v %v", evs, err)
	}
	for _, a := range []string{"plan", "apply"} {
		if err := execution.AppendHistory(path, execution.HistoryEvent{Environment: "dev", Action: a, Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := execution.ReadHistory(path)
	if err != nil || len(evs) != 2 || evs[1].Action != "apply" || evs[0].Time.IsZero() {
		t.Fatalf("history: %+v %v", evs, err)
	}
}

func TestWorkspacePaths(t *testing.T) {
	w := execution.Workspace{Dir: ".hermes"}
	if w.BuildDir("dev") != filepath.Join(".hermes", "build", "dev") ||
		w.PlanDir("dev", execution.KindApply) != filepath.Join(".hermes", "plans", "dev") ||
		w.PlanDir("dev", execution.KindDestroy) != filepath.Join(".hermes", "plans", "dev", "destroy") ||
		w.HistoryFile("dev") != filepath.Join(".hermes", "history", "dev.jsonl") {
		t.Fatal("unexpected workspace layout")
	}
}

// F-09: a saved plan applied on a fresh machine (CI apply job with the downloaded plan
// directory) initialises the re-rendered root first; an initialised root is not re-initialised.
func TestApplyInitialisesFreshRoot(t *testing.T) {
	f := &enginetest.Fake{PlanJSON: enginetest.PlanJSON(map[string][]string{"a.b": {"create"}})}
	sp := plan(t, f, "", false, nil)
	fresh := t.TempDir()
	f.Calls = nil
	if err := execution.Apply(context.Background(), f.Engine("tofu"), fresh, sp, base(sp)); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != 2 || !strings.HasPrefix(f.Calls[0], "init ") || !strings.HasPrefix(f.Calls[1], "apply ") {
		t.Fatalf("want init then apply of the saved plan, got %q", f.Calls)
	}

	f2 := &enginetest.Fake{PlanJSON: enginetest.PlanJSON(map[string][]string{"a.b": {"create"}})}
	sp2 := plan(t, f2, "", false, nil)
	ready := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ready, ".terraform"), 0o750); err != nil {
		t.Fatal(err)
	}
	f2.Calls = nil
	if err := execution.Apply(context.Background(), f2.Engine("tofu"), ready, sp2, base(sp2)); err != nil {
		t.Fatal(err)
	}
	if len(f2.Calls) != 1 || !strings.HasPrefix(f2.Calls[0], "apply ") {
		t.Fatalf("an initialised root must not be re-initialised: %q", f2.Calls)
	}
}
