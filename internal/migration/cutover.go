package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/approval"
	"github.com/nityam02/hermes-cloud-foundation/internal/yamlsafe"
)

// Cutover is a gated, persisted state machine over the plan's cutover, monitor and
// decommission steps. Hermes has no built-in action that touches DNS, databases or source
// resources: executing a step means running the hook argv the user declared for it.
// Decommission steps are never executed, only recorded.
//
// Hook output is streamed to the operator's terminal and never persisted: state.json and
// audit.jsonl keep only the exit code, duration, output size and the output's sha256.
// The hooks file is bound to the reviewed dry-run: the dry-run records its sha256, and
// --execute/--rollback refuse a hooks file whose digest differs.

// Step states.
const (
	StepPending    = "pending"
	StepDone       = "done"
	StepFailed     = "failed"
	StepRolledBack = "rolled-back"
)

// CutoverPhases are the plan phases the cutover state machine tracks.
var CutoverPhases = []string{"cutover", "monitor", "decommission"}

// Hooks is a CutoverHooks document.
type Hooks struct {
	APIVersion string          `yaml:"apiVersion" json:"apiVersion"`
	Kind       string          `yaml:"kind" json:"kind"`
	Hooks      map[string]Hook `yaml:"hooks" json:"hooks"`
	dir        string
	digest     string
}

// Digest is the sha256 (hex) of the hooks file bytes that were parsed.
func (h *Hooks) Digest() string { return h.digest }

