// Package migration implements the Terraform-independent migration engine: catalog
// classification, assessment, draft target spec generation, migration plans, validation
// checks and the gated cutover state machine. It never touches source resources: the only
// external actions it can take are user-declared validation commands and cutover hooks.
package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nityam02/hermes-cloud-foundation/internal/yamlsafe"

	migrationdata "github.com/nityam02/hermes-cloud-foundation/migration"
)

// APIVersion of every migration document.
const APIVersion = "hermes/v1alpha1"

// Enumerations from data-contracts §4–§6.
var (
	Categories = []string{"compute", "container-orchestration", "serverless-container", "kubernetes",
		"serverless-function", "container-registry", "object-storage", "block-storage", "file-storage",
		"relational-database", "nosql-database", "cache", "analytics", "streaming", "messaging-queue",
		"messaging-pubsub", "iot", "geospatial", "time-series", "networking", "hybrid-connectivity",
		"load-balancing", "cdn", "dns", "identity", "secrets", "key-management", "certificates", "monitoring",
		"logging", "tracing", "api-gateway", "workflow", "notification", "backup", "iac", "other"}
	Capabilities      = []string{"network", "compute", "database", "storage", "messaging", "secrets", "observability", "identity", "dns", "none"}
	Compatibility     = []string{"equivalent", "compatible", "partial", "re-architect", "none", "unknown"}
	Levels            = []string{"low", "medium", "high"}
	DataMigrations    = []string{"required", "optional", "none"}
	HermesSupport     = []string{"implemented", "partial", "not-implemented"}
	Phases            = []string{"foundation", "prepare", "provision", "synchronize", "validate", "cutover", "monitor", "decommission"}
	Automations       = []string{"manual", "hermes", "provider-tool"}
	Gates             = []string{"none", "approval", "validation"}
	SourceImpacts     = []string{"none", "read-only-load", "modifies-source-config", "modifies-source-data"}
	RateKinds         = []string{"egress-internet", "egress-inter-region", "transfer-service", "replication-service"}
	Targets           = []string{"gcp", "alibaba"}
	ReservedWorkflows = []string{"foundation", "hybrid-connectivity", "stateless-rebuild", "container-workload",
		"relational-database-postgresql", "relational-database-mysql", "object-storage", "messaging", "secrets",
		"nosql-database", "traffic-cutover", "decommission", "manual-review"}
)

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// PhaseRank returns the position of a phase in the global order.
func PhaseRank(p string) int {
	for i, x := range Phases {
		if x == p {
			return i
		}
	}
	return len(Phases)
}

// SourceImpactGated reports whether a source impact requires an approval gate.
func SourceImpactGated(impact string) bool {
	return impact != "none" && impact != "read-only-load"
}

var kebab = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// digest returns "sha256:<hex>".
func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// readSource reads an override file, or the embedded default when file is "".
func readSource(file, embedded string) ([]byte, string, error) {
	if file != "" {
		b, err := yamlsafe.ReadFile(file, yamlsafe.MaxBytes)
		return b, file, err
	}
	b, err := migrationdata.FS.ReadFile(embedded)
	return b, "embedded:migration/" + embedded, err
}

// decodeStrict decodes a catalog, mapping or pricing document: at most 1 MiB, no anchors or
// aliases, no unknown fields.
func decodeStrict(data []byte, out any) error {
	return yamlsafe.DecodeStrict(data, yamlsafe.MaxBytes, out)
}

// ---------------------------------------------------------------------------------------
// ServiceCatalog (data-contracts §4)

// Catalog is a ServiceCatalog document.
type Catalog struct {
	APIVersion string          `yaml:"apiVersion" json:"apiVersion"`
	Kind       string          `yaml:"kind" json:"kind"`
	Metadata   CatalogMetadata `yaml:"metadata" json:"metadata"`
	Entries    []CatalogEntry  `yaml:"entries" json:"entries"`

	Digest string `yaml:"-" json:"-"`
	Source string `yaml:"-" json:"-"`
	byType map[string][]int
}

// CatalogMetadata is the catalog header.
type CatalogMetadata struct {
	ReviewedAt string `yaml:"reviewed_at" json:"reviewed_at"`
	Disclaimer string `yaml:"disclaimer" json:"disclaimer"`
}

// CatalogEntry maps source resource types to target services.
type CatalogEntry struct {
	ID         string                   `yaml:"id" json:"id"`
	Category   string                   `yaml:"category" json:"category"`
	Capability string                   `yaml:"capability" json:"capability"`
	Source     CatalogSource            `yaml:"source" json:"source"`
	Targets    map[string]CatalogTarget `yaml:"targets" json:"targets"`
}

