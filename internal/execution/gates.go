package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/approval"
	"github.com/nityam02/hermes-cloud-foundation/internal/tfplan"
)

// Plan kinds.
const (
	KindApply   = "apply"
	KindDestroy = "destroy"
)

// Safety-gate defaults.
const (
	DefaultMaxPlanAge = 24 * time.Hour
	MinApprovalPrefix = approval.MinPrefix
	metaSchemaVersion = 1
)

// Safety-gate diagnostic codes (HCF5xx, documented in docs/reference/diagnostics.md).
const (
	CodeNoPlan        = "HCF501"
	CodeStalePlan     = "HCF502"
	CodeExpiredPlan   = "HCF503"
	CodeTamperedPlan  = "HCF504"
	CodeDestructive   = "HCF505"
	CodeApproval      = "HCF506"
	CodeConfirmEnv    = "HCF507"
	CodePolicyFailed  = "HCF508"
	CodeDeclined      = "HCF509"
	CodeAlreadyDone   = "HCF510"
	CodeWrongPlan     = "HCF511"
	CodePolicyMissing = "HCF512"
)

// GateError is a safety-gate refusal. The CLI maps it to exit code 2.
type GateError struct {
	Code   string
	Reason string
	Hint   string
}

func (e *GateError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Reason, e.Hint)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Reason)
}

func refuse(code, hint, format string, args ...any) *GateError {
	return &GateError{Code: code, Reason: fmt.Sprintf(format, args...), Hint: hint}
}

// Workspace is the .hermes working directory.
type Workspace struct{ Dir string }

// BuildDir is .hermes/build/<env>.
func (w Workspace) BuildDir(env string) string { return filepath.Join(w.Dir, "build", env) }

// PlanDir is .hermes/plans/<env> for apply plans and .hermes/plans/<env>/destroy for
// destroy plans.
func (w Workspace) PlanDir(env, kind string) string {
	if kind == KindDestroy {
		return filepath.Join(w.Dir, "plans", env, "destroy")
	}
	return filepath.Join(w.Dir, "plans", env)
}

// HistoryFile is .hermes/history/<env>.jsonl.
func (w Workspace) HistoryFile(env string) string {
	return filepath.Join(w.Dir, "history", env+".jsonl")
}