// Hook declares the commands for one plan step (argv lists, never shell strings).
type Hook struct {
	Run            []string `yaml:"run" json:"run"`
	Rollback       []string `yaml:"rollback,omitempty" json:"rollback,omitempty"`
	TimeoutSeconds int      `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
	Dir            string   `yaml:"dir,omitempty" json:"dir,omitempty"`
}

// LoadHooks reads a hooks file and checks every hook refers to a plan step.
func LoadHooks(path string, p *Plan) (*Hooks, error) {
	data, err := yamlsafe.ReadFile(path, yamlsafe.MaxBytes)
	if err != nil {
		return nil, fmt.Errorf("hooks: %w", err)
	}
	var h Hooks
	if err := yamlsafe.DecodeStrict(data, yamlsafe.MaxBytes, &h); err != nil {
		return nil, fmt.Errorf("hooks %s: %w", path, err)
	}
	if h.APIVersion != APIVersion || h.Kind != "CutoverHooks" {
		return nil, fmt.Errorf("hooks %s: apiVersion/kind must be %s/CutoverHooks", path, APIVersion)
	}
	h.dir = filepath.Dir(path)
	sum := sha256.Sum256(data)
	h.digest = hex.EncodeToString(sum[:])
	for id, hk := range h.Hooks {
		st := p.Step(id)
		switch {
		case st == nil:
			return nil, fmt.Errorf("hooks %s: step %q is not in the plan", path, id)
		case st.Phase == "decommission":
			return nil, fmt.Errorf("hooks %s: step %q is a decommission step; Hermes never executes decommission", path, id)
		case !oneOf(st.Phase, CutoverPhases):
			return nil, fmt.Errorf("hooks %s: step %q is in phase %s; hooks are only for cutover and monitor steps", path, id, st.Phase)
		case len(hk.Run) == 0:
			return nil, fmt.Errorf("hooks %s: step %q has an empty run argv", path, id)
		}
	}
	return &h, nil
}

// CutoverState is persisted in .hermes/migrations/<plan_id[:12]>/state.json.
type CutoverState struct {
	PlanID    string `json:"plan_id"`
	Target    string `json:"target_environment"`
	Status    string `json:"status"` // planned | in-progress | completed | failed | rolled-back | rollback-incomplete
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	// DryRunAt and HooksSHA256 record the last dry-run and the digest of the hooks file it
	// showed ("" = no hooks file); --execute and --rollback refuse a different hooks file.
	DryRunAt    string                `json:"dry_run_at,omitempty"`
	HooksSHA256 string                `json:"hooks_sha256,omitempty"`
	Steps       map[string]*StepState `json:"steps"`
}

// StepState is the recorded state of one step.
type StepState struct {
	Status      string      `json:"status"`
	By          string      `json:"by,omitempty"` // hook | manual
	User        string      `json:"user,omitempty"`
	CompletedAt string      `json:"completed_at,omitempty"`
	Detail      string      `json:"detail,omitempty"` // manual note or hook error; never hook output
	Hook        *HookRecord `json:"hook,omitempty"`
}

// HookRecord is what is persisted about one hook run. The output itself is only streamed
// to the terminal (it may contain secrets); its size and digest let an operator match a
// saved terminal log to the record.
type HookRecord struct {
	Action       string `json:"action"` // run | rollback
	ExitCode     int    `json:"exit_code"`
	DurationMS   int64  `json:"duration_ms"`
	OutputBytes  int64  `json:"output_bytes"`
	OutputSHA256 string `json:"output_sha256"`
}

// AuditEntry is one line of audit.jsonl (append-only).
type AuditEntry struct {
	Time   string      `json:"time"`
	User   string      `json:"user"`
	PlanID string      `json:"plan_id"`
	Action string      `json:"action"` // dry-run | execute-step | complete-step | rollback-step | refused | status
	Step   string      `json:"step,omitempty"`
	Result string      `json:"result"`
	Detail string      `json:"detail,omitempty"`
	Hook   *HookRecord `json:"hook,omitempty"`
}

// HookRunner runs an argv, writing its combined stdout/stderr to out; replaced in tests.
// A non-nil error that has an ExitCode() int method (such as *exec.ExitError) reports the
// hook's exit code.
type HookRunner func(ctx context.Context, argv []string, dir string, env []string, out io.Writer) error

// DefaultHookRunner executes argv directly (no shell). The hook inherits the operator's
// environment (it needs the credentials of the tools it calls) plus the HERMES_* variables.
func DefaultHookRunner(ctx context.Context, argv []string, dir string, env []string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// Controller drives the cutover for one plan.
type Controller struct {
	Plan   *Plan
	Dir    string // state directory
	Hooks  *Hooks
	Runner HookRunner
	Output io.Writer // terminal that receives hook output (nil discards it); never persisted
	Now    func() time.Time
	User   string
	State  *CutoverState
}

// StateDir returns <workdir>/.hermes/migrations/<plan_id[:12]>.
func StateDir(workdir, planID string) string {
	return filepath.Join(workdir, ".hermes", "migrations", short(planID))
}

// OpenController loads or initializes the state for the plan.
func OpenController(p *Plan, workdir string) (*Controller, error) {
	c := &Controller{Plan: p, Dir: StateDir(workdir, p.Metadata.PlanID), Runner: DefaultHookRunner, Now: time.Now, User: currentUser()}
	data, err := os.ReadFile(filepath.Join(c.Dir, "state.json"))
	switch {
	case err == nil:
		var st CutoverState
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, fmt.Errorf("cutover state: %w", err)
		}
		if st.PlanID != p.Metadata.PlanID {
			return nil, fmt.Errorf("cutover state in %s belongs to plan %s, not %s", c.Dir, short(st.PlanID), short(p.Metadata.PlanID))
		}
		c.State = &st
	case errors.Is(err, os.ErrNotExist):
		now := c.Now().UTC().Format(time.RFC3339)
		c.State = &CutoverState{PlanID: p.Metadata.PlanID, Target: p.Metadata.Target.Environment, Status: "planned", CreatedAt: now, UpdatedAt: now, Steps: map[string]*StepState{}}
	default:
		return nil, fmt.Errorf("cutover state: %w", err)
	}
	for _, s := range c.ScopeSteps() {
		if c.State.Steps[s.ID] == nil {
			c.State.Steps[s.ID] = &StepState{Status: StepPending}
		}
	}
	return c, nil
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

// ScopeSteps returns the cutover, monitor and decommission steps in plan order.
func (c *Controller) ScopeSteps() []PlanStep {
	var out []PlanStep
	for _, s := range c.Plan.Steps() {
		if oneOf(s.Phase, CutoverPhases) {
			out = append(out, s)
		}
	}
	return out
}

func (c *Controller) save() error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	c.State.UpdatedAt = c.Now().UTC().Format(time.RFC3339)
	b, err := json.MarshalIndent(c.State, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(c.Dir, "state.json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(c.Dir, "state.json"))
}

// Audit appends an entry to audit.jsonl.
func (c *Controller) Audit(action, step, result, detail string) error {
	return c.audit(AuditEntry{Action: action, Step: step, Result: result, Detail: detail})
}

func (c *Controller) audit(e AuditEntry) error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(c.Dir, "audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	e.Time, e.User, e.PlanID = c.Now().UTC().Format(time.RFC3339), c.User, c.Plan.Metadata.PlanID
	b, _ := json.Marshal(e)
	_, err = f.Write(append(b, '\n'))
	return err
}

// GateStatus is one execution precondition.
type GateStatus struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// ExecGates are the inputs for --execute and --rollback.
type ExecGates struct {
	Report        *Report
	MaxReportAge  time.Duration
	Approve       string // >= 8-char plan_id prefix
	ConfirmTarget string
	// ApproveHooks is a >= 8-char prefix of the hooks file sha256 printed by the dry-run. It is
	// required whenever a hooks file is used, so changing the hooks needs a new, explicit
	// approval of the new digest (F-12), not only a new dry-run. Scripts invoked by the hooks
	// are not hashed: only the hooks file (the argv lists) is.
	ApproveHooks string
}

// CheckGates evaluates the execution gates (all must pass).
func (c *Controller) CheckGates(g ExecGates, needReport bool) []GateStatus {
	id := c.Plan.Metadata.PlanID
	var out []GateStatus
	if needReport {
		rs := GateStatus{Name: "validation-report"}
		switch r := g.Report; {
		case r == nil:
			rs.Detail = "no validation report (--report)"
		case r.Metadata.PlanID != id:
			rs.Detail = fmt.Sprintf("report is for plan %s, not %s", short(r.Metadata.PlanID), short(id))
		case !r.Recomputed().Passed:
			// The top-level passed/summary fields are never trusted (F-10): they are recomputed
			// from the per-check results, and every check must have passed.
			sum := r.Recomputed()
			rs.Detail = fmt.Sprintf("report did not pass: %d/%d checks passed (%d failed, %d errors), recomputed from its results", sum.Summary.Passed, sum.Summary.Total, sum.Summary.Failed, sum.Summary.Errors)
		default:
			fin, err := time.Parse(time.RFC3339, r.Metadata.FinishedAt)
			age := c.Now().Sub(fin)
			switch {
			case err != nil:
				rs.Detail = "report finished_at is not a timestamp"
			case age > g.MaxReportAge:
				rs.Detail = fmt.Sprintf("report is %s old; maximum is %s (re-run hermes migrate validate)", age.Round(time.Minute), g.MaxReportAge)
			case age < -5*time.Minute:
				rs.Detail = "report finished_at is in the future"
			default:
				n := len(r.Results)
				rs.OK, rs.Detail = true, fmt.Sprintf("passed %d/%d checks, %s old", n, n, age.Round(time.Second))
			}
		}
		out = append(out, rs)
	}
	ap := GateStatus{Name: "approval"}
	switch err := approval.Check(id, g.Approve); {
	case errors.Is(err, approval.ErrMismatch):
		ap.Detail = "--approve does not match the plan id"
	case err != nil:
		ap.Detail = "--approve <plan-id-prefix> (at least 8 hexadecimal characters) is required"
	default:
		ap.OK, ap.Detail = true, "plan "+short(id)+" approved"
	}
	out = append(out, ap)
	ct := GateStatus{Name: "confirm-target"}
	if g.ConfirmTarget == c.Plan.Metadata.Target.Environment && g.ConfirmTarget != "" {
		ct.OK, ct.Detail = true, "target environment "+g.ConfirmTarget+" confirmed"
	} else {
		ct.Detail = fmt.Sprintf("--confirm-target %s is required", c.Plan.Metadata.Target.Environment)
	}
	out = append(out, ct)
	if c.Hooks != nil {
		out = append(out, c.hooksGate(g.ApproveHooks))
	}
	return out
}

// hooksGate binds the hooks file to the reviewed dry-run: the commands about to run must be
// byte-for-byte the ones the dry-run showed before the operator approved.
func (c *Controller) hooksGate(approveHooks string) GateStatus {
	gs := GateStatus{Name: "hooks-file"}
	const redo = "re-run the dry-run (hermes migrate cutover without --execute/--rollback), review the hooks, then re-approve with --approve-hooks <prefix of the printed sha256>"
	d := c.Hooks.Digest()
	herr := approval.Check(d, approveHooks)
	switch {
	case c.State.DryRunAt == "":
		gs.Detail = "no dry-run recorded for this plan; " + redo
	case d != c.State.HooksSHA256:
		recorded := "none (dry-run without --hooks)"
		if c.State.HooksSHA256 != "" {
			recorded = short(c.State.HooksSHA256)
		}
		gs.Detail = fmt.Sprintf("hooks file sha256 %s differs from the one recorded at the dry-run of %s (%s); %s", short(d), c.State.DryRunAt, recorded, redo)
	case herr != nil && !errors.Is(herr, approval.ErrMismatch):
		gs.Detail = fmt.Sprintf("--approve-hooks <at least 8 hex characters of the hooks sha256 %s printed by the dry-run> is required", short(d))
	case herr != nil:
		gs.Detail = fmt.Sprintf("--approve-hooks %s does not match the hooks file sha256 %s; %s", approval.Normalize(approveHooks), short(d), redo)
	default:
		gs.OK, gs.Detail = true, fmt.Sprintf("hooks file sha256 %s matches the dry-run of %s and is approved", short(d), c.State.DryRunAt)
	}
	return gs
}

// RecordDryRun records that the runbook was shown, with the digest of the hooks file it
// showed, and persists the state. Only a later --execute/--rollback with the same hooks file
// passes the hooks-file gate.
func (c *Controller) RecordDryRun() error {
	c.State.DryRunAt = c.Now().UTC().Format(time.RFC3339)
	c.State.HooksSHA256 = ""
	detail := "no hooks file"
	if c.Hooks != nil {
		c.State.HooksSHA256 = c.Hooks.Digest()
		detail = "hooks sha256 " + c.State.HooksSHA256
	}
	if err := c.save(); err != nil {
		return err
	}
	return c.Audit("dry-run", "", "ok", detail)
}

func gatesOK(gs []GateStatus) (bool, string) {
	var failed []string
	for _, g := range gs {
		if !g.OK {
			failed = append(failed, g.Name+": "+g.Detail)
		}
	}
	return len(failed) == 0, strings.Join(failed, "; ")
}

// StepView describes a step for the runbook display.
type StepView struct {
	ID           string   `json:"id"`
	Phase        string   `json:"phase"`
	Title        string   `json:"title"`
	Gate         string   `json:"gate"`
	SourceImpact string   `json:"source_impact"`
	Reversible   bool     `json:"reversible"`
	Status       string   `json:"status"`
	Action       string   `json:"action"`
	DependsOn    []string `json:"depends_on"`
	Rollback     string   `json:"rollback"`
}

// Runbook returns the cutover runbook with each step's state and what --execute would do.
func (c *Controller) Runbook() []StepView {
	var out []StepView
	for _, s := range c.ScopeSteps() {
		v := StepView{ID: s.ID, Phase: s.Phase, Title: s.Title, Gate: s.Gate, SourceImpact: s.SourceImpact, Reversible: s.Reversible,
			Status: c.State.Steps[s.ID].Status, DependsOn: s.DependsOn, Rollback: s.Rollback}
		switch {
		case s.Phase == "decommission":
			v.Action = "never executed by Hermes; record with --complete-step after doing it through the owning tool"
		case c.hook(s.ID) != nil:
			v.Action = "run declared hook: " + strings.Join(c.hook(s.ID).Run, " ")
		default:
			v.Action = "manual; record with --complete-step " + s.ID
		}
		if SourceImpactGated(s.SourceImpact) && s.Phase != "decommission" {
			v.Action += " (needs --approve-source-step " + s.ID + ")"
		}
		out = append(out, v)
	}
	return out
}

func (c *Controller) hook(id string) *Hook {
	if c.Hooks == nil {
		return nil
	}
	if h, ok := c.Hooks.Hooks[id]; ok {
		return &h
	}
	return nil
}

// depsDone reports unmet in-scope dependencies (steps outside cutover/monitor/decommission
// are covered by the validation report gate).
func (c *Controller) unmetDeps(s PlanStep) []string {
	var out []string
	for _, d := range s.DependsOn {
		if st, ok := c.State.Steps[d]; ok && st.Status != StepDone {
			out = append(out, d)
		}
	}
	return out
}

// ExecResult summarizes an execute call.
type ExecResult struct {
	Executed  []string `json:"executed"`
	StoppedAt string   `json:"stopped_at,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Status    string   `json:"status"`
}

