package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const cutoverHooksYAML = `apiVersion: hermes/v1alpha1
kind: CutoverHooks
hooks:
  shift-10: {run: ["./shift.sh", "10"], rollback: ["./shift.sh", "0"]}
`

// exitErr mimics *exec.ExitError for the fake runner.
type exitErr struct{ code int }

func (e exitErr) Error() string { return "exit status " + string(rune('0'+e.code)) }
func (e exitErr) ExitCode() int { return e.code }

func cutoverFixture(t *testing.T) (p *Plan, work, hooksPath string, now time.Time, gates ExecGates) {
	t.Helper()
	p = cutoverPlan()
	work = t.TempDir()
	hooksPath = filepath.Join(work, "hooks.yaml")
	if err := os.WriteFile(hooksPath, []byte(cutoverHooksYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	sum := sha256.Sum256([]byte(cutoverHooksYAML))
	gates = ExecGates{Report: passingReport(p, now.Add(-time.Hour)), MaxReportAge: 4 * time.Hour, Approve: p.Metadata.PlanID[:8], ConfirmTarget: "production",
		ApproveHooks: hex.EncodeToString(sum[:])[:12]}
	return p, work, hooksPath, now, gates
}

func openCutover(t *testing.T, p *Plan, work, hooksPath string, now time.Time, runner HookRunner, term io.Writer) *Controller {
	t.Helper()
	c, err := OpenController(p, work)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hooks, err = LoadHooks(hooksPath, p); err != nil {
		t.Fatal(err)
	}
	c.Now, c.User, c.Runner, c.Output = func() time.Time { return now }, "tester", runner, term
	return c
}

// Hook output may contain secrets: it must reach the terminal and never state.json or audit.jsonl.
func TestCutoverHookOutputIsNeverPersisted(t *testing.T) {
	p, work, hooksPath, now, gates := cutoverFixture(t)
	const leak = "token=demo-not-a-secret-7f3a"
	output := "shifting traffic\n" + leak + "\n"
	var term bytes.Buffer
	fail := false
	runner := func(_ context.Context, _ []string, _ string, _ []string, out io.Writer) error {
		_, _ = io.WriteString(out, output)
		if fail {
			return exitErr{code: 3}
		}
		return nil
	}
	c := openCutover(t, p, work, hooksPath, now, runner, &term)
	if err := c.RecordDryRun(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Execute(context.Background(), gates, nil, "shift-10"); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(output))
	rec := c.State.Steps["shift-10"].Hook
	if rec == nil || rec.ExitCode != 0 || rec.OutputBytes != int64(len(output)) || rec.OutputSHA256 != hex.EncodeToString(sum[:]) || rec.Action != "run" {
		t.Fatalf("hook record: %+v", rec)
	}
	// F-12: rollback with hooks also needs the hooks approval.
	if _, err := c.Rollback(context.Background(), ExecGates{Approve: gates.Approve, ConfirmTarget: "production"}); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "--approve-hooks") {
		t.Fatalf("rollback without --approve-hooks must be refused: %v", err)
	}
	// Rollback, then a failing re-execution: both paths must keep the output out of the files too.
	if _, err := c.Rollback(context.Background(), ExecGates{Approve: gates.Approve, ConfirmTarget: "production", ApproveHooks: gates.ApproveHooks}); err != nil {
		t.Fatal(err)
	}
	fail = true
	c.State.Steps["shift-10"].Status = StepPending
	if _, err := c.Execute(context.Background(), gates, nil, "shift-10"); err == nil {
		t.Fatal("failing hook must fail the step")
	}
	if st := c.State.Steps["shift-10"]; st.Status != StepFailed || st.Hook == nil || st.Hook.ExitCode != 3 {
		t.Fatalf("failed step must record the exit code: %+v %+v", st, st.Hook)
	}
	if got := strings.Count(term.String(), leak); got != 3 {
		t.Errorf("hook output must be streamed to the terminal on every run (want 3, got %d):\n%s", got, term.String())
	}
	for _, f := range []string{"state.json", "audit.jsonl"} {
		data, err := os.ReadFile(filepath.Join(c.Dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), leak) || strings.Contains(string(data), "shifting traffic") {
			t.Errorf("%s persists hook output:\n%s", f, data)
		}
		if !strings.Contains(string(data), `"output_sha256": "`+hex.EncodeToString(sum[:])) && !strings.Contains(string(data), `"output_sha256":"`+hex.EncodeToString(sum[:])) {
			t.Errorf("%s must record the output digest:\n%s", f, data)
		}
	}
}

// The hooks file is bound to the reviewed dry-run.
func TestCutoverRefusesHooksChangedSinceDryRun(t *testing.T) {
	p, work, hooksPath, now, gates := cutoverFixture(t)
	var ran []string
	runner := func(_ context.Context, argv []string, _ string, _ []string, _ io.Writer) error {
		ran = append(ran, strings.Join(argv, " "))
		return nil
	}
	// No dry-run recorded yet: refused.
	c := openCutover(t, p, work, hooksPath, now, runner, io.Discard)
	if _, err := c.Execute(context.Background(), gates, nil, ""); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "hooks-file") {
		t.Fatalf("execute without a dry-run must be refused: %v", err)
	}
	// Dry-run without hooks, then execute with hooks: refused.
	c.Hooks = nil
	if err := c.RecordDryRun(); err != nil {
		t.Fatal(err)
	}
	c = openCutover(t, p, work, hooksPath, now, runner, io.Discard)
	if _, err := c.Execute(context.Background(), gates, nil, ""); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "dry-run without --hooks") {
		t.Fatalf("hooks added after the dry-run must be refused: %v", err)
	}
	// Dry-run with the reviewed file, then the file is swapped: refused, nothing runs.
	if err := c.RecordDryRun(); err != nil {
		t.Fatal(err)
	}
	swapped := strings.Replace(cutoverHooksYAML, `"./shift.sh", "10"`, `"./exfiltrate.sh"`, 1)
	if err := os.WriteFile(hooksPath, []byte(swapped), 0o600); err != nil {
		t.Fatal(err)
	}
	c = openCutover(t, p, work, hooksPath, now, runner, io.Discard)
	_, err := c.Execute(context.Background(), gates, nil, "")
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "re-run the dry-run") || !strings.Contains(err.Error(), "re-approve") {
		t.Fatalf("changed hooks file must be refused with re-run guidance: %v", err)
	}
	if _, err := c.Rollback(context.Background(), ExecGates{Approve: gates.Approve, ConfirmTarget: "production"}); !errors.Is(err, ErrRefused) {
		t.Fatalf("rollback with a changed hooks file must be refused: %v", err)
	}
	if len(ran) != 0 {
		t.Fatalf("hooks ran despite the digest mismatch: %v", ran)
	}
	// F-12: re-running the dry-run on the new file is NOT enough: the old approval (plan id and
	// the OLD hooks digest) is refused; the new digest must be approved explicitly.
	if err := c.RecordDryRun(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Execute(context.Background(), gates, nil, "shift-10"); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "--approve-hooks") || len(ran) != 0 {
		t.Fatalf("the previous approval must not cover a changed hooks file: %v %v", err, ran)
	}
	for _, bad := range []string{"", "1234567", "not-hex-at-all"} {
		g := gates
		g.ApproveHooks = bad
		if _, err := c.Execute(context.Background(), g, nil, "shift-10"); !errors.Is(err, ErrRefused) {
			t.Errorf("--approve-hooks %q must be refused: %v", bad, err)
		}
	}
	newSum := sha256.Sum256([]byte(swapped))
	approved := gates
	approved.ApproveHooks = strings.ToUpper(hex.EncodeToString(newSum[:])[:8]) // case-insensitive
	if res, err := c.Execute(context.Background(), approved, nil, "shift-10"); err != nil || len(res.Executed) != 1 || ran[0] != "./exfiltrate.sh" {
		t.Fatalf("execute after re-review and re-approval: %+v %v %v", res, err, ran)
	}
}