// CatalogSource identifies the source service.
type CatalogSource struct {
	Provider      string           `yaml:"provider" json:"provider"`
	Service       string           `yaml:"service" json:"service"`
	ResourceTypes []string         `yaml:"resource_types" json:"resource_types"`
	Match         map[string][]any `yaml:"match,omitempty" json:"match,omitempty"`
}

// CatalogTarget is the mapping to one target provider.
type CatalogTarget struct {
	Service       string   `yaml:"service" json:"service"`
	Alternatives  []string `yaml:"alternatives" json:"alternatives"`
	Compatibility string   `yaml:"compatibility" json:"compatibility"`
	Complexity    string   `yaml:"complexity" json:"complexity"`
	DataMigration string   `yaml:"data_migration" json:"data_migration"`
	Workflow      string   `yaml:"workflow" json:"workflow"`
	Tooling       []string `yaml:"tooling" json:"tooling"`
	LockIn        string   `yaml:"lock_in" json:"lock_in"`
	HermesSupport string   `yaml:"hermes_support" json:"hermes_support"`
	Differences   []string `yaml:"differences" json:"differences"`
	Issues        []string `yaml:"issues" json:"issues"`
	References    []string `yaml:"references" json:"references"`
}

// LoadCatalog loads and validates a catalog (file "" = embedded default).
func LoadCatalog(file string) (*Catalog, error) {
	data, src, err := readSource(file, "mappings/catalog.yaml")
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	c, err := ParseCatalog(data)
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", src, err)
	}
	c.Source = src
	return c, nil
}

// ParseCatalog decodes, validates and indexes a catalog.
func ParseCatalog(data []byte) (*Catalog, error) {
	var c Catalog
	if err := decodeStrict(data, &c); err != nil {
		return nil, err
	}
	c.Digest = digest(data)
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Catalog) validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if c.APIVersion != APIVersion || c.Kind != "ServiceCatalog" {
		add("apiVersion/kind must be %s/ServiceCatalog", APIVersion)
	}
	ids := map[string]bool{}
	c.byType = map[string][]int{}
	for i, e := range c.Entries {
		if !kebab.MatchString(e.ID) {
			add("entries[%d]: id %q must be kebab-case", i, e.ID)
		}
		if ids[e.ID] {
			add("entries[%d]: duplicate id %q", i, e.ID)
		}
		ids[e.ID] = true
		if !oneOf(e.Category, Categories) {
			add("%s: unknown category %q", e.ID, e.Category)
		}
		if !oneOf(e.Capability, Capabilities) {
			add("%s: unknown capability %q", e.ID, e.Capability)
		}
		if len(e.Source.ResourceTypes) == 0 {
			add("%s: source.resource_types is empty", e.ID)
		}
		for _, t := range Targets {
			tg, ok := e.Targets[t]
			if !ok {
				add("%s: missing target %s", e.ID, t)
				continue
			}
			if !oneOf(tg.Compatibility, Compatibility) || !oneOf(tg.Complexity, Levels) || !oneOf(tg.DataMigration, DataMigrations) ||
				!oneOf(tg.LockIn, Levels) || !oneOf(tg.HermesSupport, HermesSupport) || tg.Workflow == "" || tg.Service == "" {
				add("%s/%s: invalid enum value or missing service/workflow", e.ID, t)
			}
		}
		for _, rt := range e.Source.ResourceTypes {
			c.byType[rt] = append(c.byType[rt], i)
		}
	}
	// Ambiguity: two entries for one type that could both match the same resource with the
	// same number of match keys (or two fallbacks) make classification order-dependent.
	for rt, idx := range c.byType {
		for a := 0; a < len(idx); a++ {
			for b := a + 1; b < len(idx); b++ {
				ea, eb := c.Entries[idx[a]], c.Entries[idx[b]]
				if len(ea.Source.Match) != len(eb.Source.Match) {
					continue
				}
				if canBothMatch(ea.Source.Match, eb.Source.Match) {
					add("%s: entries %s and %s tie for the same resources (equal match keys); make one more specific", rt, ea.ID, eb.ID)
				}
			}
		}
		sort.SliceStable(idx, func(i, j int) bool {
			return len(c.Entries[idx[i]].Source.Match) > len(c.Entries[idx[j]].Source.Match)
		})
		c.byType[rt] = idx
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("invalid catalog:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

func canBothMatch(a, b map[string][]any) bool {
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			continue
		}
		overlap := false
		for _, x := range va {
			for _, y := range vb {
				if strings.EqualFold(Stringify(x), Stringify(y)) {
					overlap = true
				}
			}
		}
		if !overlap {
			return false
		}
	}
	return true
}