// ErrRefused marks gate refusals (CLI exit code 2).
var ErrRefused = errors.New("refused")

// Execute runs declared hooks for pending cutover/monitor steps in plan order, stopping at
// the first manual step, unmet dependency, missing source-step approval or failure.
func (c *Controller) Execute(ctx context.Context, g ExecGates, approveSource []string, only string) (*ExecResult, error) {
	gs := c.CheckGates(g, true)
	if ok, why := gatesOK(gs); !ok {
		_ = c.Audit("refused", only, "refused", why)
		return nil, fmt.Errorf("%w: %s", ErrRefused, why)
	}
	res := &ExecResult{Executed: []string{}}
	c.State.Status = "in-progress"
	defer func() { _ = c.save() }()
	for _, s := range c.ScopeSteps() {
		if only != "" && s.ID != only {
			continue
		}
		st := c.State.Steps[s.ID]
		if st.Status == StepDone {
			continue
		}
		if s.Phase == "decommission" {
			res.StoppedAt, res.Reason = s.ID, "decommission steps are never executed by Hermes; record them with --complete-step"
			break
		}
		if un := c.unmetDeps(s); len(un) > 0 {
			res.StoppedAt, res.Reason = s.ID, "waiting for "+strings.Join(un, ", ")
			break
		}
		h := c.hook(s.ID)
		if h == nil {
			res.StoppedAt, res.Reason = s.ID, "manual step: perform it, then record it with --complete-step "+s.ID
			break
		}
		if SourceImpactGated(s.SourceImpact) && !oneOf(s.ID, approveSource) {
			_ = c.Audit("refused", s.ID, "refused", "source-impacting step without --approve-source-step")
			return res, fmt.Errorf("%w: step %s has source_impact %s and needs --approve-source-step %s", ErrRefused, s.ID, s.SourceImpact, s.ID)
		}
		rec, err := c.runHook(ctx, s.ID, h, h.Run, "run")
		if err != nil {
			*st = StepState{Status: StepFailed, By: "hook", User: c.User, Detail: err.Error(), Hook: rec}
			c.State.Status = "failed"
			_ = c.audit(AuditEntry{Action: "execute-step", Step: s.ID, Result: "failed", Detail: err.Error(), Hook: rec})
			return res, fmt.Errorf("step %s failed: %w (inspect the hook output above, fix and re-run, or use --rollback)", s.ID, err)
		}
		*st = StepState{Status: StepDone, By: "hook", User: c.User, CompletedAt: c.Now().UTC().Format(time.RFC3339), Hook: rec}
		_ = c.audit(AuditEntry{Action: "execute-step", Step: s.ID, Result: "done", Hook: rec})
		res.Executed = append(res.Executed, s.ID)
		if only != "" {
			break
		}
	}
	c.updateStatus()
	res.Status = c.State.Status
	return res, nil
}

