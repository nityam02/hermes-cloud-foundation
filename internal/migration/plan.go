package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nityam02/hermes-cloud-foundation/internal/yamlsafe"
)

// Plan is a MigrationPlan document (data-contracts §7).
type Plan struct {
	APIVersion string         `json:"apiVersion" yaml:"apiVersion"`
	Kind       string         `json:"kind" yaml:"kind"`
	Metadata   PlanMetadata   `json:"metadata" yaml:"metadata"`
	Summary    PlanSummary    `json:"summary" yaml:"summary"`
	Phases     []PlanPhase    `json:"phases" yaml:"phases"`
	Coverage   []CoverageItem `json:"coverage" yaml:"coverage"`
	Notes      []string       `json:"notes" yaml:"notes"`
}

// PlanEndpoint names a side of the migration.
type PlanEndpoint struct {
	Provider    string   `json:"provider" yaml:"provider"`
	AccountID   string   `json:"account_id,omitempty" yaml:"account_id,omitempty"`
	Regions     []string `json:"regions,omitempty" yaml:"regions,omitempty"`
	Environment string   `json:"environment,omitempty" yaml:"environment,omitempty"`
}

// PlanMetadata carries identity and provenance. plan_id is the sha256 of the canonical plan
// body (everything except plan_id and generated_at), so regenerating an identical plan
// yields the same id.
type PlanMetadata struct {
	PlanID           string       `json:"plan_id" yaml:"plan_id"`
	GeneratedAt      string       `json:"generated_at" yaml:"generated_at"`
	Generator        string       `json:"generator" yaml:"generator"`
	Source           PlanEndpoint `json:"source" yaml:"source"`
	Target           PlanEndpoint `json:"target" yaml:"target"`
	InventoryDigest  string       `json:"inventory_digest" yaml:"inventory_digest"`
	AssessmentDigest string       `json:"assessment_digest" yaml:"assessment_digest"`
	WorkflowsDigest  string       `json:"workflows_digest" yaml:"workflows_digest"`
}

// PlanSummary counts steps and gates.
type PlanSummary struct {
	Phases               int `json:"phases" yaml:"phases"`
	Steps                int `json:"steps" yaml:"steps"`
	ApprovalGates        int `json:"approval_gates" yaml:"approval_gates"`
	ValidationGates      int `json:"validation_gates" yaml:"validation_gates"`
	SourceImpactingSteps int `json:"source_impacting_steps" yaml:"source_impacting_steps"`
	ManualSteps          int `json:"manual_steps" yaml:"manual_steps"`
}

// PlanPhase groups steps of one phase.
type PlanPhase struct {
	ID    string     `json:"id" yaml:"id"`
	Order int        `json:"order" yaml:"order"`
	Steps []PlanStep `json:"steps" yaml:"steps"`
}

// PlanStep is one instantiated workflow step.
type PlanStep struct {
	ID           string   `json:"id" yaml:"id"`
	Workflow     string   `json:"workflow" yaml:"workflow"`
	WorkflowStep string   `json:"workflow_step" yaml:"workflow_step"`
	Title        string   `json:"title" yaml:"title"`
	Description  string   `json:"description" yaml:"description"`
	Phase        string   `json:"phase" yaml:"phase"`
	Wave         int      `json:"wave" yaml:"wave"`
	Automation   string   `json:"automation" yaml:"automation"`
	Gate         string   `json:"gate" yaml:"gate"`
	SourceImpact string   `json:"source_impact" yaml:"source_impact"`
	Reversible   bool     `json:"reversible" yaml:"reversible"`
	Rollback     string   `json:"rollback" yaml:"rollback"`
	Validation   []string `json:"validation" yaml:"validation"`
	Risks        []string `json:"risks" yaml:"risks"`
	ResourceRefs []string `json:"resource_refs" yaml:"resource_refs"`
	DependsOn    []string `json:"depends_on" yaml:"depends_on"`
}

// CoverageItem is a source resource within Hermes scope and the spec element that must
// cover it (used by the spec-coverage validation check).
type CoverageItem struct {
	SourceID   string `json:"source_id" yaml:"source_id"`
	SourceType string `json:"source_type" yaml:"source_type"`
	Kind       string `json:"kind" yaml:"kind"` // network|database|bucket|topic|subscription|secret|service|block
	Name       string `json:"name" yaml:"name"`
}

// PlanOptions configures BuildPlan.
type PlanOptions struct {
	Environment string // target environment name (recorded; cutover --confirm-target must match)
	Generator   string
	Now         func() time.Time
}

