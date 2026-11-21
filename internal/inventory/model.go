// Package inventory defines the provider-neutral Inventory document (data-contracts §3)
// produced by read-only discovery, plus the helpers shared by every collector and importer:
// normalization, digests, redaction, managed_by annotation and the AWS Config importer.
//
// The inventory never depends on IaC state: Terraform state and CloudFormation stacks are
// only used to annotate managed_by.
package inventory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Envelope constants.
const (
	APIVersion = "hermes/v1alpha1"
	Kind       = "Inventory"
)

// Collector values for metadata.collector.
const (
	CollectorAWSAPI           = "aws-api"
	CollectorAWSConfig        = "aws-config"
	CollectorResourceExplorer = "resource-explorer"
	CollectorFixture          = "fixture"
)

// managed_by values.
const (
	ManagedUnmanaged      = "unmanaged"
	ManagedTerraform      = "terraform"
	ManagedCloudFormation = "cloudformation"
	ManagedUnknown        = "unknown"
)

// completeness values.
const (
	CompletenessFull           = "full"
	CompletenessPartial        = "partial"
	CompletenessIdentifierOnly = "identifier-only"
)

// Relationship types (data-contracts §3). Every relationship points from the dependent
// resource to the resource it depends on, so the dependency graph can be built directly.
const (
	RelInVPC             = "in-vpc"
	RelInSubnet          = "in-subnet"
	RelUsesSecurityGroup = "uses-security-group"
	RelAllowsIngressFrom = "allows-ingress-from"
	RelRoutesTo          = "routes-to"
	RelEncryptedBy       = "encrypted-by"
	RelAssumesRole       = "assumes-role"
	RelUsesSecret        = "uses-secret"
	RelPublishesTo       = "publishes-to"
	RelSubscribesTo      = "subscribes-to"
	RelTargets           = "targets"
	RelReadsFrom         = "reads-from"
	RelWritesTo          = "writes-to"
	RelAttachedTo        = "attached-to"
	RelDependsOn         = "depends-on"
	EvidenceAPI          = "api"
	EvidenceInferred     = "inferred"
)

// RelationshipTypes lists every valid relationship type.
var RelationshipTypes = []string{RelInVPC, RelInSubnet, RelUsesSecurityGroup, RelAllowsIngressFrom, RelRoutesTo,
	RelEncryptedBy, RelAssumesRole, RelUsesSecret, RelPublishesTo, RelSubscribesTo, RelTargets, RelReadsFrom,
	RelWritesTo, RelAttachedTo, RelDependsOn}

// Gap reasons.
const (
	GapAccessDenied  = "access-denied"
	GapNotEnabled    = "not-enabled"
	GapOptInDisabled = "opt-in-region-disabled"
	GapThrottled     = "throttled"
	GapUnsupported   = "unsupported"
	GapError         = "error"
)

// Inventory is the discovery output.
type Inventory struct {
	APIVersion    string         `json:"apiVersion"`
	Kind          string         `json:"kind"`
	Metadata      Metadata       `json:"metadata"`
	Resources     []Resource     `json:"resources"`
	Relationships []Relationship `json:"relationships"`
	Gaps          []Gap          `json:"gaps"`
	Summary       Summary        `json:"summary"`
}

// Metadata describes how and where discovery ran. It never contains credentials.
type Metadata struct {
	Provider    string    `json:"provider"`
	GeneratedAt string    `json:"generated_at"`
	Generator   string    `json:"generator"`
	Collector   string    `json:"collector"`
	AccountID   string    `json:"account_id"`
	Regions     []string  `json:"regions"`
	ReadOnly    bool      `json:"read_only"`
	Identity    *Identity `json:"identity,omitempty"`
	Services    []string  `json:"services,omitempty"` // services that were in scope
	Redacted    bool      `json:"redacted,omitempty"`
	Notes       []string  `json:"notes,omitempty"`
}

// Identity is the principal that performed discovery (never credentials).
type Identity struct {
	PrincipalARN   string `json:"principal_arn"`
	RoleARN        string `json:"role_arn,omitempty"`
	ExternalIDUsed bool   `json:"external_id_used"`
}

// Resource is one discovered resource with normalized, non-secret attributes.
type Resource struct {
	ID                string            `json:"id"`
	Type              string            `json:"type"`
	Name              string            `json:"name"`
	Region            string            `json:"region"`
	Tags              map[string]string `json:"tags,omitempty"`
	ManagedBy         string            `json:"managed_by"`
	ManagedByEvidence string            `json:"managed_by_evidence,omitempty"`
	Completeness      string            `json:"completeness"`
	Attributes        map[string]any    `json:"attributes,omitempty"`
	SizeBytes         *int64            `json:"size_bytes,omitempty"`
}