// runHook runs one hook, streaming its output to c.Output only, and returns what may be
// persisted about it (never the output itself).
func (c *Controller) runHook(ctx context.Context, id string, h *Hook, argv []string, action string) (*HookRecord, error) {
	timeout := time.Duration(h.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dir := h.Dir
	if dir == "" || !filepath.IsAbs(dir) {
		dir = filepath.Join(c.Hooks.dir, dir)
	}
	term := c.Output
	if term == nil {
		term = io.Discard
	}
	fmt.Fprintf(term, "==> %s %s: %s\n", action, id, strings.Join(argv, " "))
	digest := &countingHash{h: sha256.New()}
	start := time.Now()
	err := c.Runner(cctx, argv, dir, []string{"HERMES_PLAN_ID=" + c.Plan.Metadata.PlanID, "HERMES_STEP_ID=" + id, "HERMES_ACTION=" + action}, io.MultiWriter(term, digest))
	rec := &HookRecord{Action: action, ExitCode: exitCode(err), DurationMS: time.Since(start).Milliseconds(),
		OutputBytes: digest.n, OutputSHA256: hex.EncodeToString(digest.h.Sum(nil))}
	return rec, err
}

// countingHash hashes and counts what is written to it.
type countingHash struct {
	h hash.Hash
	n int64
}

func (w *countingHash) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return w.h.Write(p)
}