// PolicyRecord is the outcome of the policy check recorded with a saved plan.
type PolicyRecord struct {
	Ran      bool     `json:"ran"`
	Passed   bool     `json:"passed"`
	Skipped  bool     `json:"skipped,omitempty"` // --skip-policy
	Tool     string   `json:"tool,omitempty"`    // e.g. "conftest 0.71.1"
	Failures []string `json:"failures,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Note     string   `json:"note,omitempty"`
	// Bundles are the policy directories evaluated: the embedded bundle first (always), then
	// every --policy-dir. DataFiles are the --policy-data files (e.g. exceptions).
	Bundles   []PolicySource `json:"bundles,omitempty"`
	DataFiles []PolicySource `json:"data_files,omitempty"`
	// Exceptions are the policy exceptions (hermes.exceptions in --policy-data files) in effect
	// for this plan; each one is printed with the plan and recorded in history.
	Exceptions []PolicyException `json:"exceptions,omitempty"`
}

// PolicyException is one time-boxed, per-address policy exception.
type PolicyException struct {
	Rule    string `json:"rule"`
	Address string `json:"address"`
	Reason  string `json:"reason"`
	Expires string `json:"expires"` // YYYY-MM-DD
	// InPlan is true when the address is a resource in this plan (the exception can suppress a
	// finding); false means the exception has no effect on this plan.
	InPlan bool   `json:"in_plan"`
	File   string `json:"file"` // the --policy-data file it came from
}

// PolicySource identifies one policy bundle or data file by content digest.
type PolicySource struct {
	Source string `json:"source"` // embedded | policy-dir | policy-data
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Sources lists the bundles and data files of a record (for history).
func (r PolicyRecord) Sources() []PolicySource {
	if len(r.Bundles)+len(r.DataFiles) == 0 {
		return nil
	}
	return append(append([]PolicySource{}, r.Bundles...), r.DataFiles...)
}

// PlanMeta is .hermes/plans/<env>/meta.json. It never contains secrets: only identifiers,
// digests, counts and resource addresses.
type PlanMeta struct {
	SchemaVersion int            `json:"schema_version"`
	PlanID        string         `json:"plan_id"` // sha256 of plan.tfplan
	Kind          string         `json:"kind"`    // apply | destroy
	Project       string         `json:"project"`
	Environment   string         `json:"environment"`
	Provider      string         `json:"provider"`
	Protected     bool           `json:"protected"`
	RenderDigest  string         `json:"render_digest"`
	ModulesDigest string         `json:"modules_digest"`
	Engine        string         `json:"engine"`
	EngineVersion string         `json:"engine_version"`
	CreatedAt     time.Time      `json:"created_at"`
	User          string         `json:"user"`
	HasChanges    bool           `json:"has_changes"`
	Summary       tfplan.Summary `json:"summary"`
	Policy        PolicyRecord   `json:"policy"`
	AppliedAt     *time.Time     `json:"applied_at,omitempty"`
}

// SavedPlan is a plan on disk.
type SavedPlan struct {
	Dir      string
	Meta     PlanMeta
	PlanFile string // plan.tfplan (absolute)
	PlanJSON string // plan.json (absolute)
}

// PlanRequest describes a plan to create.
type PlanRequest struct {
	Kind     string // apply (default) | destroy
	RootDir  string // rendered root module
	PlanDir  string // where plan.tfplan, plan.json and meta.json go
	InitArgs []string
	// Meta carries the identifying fields (project, environment, provider, protected,
	// digests, user); PlanID, Summary, timestamps and engine fields are filled in.
	Meta PlanMeta
	// Policy, when set, evaluates plan.json; its record is stored in meta.json.
	Policy func(ctx context.Context, planJSON string) (PolicyRecord, error)
	Now    func() time.Time
}

// CreatePlan runs init → plan -out → show -json → summarize → policy and saves the plan.
// meta.json is written last, so an interrupted plan never looks applicable.
func CreatePlan(ctx context.Context, eng *Engine, req PlanRequest) (*SavedPlan, error) {
	if req.Kind == "" {
		req.Kind = KindApply
	}
	if req.Now == nil {
		req.Now = time.Now
	}
	planDir, err := filepath.Abs(req.PlanDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(planDir, 0o700); err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	metaPath := filepath.Join(planDir, "meta.json")
	if err := os.Remove(metaPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("plan: %w", err)
	}
	sp := &SavedPlan{Dir: planDir, PlanFile: filepath.Join(planDir, "plan.tfplan"), PlanJSON: filepath.Join(planDir, "plan.json")}
	_ = os.Remove(sp.PlanFile)
	_ = os.Remove(sp.PlanJSON)

	if err := eng.Init(ctx, req.RootDir, req.InitArgs...); err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	var args []string
	if req.Kind == KindDestroy {
		args = append(args, "-destroy")
	}
	if _, err := eng.Plan(ctx, req.RootDir, sp.PlanFile, args...); err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	raw, err := eng.ShowJSON(ctx, req.RootDir, sp.PlanFile)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	parsed, err := tfplan.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	if parsed.Errored {
		return nil, fmt.Errorf("plan: the engine reported an errored plan")
	}
	if err := os.WriteFile(sp.PlanJSON, raw, 0o600); err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	id, err := fileSHA256(sp.PlanFile)
	if err != nil {
		return nil, fmt.Errorf("plan: plan file: %w", err)
	}
	m := req.Meta
	m.SchemaVersion = metaSchemaVersion
	m.PlanID = id
	m.Kind = req.Kind
	m.Engine = eng.Name
	m.EngineVersion = eng.Version.String()
	m.CreatedAt = req.Now().UTC().Truncate(time.Second)
	m.Summary = tfplan.Summarize(parsed)
	m.HasChanges = m.Summary.HasChanges()
	m.AppliedAt = nil
	if req.Policy != nil {
		rec, err := req.Policy(ctx, sp.PlanJSON)
		if err != nil {
			return nil, fmt.Errorf("plan: policy: %w", err)
		}
		m.Policy = rec
	}
	sp.Meta = m
	if err := writeJSON(metaPath, m); err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	return sp, nil
}

// LoadPlan reads the saved plan in planDir.
func LoadPlan(planDir string) (*SavedPlan, error) {
	planDir, err := filepath.Abs(planDir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(planDir, "meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, refuse(CodeNoPlan, "run `hermes plan` first", "no saved plan in %s", planDir)
	}
	if err != nil {
		return nil, err
	}
	var m PlanMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("saved plan: meta.json: %w", err)
	}
	return &SavedPlan{Dir: planDir, Meta: m, PlanFile: filepath.Join(planDir, "plan.tfplan"), PlanJSON: filepath.Join(planDir, "plan.json")}, nil
}

// ApplyRequest carries the operator's intent and the current render state.
type ApplyRequest struct {
	Kind        string // apply (default) | destroy
	Environment string
	Protected   bool

	// CurrentDigest/CurrentModulesDigest come from re-rendering the spec now.
	CurrentDigest        string
	CurrentModulesDigest string

	Approve            string // --approve <plan-id-prefix>
	AllowDestructive   bool   // --allow-destructive
	Yes                bool   // --yes (non-protected environments only)
	ConfirmEnvironment string // --confirm-environment (destroy in protected envs)
	MaxAge             time.Duration

	// Interactive is true when stdin and stdout are terminals; Confirm asks y/N.
	Interactive bool
	Confirm     func(prompt string) (bool, error)
	Now         func() time.Time
}

// CheckApply evaluates every safety gate for applying sp. It returns nil when the plan
// may be applied, a *GateError when a gate refuses, or another error.
func CheckApply(sp *SavedPlan, req ApplyRequest) error {
	if req.Kind == "" {
		req.Kind = KindApply
	}
	if req.Now == nil {
		req.Now = time.Now
	}
	if req.MaxAge <= 0 {
		req.MaxAge = DefaultMaxPlanAge
	}
	m := sp.Meta
	next := "run `hermes plan`"
	if req.Kind == KindDestroy {
		next = "run `hermes destroy` without --approve to create a new destroy plan"
	}
	switch {
	case m.Kind != req.Kind:
		return refuse(CodeWrongPlan, next, "the saved plan is a %s plan, not a %s plan", m.Kind, req.Kind)
	case m.Environment != req.Environment:
		return refuse(CodeWrongPlan, next, "the saved plan is for environment %q, not %q", m.Environment, req.Environment)
	case m.AppliedAt != nil:
		return refuse(CodeAlreadyDone, next, "plan %s was already applied at %s", short(m.PlanID), m.AppliedAt.Format(time.RFC3339))
	case m.RenderDigest != req.CurrentDigest || m.ModulesDigest != req.CurrentModulesDigest:
		return refuse(CodeStalePlan, next, "the spec or modules changed since plan %s was created (stale plan)", short(m.PlanID))
	}
	if age := req.Now().Sub(m.CreatedAt); age < -MaxClockSkew {
		// F-04: a future created_at would make every plan look fresh (meta.json is local, unsigned).
		return refuse(CodeExpiredPlan, next, "plan %s has created_at %s, %s in the future (allowed clock skew %s): meta.json is not trustworthy", short(m.PlanID), m.CreatedAt.Format(time.RFC3339), (-age).Round(time.Minute), MaxClockSkew)
	} else if age > req.MaxAge {
		return refuse(CodeExpiredPlan, next, "plan %s is %s old (maximum %s)", short(m.PlanID), age.Round(time.Minute), req.MaxAge)
	}
	id, err := fileSHA256(sp.PlanFile)
	if err != nil {
		return refuse(CodeTamperedPlan, next, "plan file missing or unreadable: %v", err)
	}
	if id != m.PlanID {
		return refuse(CodeTamperedPlan, next, "plan file does not match plan_id %s (modified after planning)", short(m.PlanID))
	}
	if m.Policy.Ran && !m.Policy.Passed {
		return refuse(CodePolicyFailed, "fix the findings and re-plan", "policy check failed for plan %s: %s", short(m.PlanID), strings.Join(m.Policy.Failures, "; "))
	}
	if req.Kind == KindApply {
		if d := m.Summary.Destructive(); len(d) > 0 && !req.AllowDestructive {
			return refuse(CodeDestructive, "review the plan and pass --allow-destructive", "plan deletes or replaces %d resource(s): %s", len(d), addresses(d))
		}
	}

	// Approval.
	needsApproval := req.Protected || req.Kind == KindDestroy
	if req.Approve != "" || needsApproval {
		if err := checkApproval(m.PlanID, req.Approve); err != nil {
			return err
		}
	}
	if req.Kind == KindDestroy && req.Protected && req.ConfirmEnvironment != req.Environment {
		return refuse(CodeConfirmEnv, fmt.Sprintf("pass --confirm-environment %s", req.Environment), "destroying protected environment %q requires --confirm-environment", req.Environment)
	}
	if needsApproval || req.Approve != "" || req.Yes {
		return nil
	}
	if !req.Interactive || req.Confirm == nil {
		return refuse(CodeDeclined, fmt.Sprintf("pass --yes or --approve %s", short(m.PlanID)), "refusing to apply without confirmation in a non-interactive session")
	}
	ok, err := req.Confirm(fmt.Sprintf("Apply plan %s to %s?", short(m.PlanID), req.Environment))
	if err != nil {
		return err
	}
	if !ok {
		return refuse(CodeDeclined, "", "apply declined")
	}
	return nil
}

// MaxClockSkew is how far in the future a plan's created_at may be (clock differences between
// the machine that planned and the one that applies).
const MaxClockSkew = 5 * time.Minute

func checkApproval(planID, approve string) error {
	switch err := approval.Check(planID, approve); {
	case errors.Is(err, approval.ErrMissing):
		return refuse(CodeApproval, fmt.Sprintf("pass --approve %s", short(planID)), "this operation requires --approve <plan-id-prefix> (plan %s)", short(planID))
	case errors.Is(err, approval.ErrTooShort), errors.Is(err, approval.ErrNotHex):
		return refuse(CodeApproval, "", "--approve needs at least %d hexadecimal characters of the plan id", MinApprovalPrefix)
	case err != nil:
		return refuse(CodeApproval, "re-read the plan id printed by the plan", "--approve %s does not match plan %s", approval.Normalize(approve), short(planID))
	}
	return nil
}

// Apply runs the gates and then applies the saved plan. On success the plan is marked
// applied and its plan file removed, so it can never be applied twice.
func Apply(ctx context.Context, eng *Engine, rootDir string, sp *SavedPlan, req ApplyRequest) error {
	if err := CheckApply(sp, req); err != nil {
		return err
	}
	// A saved plan can be applied on another machine than the one that planned it (CI: the plan
	// job uploads .hermes/plans/<env>/, the approved apply job downloads it). The rendered root
	// is re-rendered by the caller (its digest is gated above) but may not be initialised yet;
	// init only installs the pinned providers and configures the backend, it never plans.
	if _, err := os.Stat(filepath.Join(rootDir, ".terraform")); errors.Is(err, os.ErrNotExist) {
		if err := eng.Init(ctx, rootDir); err != nil {
			return fmt.Errorf("apply: %w", err)
		}
	}
	if err := eng.ApplySaved(ctx, rootDir, sp.PlanFile); err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if req.Now != nil {
		now = req.Now().UTC().Truncate(time.Second)
	}
	sp.Meta.AppliedAt = &now
	if err := writeJSON(filepath.Join(sp.Dir, "meta.json"), sp.Meta); err != nil {
		return fmt.Errorf("apply: record: %w", err)
	}
	_ = os.Remove(sp.PlanFile)
	return nil
}

// ShortID returns the first 12 characters of a plan id.
func ShortID(id string) string { return short(id) }

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func addresses(items []tfplan.Item) string {
	var out []string
	for i, it := range items {
		if i == 5 {
			out = append(out, fmt.Sprintf("and %d more", len(items)-5))
			break
		}
		out = append(out, fmt.Sprintf("%s (%s)", it.Address, it.Action))
	}
	return strings.Join(out, ", ")
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// HistoryEvent is one line of .hermes/history/<env>.jsonl. It never contains secrets.
type HistoryEvent struct {
	Time        time.Time `json:"time"`
	Environment string    `json:"environment"`
	Action      string    `json:"action"` // plan | apply | destroy-plan | destroy | bootstrap-plan | bootstrap-apply
	Result      string    `json:"result"` // ok | refused | failed
	PlanID      string    `json:"plan_id,omitempty"`
	Code        string    `json:"code,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	User        string    `json:"user,omitempty"`
	Engine      string    `json:"engine,omitempty"`
	Changes     *Counts   `json:"changes,omitempty"`
	SkipPolicy  bool      `json:"skip_policy,omitempty"`
	// Policies are the digests of the policy bundles and data files the plan was checked with.
	Policies []PolicySource `json:"policies,omitempty"`
	// PolicyExceptions are the policy exceptions in effect for the plan.
	PolicyExceptions []PolicyException `json:"policy_exceptions,omitempty"`
}

// Counts summarizes a plan for history.
type Counts struct {
	Create  int `json:"create"`
	Update  int `json:"update"`
	Delete  int `json:"delete"`
	Replace int `json:"replace"`
	Import  int `json:"import"`
}

// CountsOf extracts history counts from a summary.
func CountsOf(s tfplan.Summary) *Counts {
	return &Counts{Create: s.Create, Update: s.Update, Delete: s.Delete, Replace: s.Replace, Import: s.Import}
}

// AppendHistory appends one event (JSON Lines, append-only).
func AppendHistory(path string, ev HistoryEvent) error {
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC().Truncate(time.Second)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// ReadHistory returns every event in path (empty when the file does not exist).
func ReadHistory(path string) ([]HistoryEvent, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []HistoryEvent
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev HistoryEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("history %s line %d: %w", path, i+1, err)
		}
		out = append(out, ev)
	}
	return out, nil
}