// Relationship is a directed edge from a dependent resource to its dependency.
type Relationship struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Type     string `json:"type"`
	Evidence string `json:"evidence"`
}

// Gap records something that could not be discovered or fully inspected.
type Gap struct {
	Service    string `json:"service"`
	Region     string `json:"region"`
	Operation  string `json:"operation"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	Impact     string `json:"impact"`
	ResourceID string `json:"resource_id,omitempty"`
}

// Summary is derived by Finalize.
type Summary struct {
	ResourceCount  int            `json:"resource_count"`
	ByType         map[string]int `json:"by_type"`
	ByCompleteness map[string]int `json:"by_completeness,omitempty"`
	GapCount       int            `json:"gap_count"`
}

// New returns an empty inventory with the envelope filled in.
func New(provider, collector string) *Inventory {
	return &Inventory{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Provider: provider, Collector: collector, ReadOnly: true},
	}
}

// Attr returns an attribute value or nil.
func (r *Resource) Attr(key string) any {
	if r.Attributes == nil {
		return nil
	}
	return r.Attributes[key]
}

// AttrString returns a string attribute ("" when missing or not a string).
func (r *Resource) AttrString(key string) string {
	s, _ := r.Attr(key).(string)
	return s
}

// AttrBool returns a boolean attribute (false when missing).
func (r *Resource) AttrBool(key string) bool {
	b, _ := r.Attr(key).(bool)
	return b
}

// AttrInt returns a numeric attribute as int64 (0 when missing).
func (r *Resource) AttrInt(key string) int64 {
	switch v := r.Attr(key).(type) {
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

// AttrStrings returns a string-list attribute.
func (r *Resource) AttrStrings(key string) []string {
	switch v := r.Attr(key).(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Index returns resources keyed by id.
func (inv *Inventory) Index() map[string]*Resource {
	m := make(map[string]*Resource, len(inv.Resources))
	for i := range inv.Resources {
		m[inv.Resources[i].ID] = &inv.Resources[i]
	}
	return m
}

// Finalize normalizes the inventory: fills defaults, de-duplicates and sorts resources,
// relationships and gaps deterministically, drops self-edges and edges whose source is
// unknown, and recomputes the summary. Relationships may point at resources outside the
// inventory (for example AWS-managed keys or other accounts); the graph marks those external.
func (inv *Inventory) Finalize() {
	if inv.APIVersion == "" {
		inv.APIVersion = APIVersion
	}
	if inv.Kind == "" {
		inv.Kind = Kind
	}
	seen := map[string]int{}
	var res []Resource
	for _, r := range inv.Resources {
		if r.ID == "" {
			continue
		}
		if r.ManagedBy == "" {
			r.ManagedBy = ManagedUnmanaged
		}
		if r.Completeness == "" {
			r.Completeness = CompletenessFull
		}
		if r.Name == "" {
			r.Name = r.ID
		}
		if i, ok := seen[r.ID]; ok {
			// Same id reported twice (e.g. a global resource seen from two regions): merge attributes.
			for k, v := range r.Attributes {
				if res[i].Attributes == nil {
					res[i].Attributes = map[string]any{}
				}
				if _, exists := res[i].Attributes[k]; !exists {
					res[i].Attributes[k] = v
				}
			}
			if worseCompleteness(r.Completeness, res[i].Completeness) {
				res[i].Completeness = r.Completeness
			}
			continue
		}
		seen[r.ID] = len(res)
		res = append(res, r)
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].ID < res[j].ID })
	inv.Resources = res
	if inv.Resources == nil {
		inv.Resources = []Resource{}
	}

	type relKey struct{ f, t, k string }
	relSeen := map[relKey]int{}
	var rels []Relationship
	for _, rel := range inv.Relationships {
		if rel.From == "" || rel.To == "" || rel.From == rel.To {
			continue
		}
		if _, ok := seen[rel.From]; !ok {
			continue
		}
		if rel.Evidence == "" {
			rel.Evidence = EvidenceAPI
		}
		k := relKey{rel.From, rel.To, rel.Type}
		if i, ok := relSeen[k]; ok {
			if rel.Evidence == EvidenceAPI {
				rels[i].Evidence = EvidenceAPI
			}
			continue
		}
		relSeen[k] = len(rels)
		rels = append(rels, rel)
	}
	sort.SliceStable(rels, func(i, j int) bool {
		a, b := rels[i], rels[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Type < b.Type
	})
	inv.Relationships = rels
	if inv.Relationships == nil {
		inv.Relationships = []Relationship{}
	}

	type gapKey struct{ s, r, o, id, reason string }
	gapSeen := map[gapKey]bool{}
	var gaps []Gap
	for _, g := range inv.Gaps {
		k := gapKey{g.Service, g.Region, g.Operation, g.ResourceID, g.Reason}
		if gapSeen[k] {
			continue
		}
		gapSeen[k] = true
		gaps = append(gaps, g)
	}
	sort.SliceStable(gaps, func(i, j int) bool {
		a, b := gaps[i], gaps[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		return a.ResourceID < b.ResourceID
	})
	inv.Gaps = gaps
	if inv.Gaps == nil {
		inv.Gaps = []Gap{}
	}
	sort.Strings(inv.Metadata.Regions)
	inv.Metadata.Regions = uniqStrings(inv.Metadata.Regions)
	if inv.Metadata.Regions == nil {
		inv.Metadata.Regions = []string{}
	}
	inv.Metadata.ReadOnly = true

	inv.Summary = Summary{ResourceCount: len(inv.Resources), ByType: map[string]int{}, ByCompleteness: map[string]int{}, GapCount: len(inv.Gaps)}
	for _, r := range inv.Resources {
		inv.Summary.ByType[r.Type]++
		inv.Summary.ByCompleteness[r.Completeness]++
	}
}

func worseCompleteness(a, b string) bool {
	rank := map[string]int{CompletenessFull: 0, CompletenessPartial: 1, CompletenessIdentifierOnly: 2}
	return rank[a] > rank[b]
}

func uniqStrings(in []string) []string {
	var out []string
	for i, s := range in {
		if s == "" || (i > 0 && in[i-1] == s) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Marshal renders the inventory as indented JSON (stable key order).
func (inv *Inventory) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(inv); err != nil {
		return nil, fmt.Errorf("inventory: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// Digest returns "sha256:<hex>" of the canonical (compact) JSON encoding.
func (inv *Inventory) Digest() string {
	b, _ := json.Marshal(inv)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Decode reads an inventory document (JSON) strictly and validates its envelope and
// semantic invariants.
func Decode(r io.Reader) (*Inventory, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	dec.UseNumber()
	var inv Inventory
	if err := dec.Decode(&inv); err != nil {
		return nil, fmt.Errorf("inventory: decode: %w", err)
	}
	if err := inv.Check(); err != nil {
		return nil, err
	}
	normalizeNumbers(&inv)
	return &inv, nil
}

// Load reads an inventory file.
func Load(path string) (*Inventory, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("inventory: %w", err)
	}
	defer f.Close()
	inv, err := Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return inv, nil
}

// Check validates the envelope and the invariants a JSON Schema cannot express.
func (inv *Inventory) Check() error {
	var errs []string
	if inv.APIVersion != APIVersion {
		errs = append(errs, fmt.Sprintf("apiVersion must be %q", APIVersion))
	}
	if inv.Kind != Kind {
		errs = append(errs, fmt.Sprintf("kind must be %q", Kind))
	}
	if !inv.Metadata.ReadOnly {
		errs = append(errs, "metadata.read_only must be true (a writer that cannot guarantee read-only discovery must refuse)")
	}
	ids := map[string]bool{}
	for i, r := range inv.Resources {
		if r.ID == "" || r.Type == "" {
			errs = append(errs, fmt.Sprintf("resources[%d]: id and type are required", i))
		}
		if ids[r.ID] {
			errs = append(errs, fmt.Sprintf("resources[%d]: duplicate id %q", i, r.ID))
		}
		ids[r.ID] = true
	}
	valid := map[string]bool{}
	for _, t := range RelationshipTypes {
		valid[t] = true
	}
	for i, rel := range inv.Relationships {
		if !valid[rel.Type] {
			errs = append(errs, fmt.Sprintf("relationships[%d]: unknown type %q", i, rel.Type))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("inventory: invalid document:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// normalizeNumbers converts json.Number attribute values to int64 or float64 so that
// downstream code sees plain Go numbers.
func normalizeNumbers(inv *Inventory) {
	for i := range inv.Resources {
		for k, v := range inv.Resources[i].Attributes {
			inv.Resources[i].Attributes[k] = normalizeValue(v)
		}
	}
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
		f, _ := t.Float64()
		return f
	case []any:
		for i := range t {
			t[i] = normalizeValue(t[i])
		}
		return t
	case map[string]any:
		for k := range t {
			t[k] = normalizeValue(t[k])
		}
		return t
	}
	return v
}

// Int64 returns a pointer to v (helper for SizeBytes).
func Int64(v int64) *int64 { return &v }