// exitCode is 0 on success, the process exit code when known, and -1 otherwise (e.g. the
// command was not found or was killed by the timeout).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) && ec.ExitCode() >= 0 {
		return ec.ExitCode()
	}
	return -1
}

func (c *Controller) updateStatus() {
	all := true
	for _, s := range c.ScopeSteps() {
		if s.Phase != "decommission" && c.State.Steps[s.ID].Status != StepDone {
			all = false
		}
	}
	if all {
		c.State.Status = "completed"
	}
}

// CompleteStep records a manually performed step. Approval-gated steps need the plan
// approval prefix so the record is attributable.
func (c *Controller) CompleteStep(id, approve, note string) error {
	s := c.Plan.Step(id)
	if s == nil {
		return fmt.Errorf("step %q is not in the plan", id)
	}
	if s.Gate == "approval" && approval.Check(c.Plan.Metadata.PlanID, approve) != nil {
		_ = c.Audit("refused", id, "refused", "complete-step of an approval-gated step without --approve")
		return fmt.Errorf("%w: step %s has an approval gate; pass --approve <plan-id-prefix>", ErrRefused, id)
	}
	if un := c.unmetDeps(*s); len(un) > 0 {
		return fmt.Errorf("%w: step %s depends on steps that are not done: %s", ErrRefused, id, strings.Join(un, ", "))
	}
	c.State.Steps[id] = &StepState{Status: StepDone, By: "manual", User: c.User, CompletedAt: c.Now().UTC().Format(time.RFC3339), Detail: note}
	if c.State.Status == "planned" {
		c.State.Status = "in-progress"
	}
	c.updateStatus()
	if err := c.save(); err != nil {
		return err
	}
	return c.Audit("complete-step", id, "done", note)
}