// Stringify converts a scalar to its match string: booleans "true"/"false", numbers in
// their shortest decimal form, strings unchanged. Non-scalars return "".
func Stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

// Match returns the catalog entry for a resource type and attributes, or nil.
// Entries with match are evaluated first (most keys first); an entry matches when every key's
// stringified attribute value equals one of its values case-insensitively.
func (c *Catalog) Match(resourceType string, attributes map[string]any) *CatalogEntry {
	for _, i := range c.byType[resourceType] {
		e := &c.Entries[i]
		if matches(e.Source.Match, attributes) {
			return e
		}
	}
	return nil
}

func matches(match map[string][]any, attributes map[string]any) bool {
	for k, allowed := range match {
		v, ok := attributes[k]
		if !ok {
			return false
		}
		s := Stringify(v)
		if s == "" {
			return false
		}
		hit := false
		for _, a := range allowed {
			if strings.EqualFold(s, Stringify(a)) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// Entry returns the entry with id, or nil.
func (c *Catalog) Entry(id string) *CatalogEntry {
	for i := range c.Entries {
		if c.Entries[i].ID == id {
			return &c.Entries[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------------------
// MigrationWorkflow (data-contracts §5)

// Workflow is a MigrationWorkflow document.
type Workflow struct {
	APIVersion string           `yaml:"apiVersion" json:"apiVersion"`
	Kind       string           `yaml:"kind" json:"kind"`
	Metadata   WorkflowMetadata `yaml:"metadata" json:"metadata"`
	Spec       WorkflowSpec     `yaml:"spec" json:"spec"`
}

// WorkflowMetadata identifies a workflow.
type WorkflowMetadata struct {
	ID        string   `yaml:"id" json:"id"`
	Title     string   `yaml:"title" json:"title"`
	AppliesTo []string `yaml:"applies_to" json:"applies_to"`
}

// WorkflowSpec holds the ordered steps.
type WorkflowSpec struct {
	Steps []WorkflowStep `yaml:"steps" json:"steps"`
}

// WorkflowStep is one step template.
type WorkflowStep struct {
	ID           string   `yaml:"id" json:"id"`
	Phase        string   `yaml:"phase" json:"phase"`
	Title        string   `yaml:"title" json:"title"`
	Description  string   `yaml:"description" json:"description"`
	Automation   string   `yaml:"automation" json:"automation"`
	Gate         string   `yaml:"gate" json:"gate"`
	Reversible   bool     `yaml:"reversible" json:"reversible"`
	Rollback     string   `yaml:"rollback" json:"rollback"`
	Risks        []string `yaml:"risks" json:"risks"`
	Validation   []string `yaml:"validation" json:"validation"`
	SourceImpact string   `yaml:"source_impact" json:"source_impact"`
}

// Workflows is a set of workflows keyed by id.
type Workflows struct {
	ByID   map[string]*Workflow
	Digest string
	Source string
}

// LoadWorkflows loads every *.yaml in dir (dir "" = embedded defaults).
func LoadWorkflows(dir string) (*Workflows, error) {
	var fsys fs.FS = migrationdata.FS
	root := "workflows"
	src := "embedded:migration/workflows"
	if dir != "" {
		fsys, root, src = os.DirFS(dir), ".", dir
	}
	names, err := fs.Glob(fsys, path.Join(root, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("workflows: %w", err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("workflows: no *.yaml files in %s", src)
	}
	sort.Strings(names)
	ws := &Workflows{ByID: map[string]*Workflow{}, Source: src}
	h := sha256.New()
	var errs []string
	for _, n := range names {
		data, err := fs.ReadFile(fsys, n)
		if err != nil {
			return nil, fmt.Errorf("workflows: %w", err)
		}
		h.Write(data)
		w, err := ParseWorkflow(data)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", n, err))
			continue
		}
		if _, dup := ws.ByID[w.Metadata.ID]; dup {
			errs = append(errs, fmt.Sprintf("%s: duplicate workflow id %q", n, w.Metadata.ID))
		}
		ws.ByID[w.Metadata.ID] = w
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("workflows %s:\n  %s", src, strings.Join(errs, "\n  "))
	}
	ws.Digest = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return ws, nil
}

// ParseWorkflow decodes and validates one workflow, enforcing the gate rules: any step that
// modifies the source, and any cutover or decommission step, must have gate: approval.
func ParseWorkflow(data []byte) (*Workflow, error) {
	var w Workflow
	if err := decodeStrict(data, &w); err != nil {
		return nil, err
	}
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if w.APIVersion != APIVersion || w.Kind != "MigrationWorkflow" {
		add("apiVersion/kind must be %s/MigrationWorkflow", APIVersion)
	}
	if !kebab.MatchString(w.Metadata.ID) {
		add("metadata.id %q must be kebab-case", w.Metadata.ID)
	}
	for _, a := range w.Metadata.AppliesTo {
		if !oneOf(a, Categories) {
			add("applies_to: unknown category %q", a)
		}
	}
	seen := map[string]bool{}
	for _, s := range w.Spec.Steps {
		if seen[s.ID] {
			add("duplicate step id %q", s.ID)
		}
		seen[s.ID] = true
		if !kebab.MatchString(s.ID) || !oneOf(s.Phase, Phases) || !oneOf(s.Automation, Automations) || !oneOf(s.Gate, Gates) || !oneOf(s.SourceImpact, SourceImpacts) {
			add("step %q: invalid id or enum value", s.ID)
		}
		if SourceImpactGated(s.SourceImpact) && s.Gate != "approval" {
			add("step %q: source_impact %s requires gate: approval", s.ID, s.SourceImpact)
		}
		if (s.Phase == "cutover" || s.Phase == "decommission") && s.Gate != "approval" {
			add("step %q: %s steps require gate: approval", s.ID, s.Phase)
		}
		if s.Phase == "decommission" && s.Automation == "hermes" {
			add("step %q: decommission steps are never automated by Hermes", s.ID)
		}
	}
	if len(w.Spec.Steps) == 0 {
		add("spec.steps is empty")
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid workflow %s: %s", w.Metadata.ID, strings.Join(errs, "; "))
	}
	return &w, nil
}

// CheckReferences verifies that every catalog workflow exists.
func CheckReferences(c *Catalog, ws *Workflows) error {
	var missing []string
	for _, e := range c.Entries {
		for _, t := range Targets {
			if wf := e.Targets[t].Workflow; ws.ByID[wf] == nil {
				missing = append(missing, fmt.Sprintf("%s/%s -> %s", e.ID, t, wf))
			}
		}
	}
	for _, id := range []string{"foundation", "hybrid-connectivity", "traffic-cutover", "decommission", "manual-review"} {
		if ws.ByID[id] == nil {
			missing = append(missing, "required workflow "+id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("unknown workflows referenced: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ---------------------------------------------------------------------------------------
// PricingAssumptions (data-contracts §6)

// Pricing is a PricingAssumptions document.
type Pricing struct {
	APIVersion string          `yaml:"apiVersion" json:"apiVersion"`
	Kind       string          `yaml:"kind" json:"kind"`
	Metadata   PricingMetadata `yaml:"metadata" json:"metadata"`
	Rates      []Rate          `yaml:"rates" json:"rates"`
	Notes      []string        `yaml:"notes" json:"notes"`

	Digest string `yaml:"-" json:"-"`
	Source string `yaml:"-" json:"-"`
}

// PricingMetadata is the pricing header.
type PricingMetadata struct {
	RetrievedAt string `yaml:"retrieved_at" json:"retrieved_at"`
	Disclaimer  string `yaml:"disclaimer" json:"disclaimer"`
}

// Rate is one indicative price.
type Rate struct {
	ID        string  `yaml:"id" json:"id"`
	Provider  string  `yaml:"provider" json:"provider"`
	Kind      string  `yaml:"kind" json:"kind"`
	USDPerGB  float64 `yaml:"usd_per_gb" json:"usd_per_gb"`
	AppliesTo string  `yaml:"applies_to" json:"applies_to"`
	Source    string  `yaml:"source" json:"source"`
}

// LoadPricing loads pricing assumptions (file "" = embedded default).
func LoadPricing(file string) (*Pricing, error) {
	data, src, err := readSource(file, "pricing/assumptions.yaml")
	if err != nil {
		return nil, fmt.Errorf("pricing: %w", err)
	}
	var p Pricing
	if err := decodeStrict(data, &p); err != nil {
		return nil, fmt.Errorf("pricing %s: %w", src, err)
	}
	var errs []string
	if p.APIVersion != APIVersion || p.Kind != "PricingAssumptions" {
		errs = append(errs, "apiVersion/kind must be "+APIVersion+"/PricingAssumptions")
	}
	ids := map[string]bool{}
	for _, r := range p.Rates {
		if ids[r.ID] {
			errs = append(errs, "duplicate rate id "+r.ID)
		}
		ids[r.ID] = true
		if !oneOf(r.Provider, []string{"aws", "gcp", "alibaba"}) || !oneOf(r.Kind, RateKinds) || r.USDPerGB < 0 || r.Source == "" {
			errs = append(errs, "rate "+r.ID+": invalid provider/kind/usd_per_gb/source")
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("pricing %s: %s", src, strings.Join(errs, "; "))
	}
	p.Digest = digest(data)
	p.Source = src
	return &p, nil
}