var singletonWorkflows = map[string]bool{"foundation": true, "hybrid-connectivity": true, "traffic-cutover": true, "decommission": true}
var perResourceWorkflows = map[string]bool{"relational-database-postgresql": true, "relational-database-mysql": true, "nosql-database": true}

type instance struct {
	key      string // step id prefix
	workflow *Workflow
	wave     int
	refs     []string
	steps    []*PlanStep
	order    int
}

// BuildPlan instantiates workflows for the assessed resources into ordered phases.
//
// Dependencies: steps of one workflow instance run in order; every instance waits for the
// foundation; data synchronization waits for hybrid connectivity; cutovers follow the
// dependency waves (wave k cuts over after wave k-1); traffic cutover waits for every
// resource to be validated; decommission waits for traffic cutover.
func BuildPlan(a *Assessment, ws *Workflows, o PlanOptions) (*Plan, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Generator == "" {
		o.Generator = "hermes"
	}
	if o.Environment == "" {
		o.Environment = "production"
	}
	p := &Plan{APIVersion: APIVersion, Kind: "MigrationPlan", Notes: []string{}}
	p.Metadata = PlanMetadata{Generator: o.Generator,
		Source:          PlanEndpoint{Provider: a.Metadata.Source.Provider, AccountID: a.Metadata.Source.AccountID, Regions: a.Metadata.Source.Regions},
		Target:          PlanEndpoint{Provider: a.Metadata.Target.Provider, Environment: o.Environment},
		InventoryDigest: a.Metadata.InventoryDigest, AssessmentDigest: a.Digest(), WorkflowsDigest: ws.Digest}

	var all []string
	groups := map[string]*instance{}
	var insts []*instance
	get := func(key, wf string, wave int) (*instance, error) {
		if in, ok := groups[key]; ok {
			return in, nil
		}
		w := ws.ByID[wf]
		if w == nil {
			return nil, fmt.Errorf("plan: workflow %q referenced by the assessment does not exist", wf)
		}
		in := &instance{key: key, workflow: w, wave: wave, order: len(insts)}
		groups[key] = in
		insts = append(insts, in)
		return in, nil
	}
	for _, id := range []string{"foundation", "hybrid-connectivity"} {
		if _, err := get(id, id, 0); err != nil {
			return nil, err
		}
	}
	names := namer{}
	res := append([]AssessedResource(nil), a.Resources...)
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].Wave != res[j].Wave {
			return res[i].Wave < res[j].Wave
		}
		return res[i].ID < res[j].ID
	})
	for _, r := range res {
		all = append(all, r.ID)
		wf := r.Workflow
		var key string
		switch {
		case singletonWorkflows[wf]:
			key = wf
		case perResourceWorkflows[wf]:
			key = wf + "." + names.take(r.Name, 30)
		default:
			key = fmt.Sprintf("%s.w%d", wf, r.Wave)
		}
		in, err := get(key, wf, r.Wave)
		if err != nil {
			return nil, err
		}
		if singletonWorkflows[wf] && r.Wave > in.wave {
			in.wave = r.Wave
		}
		in.refs = append(in.refs, r.ID)
	}
	maxWave := 0
	for _, in := range insts {
		if in.wave > maxWave {
			maxWave = in.wave
		}
	}
	for _, id := range []string{"traffic-cutover", "decommission"} {
		in, err := get(id, id, maxWave)
		if err != nil {
			return nil, err
		}
		in.wave = maxWave
	}
	groups["decommission"].refs = append([]string(nil), all...)

	// Instantiate steps.
	for _, in := range insts {
		sort.Strings(in.refs)
		if in.refs == nil {
			in.refs = []string{}
		}
		for _, s := range in.workflow.Spec.Steps {
			st := &PlanStep{ID: in.key + "." + s.ID, Workflow: in.workflow.Metadata.ID, WorkflowStep: s.ID, Title: s.Title,
				Description: strings.TrimSpace(s.Description), Phase: s.Phase, Wave: in.wave, Automation: s.Automation, Gate: s.Gate,
				SourceImpact: s.SourceImpact, Reversible: s.Reversible, Rollback: s.Rollback,
				Validation: nonNil(s.Validation), Risks: nonNil(s.Risks), ResourceRefs: in.refs, DependsOn: []string{}}
			if (SourceImpactGated(st.SourceImpact) || st.Phase == "cutover" || st.Phase == "decommission") && st.Gate != "approval" {
				p.Notes = append(p.Notes, fmt.Sprintf("step %s: gate raised to approval (source impact %s, phase %s)", st.ID, st.SourceImpact, st.Phase))
				st.Gate = "approval"
			}
			if st.Phase == "decommission" {
				st.Automation = "manual" // Hermes never executes decommission
			}
			in.steps = append(in.steps, st)
		}
		// Sequential dependencies within the instance.
		for i, st := range in.steps {
			for j := i - 1; j >= 0; j-- {
				if PhaseRank(in.steps[j].Phase) <= PhaseRank(st.Phase) {
					st.DependsOn = append(st.DependsOn, in.steps[j].ID)
					break
				}
			}
		}
	}
	lastUpTo := func(in *instance, phase string) *PlanStep {
		var out *PlanStep
		for _, s := range in.steps {
			if PhaseRank(s.Phase) <= PhaseRank(phase) {
				out = s
			}
		}
		return out
	}
	firstIn := func(in *instance, phase string) *PlanStep {
		for _, s := range in.steps {
			if s.Phase == phase {
				return s
			}
		}
		return nil
	}
	lastIn := func(in *instance, phase string) *PlanStep {
		var out *PlanStep
		for _, s := range in.steps {
			if s.Phase == phase {
				out = s
			}
		}
		return out
	}
	dep := func(s *PlanStep, on *PlanStep) {
		if s == nil || on == nil || s == on {
			return
		}
		for _, d := range s.DependsOn {
			if d == on.ID {
				return
			}
		}
		s.DependsOn = append(s.DependsOn, on.ID)
	}
	foundation := groups["foundation"]
	hybrid := groups["hybrid-connectivity"]
	traffic := groups["traffic-cutover"]
	decom := groups["decommission"]
	foundationDone := lastUpTo(foundation, "provision")
	hybridReady := lastUpTo(hybrid, "provision")
	cutoversByWave := map[int][]*PlanStep{}
	var resourceInsts []*instance
	for _, in := range insts {
		if !singletonWorkflows[in.workflow.Metadata.ID] {
			resourceInsts = append(resourceInsts, in)
			if c := lastIn(in, "cutover"); c != nil {
				cutoversByWave[in.wave] = append(cutoversByWave[in.wave], c)
			}
		}
	}
	for _, in := range insts {
		if in == foundation || len(in.steps) == 0 {
			continue
		}
		dep(in.steps[0], foundationDone)
		if in != hybrid {
			dep(firstIn(in, "synchronize"), hybridReady)
		}
		if !singletonWorkflows[in.workflow.Metadata.ID] {
			if c := firstIn(in, "cutover"); c != nil {
				for w := in.wave - 1; w >= 0; w-- {
					if prev := cutoversByWave[w]; len(prev) > 0 {
						for _, ps := range prev {
							dep(c, ps)
						}
						break
					}
				}
			}
		}
	}
	if tc := firstIn(traffic, "cutover"); tc != nil {
		for _, in := range resourceInsts {
			dep(tc, lastUpTo(in, "validate"))
		}
	}
	if len(decom.steps) > 0 && len(traffic.steps) > 0 {
		dep(decom.steps[0], traffic.steps[len(traffic.steps)-1])
	}

	// Phases.
	byPhase := map[string][]*PlanStep{}
	type ordered struct {
		s    *PlanStep
		inst *instance
		idx  int
	}
	var flat []ordered
	for _, in := range insts {
		for i, s := range in.steps {
			flat = append(flat, ordered{s, in, i})
		}
	}
	sort.SliceStable(flat, func(i, j int) bool {
		a, b := flat[i], flat[j]
		if a.s.Phase != b.s.Phase {
			return PhaseRank(a.s.Phase) < PhaseRank(b.s.Phase)
		}
		if a.s.Wave != b.s.Wave {
			return a.s.Wave < b.s.Wave
		}
		if a.inst.order != b.inst.order {
			return a.inst.order < b.inst.order
		}
		return a.idx < b.idx
	})
	for _, f := range flat {
		byPhase[f.s.Phase] = append(byPhase[f.s.Phase], f.s)
	}
	for i, ph := range Phases {
		steps := byPhase[ph]
		if len(steps) == 0 {
			continue
		}
		pp := PlanPhase{ID: ph, Order: i + 1}
		for _, s := range steps {
			pp.Steps = append(pp.Steps, *s)
			p.Summary.Steps++
			switch s.Gate {
			case "approval":
				p.Summary.ApprovalGates++
			case "validation":
				p.Summary.ValidationGates++
			}
			if SourceImpactGated(s.SourceImpact) {
				p.Summary.SourceImpactingSteps++
			}
			if s.Automation == "manual" {
				p.Summary.ManualSteps++
			}
		}
		p.Phases = append(p.Phases, pp)
	}
	p.Summary.Phases = len(p.Phases)
	p.Coverage = coverage(a)
	if err := p.CheckDependencies(); err != nil {
		return nil, err
	}
	p.Metadata.PlanID = p.ComputeID()
	p.Metadata.GeneratedAt = o.Now().UTC().Format(time.RFC3339)
	return p, nil
}