// RollbackResult lists what rollback did.
type RollbackResult struct {
	RolledBack     []string `json:"rolled_back"`
	ManualRequired []string `json:"manual_required"`
	Irreversible   []string `json:"irreversible"`
	Status         string   `json:"status"`
}

// Rollback runs declared rollback hooks for completed reversible steps (reverse order), lists
// reversible steps without a rollback hook as manual, and lists irreversible ones.
func (c *Controller) Rollback(ctx context.Context, g ExecGates) (*RollbackResult, error) {
	if ok, why := gatesOK(c.CheckGates(g, false)); !ok {
		_ = c.Audit("refused", "", "refused", "rollback: "+why)
		return nil, fmt.Errorf("%w: %s", ErrRefused, why)
	}
	res := &RollbackResult{RolledBack: []string{}, ManualRequired: []string{}, Irreversible: []string{}}
	steps := c.ScopeSteps()
	defer func() { _ = c.save() }()
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		st := c.State.Steps[s.ID]
		if st.Status != StepDone || s.Phase == "decommission" {
			continue
		}
		if !s.Reversible {
			res.Irreversible = append(res.Irreversible, s.ID+": "+s.Rollback)
			_ = c.Audit("rollback-step", s.ID, "irreversible", s.Rollback)
			continue
		}
		h := c.hook(s.ID)
		if h == nil || len(h.Rollback) == 0 {
			res.ManualRequired = append(res.ManualRequired, s.ID+": "+s.Rollback)
			_ = c.Audit("rollback-step", s.ID, "manual-required", s.Rollback)
			continue
		}
		rec, err := c.runHook(ctx, s.ID, h, h.Rollback, "rollback")
		if err != nil {
			_ = c.audit(AuditEntry{Action: "rollback-step", Step: s.ID, Result: "failed", Detail: err.Error(), Hook: rec})
			c.State.Status = "rollback-incomplete"
			return res, fmt.Errorf("rollback of %s failed: %w (inspect the hook output above)", s.ID, err)
		}
		*st = StepState{Status: StepRolledBack, By: "hook", User: c.User, CompletedAt: c.Now().UTC().Format(time.RFC3339), Hook: rec}
		_ = c.audit(AuditEntry{Action: "rollback-step", Step: s.ID, Result: "rolled-back", Hook: rec})
		res.RolledBack = append(res.RolledBack, s.ID)
	}
	if len(res.ManualRequired) == 0 && len(res.Irreversible) == 0 {
		c.State.Status = "rolled-back"
	} else {
		c.State.Status = "rollback-incomplete"
	}
	res.Status = c.State.Status
	return res, nil
}

// Save persists the state (used after dry-runs to create the state directory).
func (c *Controller) Save() error { return c.save() }