func nonNil(xs []string) []string {
	if len(xs) == 0 {
		return []string{}
	}
	return append([]string(nil), xs...)
}

// coverage lists in-scope resources and the spec element expected to cover each.
func coverage(a *Assessment) []CoverageItem {
	support := map[string]string{}
	for _, r := range a.Resources {
		support[r.ID] = r.HermesSupport
	}
	out := []CoverageItem{}
	for _, m := range a.SpecMapping {
		if support[m.SourceID] != "implemented" {
			continue
		}
		item := CoverageItem{SourceID: m.SourceID, SourceType: m.SourceType, Kind: m.TargetKind, Name: m.TargetName}
		if m.TargetKind == "absorbed" {
			item.Kind, item.Name = "block", m.TargetPath
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	return out
}

// Steps returns all steps in plan order.
func (p *Plan) Steps() []PlanStep {
	var out []PlanStep
	for _, ph := range p.Phases {
		out = append(out, ph.Steps...)
	}
	return out
}

// Step returns the step with id, or nil.
func (p *Plan) Step(id string) *PlanStep {
	for i := range p.Phases {
		for j := range p.Phases[i].Steps {
			if p.Phases[i].Steps[j].ID == id {
				return &p.Phases[i].Steps[j]
			}
		}
	}
	return nil
}

// CheckDependencies verifies that every dependency exists and the step graph is acyclic.
func (p *Plan) CheckDependencies() error {
	ids := map[string][]string{}
	for _, s := range p.Steps() {
		if _, dup := ids[s.ID]; dup {
			return fmt.Errorf("plan: duplicate step id %s", s.ID)
		}
		ids[s.ID] = s.DependsOn
	}
	for id, deps := range ids {
		for _, d := range deps {
			if _, ok := ids[d]; !ok {
				return fmt.Errorf("plan: step %s depends on unknown step %s", id, d)
			}
		}
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("plan: dependency cycle through %s", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, d := range ids[id] {
			if err := visit(d); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	keys := make([]string, 0, len(ids))
	for k := range ids {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := visit(k); err != nil {
			return err
		}
	}
	return nil
}

// ComputeID returns the sha256 (hex) of the canonical plan body.
func (p *Plan) ComputeID() string {
	cp := *p
	cp.Metadata.PlanID = ""
	cp.Metadata.GeneratedAt = ""
	b, _ := json.Marshal(cp)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// MarshalYAML renders the plan.
func (p *Plan) YAML() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# MigrationPlan generated by hermes migrate plan. Review every step and gate before use.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// MaxPlanBytes caps a migration plan file. Plans are generated by hermes and grow with the
// estate, so the cap is larger than the 1 MiB used for hand-written inputs; anchors and
// aliases are rejected as for every YAML input.
const MaxPlanBytes = 32 << 20

// LoadPlan reads a plan (YAML or JSON) and verifies its plan_id against its body.
func LoadPlan(path string) (*Plan, error) {
	data, err := yamlsafe.ReadFile(path, MaxPlanBytes)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	var p Plan
	if err := yamlsafe.DecodeStrict(data, MaxPlanBytes, &p); err != nil {
		return nil, fmt.Errorf("plan %s: %w", path, err)
	}
	if p.APIVersion != APIVersion || p.Kind != "MigrationPlan" {
		return nil, fmt.Errorf("plan %s: not a %s MigrationPlan", path, APIVersion)
	}
	normalizePlan(&p)
	if got := p.ComputeID(); got != p.Metadata.PlanID {
		return nil, fmt.Errorf("plan %s: plan_id %s does not match the plan body (%s); the plan was modified after generation — regenerate it with hermes migrate plan", path, short(p.Metadata.PlanID), short(got))
	}
	if err := p.CheckDependencies(); err != nil {
		return nil, err
	}
	return &p, nil
}

// normalizePlan restores empty slices that YAML decodes as nil so the id is stable.
func normalizePlan(p *Plan) {
	if p.Notes == nil {
		p.Notes = []string{}
	}
	if p.Coverage == nil {
		p.Coverage = []CoverageItem{}
	}
	for i := range p.Phases {
		for j := range p.Phases[i].Steps {
			s := &p.Phases[i].Steps[j]
			s.Validation, s.Risks, s.ResourceRefs, s.DependsOn = nonNil(s.Validation), nonNil(s.Risks), nonNil(s.ResourceRefs), nonNil(s.DependsOn)
		}
	}
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
