package migration

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/graph"
	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

// Assessment is the output of `hermes migrate assess` (data-contracts §7).
type Assessment struct {
	APIVersion   string             `json:"apiVersion"`
	Kind         string             `json:"kind"`
	Metadata     AssessmentMetadata `json:"metadata"`
	Summary      AssessmentSummary  `json:"summary"`
	Resources    []AssessedResource `json:"resources"`
	Blockers     []Blocker          `json:"blockers"`
	Gaps         []inventory.Gap    `json:"gaps"`
	Dependencies Dependencies       `json:"dependencies"`
	DraftSpec    map[string]any     `json:"draft_spec"`
	SpecMapping  []SpecMapping      `json:"spec_mapping"`
	Unsupported  []UnsupportedItem  `json:"unsupported"`
}

// Endpoint names a side of the migration.
type Endpoint struct {
	Provider  string   `json:"provider"`
	AccountID string   `json:"account_id,omitempty"`
	Regions   []string `json:"regions,omitempty"`
	Region    string   `json:"region,omitempty"`
}

// AssessmentMetadata records provenance.
type AssessmentMetadata struct {
	GeneratedAt        string   `json:"generated_at"`
	Generator          string   `json:"generator"`
	Source             Endpoint `json:"source"`
	Target             Endpoint `json:"target"`
	InventoryDigest    string   `json:"inventory_digest"`
	InventoryCollector string   `json:"inventory_collector"`
	CatalogDigest      string   `json:"catalog_digest"`
	PricingDigest      string   `json:"pricing_digest"`
	Disclaimer         string   `json:"disclaimer"`
}

// AssessmentSummary aggregates the assessment.
type AssessmentSummary struct {
	ResourceCount     int            `json:"resource_count"`
	ByCompatibility   map[string]int `json:"by_compatibility"`
	ByComplexity      map[string]int `json:"by_complexity"`
	ByCategory        map[string]int `json:"by_category"`
	ByHermesSupport   map[string]int `json:"by_hermes_support"`
	PortabilityScore  float64        `json:"portability_score"`
	PortabilityMethod string         `json:"portability_method"`
	DataVolume        DataVolume     `json:"data_volume"`
	TransferCost      TransferCost   `json:"transfer_cost"`
	BlockerCount      int            `json:"blocker_count"`
	RiskCount         int            `json:"risk_count"`
	GapCount          int            `json:"gap_count"`
	WaveCount         int            `json:"wave_count"`
}

// DataVolume is the data that must (or may) move.
type DataVolume struct {
	RequiredBytes        int64            `json:"required_bytes"`
	OptionalBytes        int64            `json:"optional_bytes"`
	ByCategoryBytes      map[string]int64 `json:"by_category_bytes"`
	UnknownSizeResources []string         `json:"unknown_size_resources"`
	Basis                string           `json:"basis"`
}

// TransferCost is an indicative range, never a quote.
type TransferCost struct {
	Currency       string   `json:"currency"`
	LowUSD         float64  `json:"low_usd"`
	HighUSD        float64  `json:"high_usd"`
	Method         string   `json:"method"`
	RatesUsed      []Rate   `json:"rates_used"`
	ExitProgrammes []string `json:"exit_programmes"`
	Disclaimer     string   `json:"disclaimer"`
}

// AssessedResource is the classification of one source resource.
type AssessedResource struct {
	ID            string   `json:"id"`
	Type          string   `json:"type"`
	Name          string   `json:"name"`
	Region        string   `json:"region"`
	Category      string   `json:"category"`
	Capability    string   `json:"capability"`
	MappingID     string   `json:"mapping_id"`
	TargetService string   `json:"target_service"`
	Alternatives  []string `json:"alternatives,omitempty"`
	Compatibility string   `json:"compatibility"`
	Complexity    string   `json:"complexity"`
	DataMigration string   `json:"data_migration"`
	Workflow      string   `json:"workflow"`
	HermesSupport string   `json:"hermes_support"`
	LockIn        string   `json:"lock_in"`
	Completeness  string   `json:"completeness"`
	ManagedBy     string   `json:"managed_by"`
	Wave          int      `json:"wave"`
	SizeBytes     *int64   `json:"size_bytes,omitempty"`
	Notes         []string `json:"notes,omitempty"`
}

// Blocker is a finding that blocks or puts the migration at risk.
type Blocker struct {
	Code           string   `json:"code"`
	Severity       string   `json:"severity"` // blocker | risk | info
	ResourceIDs    []string `json:"resource_ids,omitempty"`
	Message        string   `json:"message"`
	Recommendation string   `json:"recommendation"`
}

// Dependencies are the graph-derived migration waves.
type Dependencies struct {
	Waves              []graph.Wave `json:"waves"`
	ExternalReferences []string     `json:"external_references"`
}

// Diagnostic codes used by the assessment (HCF4xx, documented in docs/migration).
const (
	CodeUnmapped      = "HCF401"
	CodeNoTarget      = "HCF402"
	CodeReArchitect   = "HCF403"
	CodeDiscoveryGaps = "HCF404"
	CodePartial       = "HCF405"
	CodeHighLockIn    = "HCF406"
	CodeUnknownSize   = "HCF407"
	CodeResourceRisk  = "HCF408"
)

// AssessOptions configures Assess.
type AssessOptions struct {
	Target    string // gcp | alibaba
	Generator string
	Project   string // draft spec project name
	Env       string // draft spec environment
	Now       func() time.Time
}

// Assess classifies every resource, derives waves, blockers, data volume, transfer cost
// range and the draft target spec.
func Assess(inv *inventory.Inventory, cat *Catalog, pricing *Pricing, o AssessOptions) (*Assessment, error) {
	if !oneOf(o.Target, Targets) {
		return nil, fmt.Errorf("assess: unknown target %q (want gcp or alibaba)", o.Target)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Generator == "" {
		o.Generator = "hermes"
	}
	g := graph.Build(inv)
	waves := g.Waves()
	waveOf := graph.WaveOf(waves)

	a := &Assessment{APIVersion: APIVersion, Kind: "Assessment"}
	a.Metadata = AssessmentMetadata{
		GeneratedAt: o.Now().UTC().Format(time.RFC3339), Generator: o.Generator,
		Source:          Endpoint{Provider: inv.Metadata.Provider, AccountID: inv.Metadata.AccountID, Regions: inv.Metadata.Regions},
		Target:          Endpoint{Provider: o.Target},
		InventoryDigest: inv.Digest(), InventoryCollector: inv.Metadata.Collector,
		CatalogDigest: cat.Digest, PricingDigest: pricing.Digest,
		Disclaimer: cat.Metadata.Disclaimer + " Cost figures are indicative ranges from pricing assumptions, not quotes. The draft spec requires human review.",
	}
	s := &a.Summary
	s.ByCompatibility, s.ByComplexity, s.ByCategory, s.ByHermesSupport = map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	s.DataVolume = DataVolume{ByCategoryBytes: map[string]int64{}, UnknownSizeResources: []string{},
		Basis: "inventory size_bytes: S3 from CloudWatch daily storage metrics, RDS/EBS provisioned storage (upper bound), DynamoDB table size"}

	var weighted, weights float64
	unmapped := []string{}
	for _, r := range inv.Resources {
		ar := AssessedResource{ID: r.ID, Type: r.Type, Name: r.Name, Region: r.Region, Completeness: r.Completeness,
			ManagedBy: r.ManagedBy, Wave: waveOf[r.ID], SizeBytes: r.SizeBytes}
		if e := cat.Match(r.Type, r.Attributes); e != nil {
			t := e.Targets[o.Target]
			ar.Category, ar.Capability, ar.MappingID = e.Category, e.Capability, e.ID
			ar.TargetService, ar.Alternatives = t.Service, t.Alternatives
			ar.Compatibility, ar.Complexity, ar.DataMigration = t.Compatibility, t.Complexity, t.DataMigration
			ar.Workflow, ar.HermesSupport, ar.LockIn = t.Workflow, t.HermesSupport, t.LockIn
			ar.Notes = append(ar.Notes, t.Issues...)
		} else {
			ar.Category, ar.Capability, ar.Compatibility, ar.Complexity = "other", "none", "unknown", "high"
			ar.DataMigration, ar.Workflow, ar.HermesSupport, ar.LockIn = "optional", "manual-review", "not-implemented", "medium"
			ar.Notes = append(ar.Notes, "Resource type not in the service catalog; classified for manual review")
			unmapped = append(unmapped, r.ID)
		}
		ar.Notes = append(ar.Notes, resourceRisks(&r, &ar, a)...)
		s.ByCompatibility[ar.Compatibility]++
		s.ByComplexity[ar.Complexity]++
		s.ByCategory[ar.Category]++
		s.ByHermesSupport[ar.HermesSupport]++

		// Portability score: weighted share of resources whose mapping is equivalent or
		// compatible AND inside the Hermes v1alpha1 provisioning scope (hermes_support:
		// implemented). Harder moves weigh more (low 1, medium 2, high 3).
		w := map[string]float64{"low": 1, "medium": 2, "high": 3}[ar.Complexity]
		weights += w
		if (ar.Compatibility == "equivalent" || ar.Compatibility == "compatible") && ar.HermesSupport == "implemented" {
			weighted += w
		}

		if ar.DataMigration == "required" || ar.DataMigration == "optional" {
			if r.SizeBytes != nil {
				if ar.DataMigration == "required" {
					s.DataVolume.RequiredBytes += *r.SizeBytes
				} else {
					s.DataVolume.OptionalBytes += *r.SizeBytes
				}
				s.DataVolume.ByCategoryBytes[ar.Category] += *r.SizeBytes
			} else if ar.DataMigration == "required" {
				s.DataVolume.UnknownSizeResources = append(s.DataVolume.UnknownSizeResources, r.ID)
			}
		}
		a.Resources = append(a.Resources, ar)
	}
	s.ResourceCount = len(a.Resources)
	if weights > 0 {
		s.PortabilityScore = math.Round(weighted/weights*1000) / 10
	}
	s.PortabilityMethod = "percentage of complexity-weighted resources (low=1, medium=2, high=3) whose mapping is equivalent/compatible and within the Hermes v1alpha1 provisioning scope (hermes_support: implemented)"
	s.TransferCost = transferCost(pricing, o.Target, s.DataVolume.RequiredBytes, s.DataVolume.OptionalBytes)

	a.addGroupedBlockers(unmapped, inv)
	a.Gaps = append([]inventory.Gap{}, inv.Gaps...)
	if len(inv.Gaps) > 0 {
		a.Blockers = append(a.Blockers, Blocker{Code: CodeDiscoveryGaps, Severity: "risk",
			Message:        fmt.Sprintf("%d discovery gap(s): parts of the source were not discovered or not fully inspected; undiscovered scope is migration risk", len(inv.Gaps)),
			Recommendation: "Triage every gap: grant the missing read-only permission and re-run discovery, or accept the gap explicitly"})
	}
	if len(s.DataVolume.UnknownSizeResources) > 0 {
		a.Blockers = append(a.Blockers, Blocker{Code: CodeUnknownSize, Severity: "risk", ResourceIDs: s.DataVolume.UnknownSizeResources,
			Message:        fmt.Sprintf("%d data-bearing resource(s) have no size; the transfer estimate excludes them", len(s.DataVolume.UnknownSizeResources)),
			Recommendation: "Allow cloudwatch:GetMetricData (S3 sizes) or measure the data volume manually"})
	}
	sortBlockers(a.Blockers)
	for _, b := range a.Blockers {
		switch b.Severity {
		case "blocker":
			s.BlockerCount++
		case "risk":
			s.RiskCount++
		}
	}
	s.GapCount = len(a.Gaps)

	var external []string
	for _, n := range g.Nodes() {
		if n.External {
			external = append(external, n.ID)
		}
	}
	sort.Strings(external)
	a.Dependencies = Dependencies{Waves: waves, ExternalReferences: external}
	if a.Dependencies.Waves == nil {
		a.Dependencies.Waves = []graph.Wave{}
	}
	if a.Dependencies.ExternalReferences == nil {
		a.Dependencies.ExternalReferences = []string{}
	}
	s.WaveCount = len(waves)

	d := BuildDraftSpec(inv, a, DraftOptions{Target: o.Target, Project: o.Project, Env: o.Env})
	a.DraftSpec, a.SpecMapping, a.Unsupported = d.Spec, d.Mapping, d.Unsupported
	if a.Blockers == nil {
		a.Blockers = []Blocker{}
	}
	return a, nil
}

// addGroupedBlockers adds catalog-driven findings grouped by kind.
func (a *Assessment) addGroupedBlockers(unmapped []string, inv *inventory.Inventory) {
	if len(unmapped) > 0 {
		a.Blockers = append(a.Blockers, Blocker{Code: CodeUnmapped, Severity: "risk", ResourceIDs: unmapped,
			Message:        fmt.Sprintf("%d resource(s) have a type that is not in the service catalog (workflow manual-review)", len(unmapped)),
			Recommendation: "Review each resource; extend migration/mappings/catalog.yaml when a type recurs"})
	}
	group := map[string][]string{}
	for _, r := range a.Resources {
		switch r.Compatibility {
		case "none":
			group[CodeNoTarget] = append(group[CodeNoTarget], r.ID)
		case "re-architect":
			group[CodeReArchitect] = append(group[CodeReArchitect], r.ID)
		}
		if r.Completeness != inventory.CompletenessFull {
			group[CodePartial] = append(group[CodePartial], r.ID)
		}
		if r.LockIn == "high" {
			group[CodeHighLockIn] = append(group[CodeHighLockIn], r.ID)
		}
	}
	add := func(code, sev, msg, rec string) {
		if ids := group[code]; len(ids) > 0 {
			a.Blockers = append(a.Blockers, Blocker{Code: code, Severity: sev, ResourceIDs: ids, Message: fmt.Sprintf(msg, len(ids)), Recommendation: rec})
		}
	}
	add(CodeNoTarget, "blocker", "%d resource(s) have no equivalent target service", "Decide per resource: keep on the source with hybrid connectivity, replace with portable software, or retire")
	add(CodeReArchitect, "risk", "%d resource(s) require re-architecture on the target", "Plan application changes; see the catalog differences for each mapping")
	add(CodePartial, "risk", "%d resource(s) were only partially inspected (a detail call failed)", "Grant the missing read-only permissions listed in gaps and re-run discovery")
	add(CodeHighLockIn, "info", "%d resource(s) use a high lock-in source service", "Expect application changes beyond configuration")
}

// resourceRisks returns per-resource notes and adds specific findings to the assessment.
func resourceRisks(r *inventory.Resource, ar *AssessedResource, a *Assessment) []string {
	var notes []string
	risk := func(sev, msg, rec string) {
		a.Blockers = append(a.Blockers, Blocker{Code: CodeResourceRisk, Severity: sev, ResourceIDs: []string{r.ID}, Message: msg, Recommendation: rec})
		notes = append(notes, msg)
	}
	switch r.Type {
	case "AWS::EC2::Instance":
		if v, ok := r.Attr("source_dest_check").(bool); ok && !v {
			risk("risk", fmt.Sprintf("Instance %s forwards traffic (source/destination check disabled): router, NAT or VPN relay", r.Name),
				"Treat as a network appliance on a separate dual-running track; never include it in a bulk cutover")
		}
		if r.AttrBool("container_host") {
			notes = append(notes, "Tags indicate a Docker/container host: candidate for the containerized compute target (image and ports must be confirmed)")
		}
	case "AWS::RDS::DBInstance", "AWS::RDS::DBCluster":
		if r.AttrBool("publicly_accessible") {
			risk("risk", fmt.Sprintf("Database %s is publicly accessible", r.Name), "Use private connectivity for replication and keep the target private")
		}
		if e := r.AttrString("engine"); strings.Contains(e, "mysql") || e == "aurora" || e == "mariadb" {
			risk("info", fmt.Sprintf("Database %s uses %s; Hermes v1alpha1 provisions PostgreSQL only", r.Name, e), "Provision the target database outside Hermes (see UNSUPPORTED.md)")
		}
	case "AWS::SQS::Queue":
		if r.AttrInt("message_retention_seconds") > 7*24*3600 {
			notes = append(notes, "Message retention exceeds 7 days; the portable maximum (Pub/Sub subscriptions, SMQ queues) is 7 days")
		}
		if r.AttrBool("fifo_queue") {
			risk("risk", fmt.Sprintf("Queue %s is FIFO; ordering/exactly-once semantics differ on the target", r.Name), "Use ordering keys (Pub/Sub) or redesign; test ordering assumptions")
		}
	case "AWS::EC2::SecurityGroup":
		for _, p := range r.AttrStrings("world_open_ports") {
			if p != "tcp/443-443" && p != "tcp/80-80" {
				risk("risk", fmt.Sprintf("Security group %s allows %s from the internet", r.Name, p), "Do not replicate broad internet ingress on the target; Hermes generates least-privilege rules from the spec")
				break
			}
		}
	case "AWS::KMS::Key":
		notes = append(notes, "KMS key material cannot be exported; data is re-encrypted with target keys during migration")
	case "AWS::IAM::Role":
		if r.AttrBool("trust_cross_account") {
			notes = append(notes, "Role trusts principals in another account; re-create cross-account access deliberately on the target")
		}
	}
	return notes
}

func sortBlockers(bs []Blocker) {
	rank := map[string]int{"blocker": 0, "risk": 1, "info": 2}
	sort.SliceStable(bs, func(i, j int) bool {
		if rank[bs[i].Severity] != rank[bs[j].Severity] {
			return rank[bs[i].Severity] < rank[bs[j].Severity]
		}
		return bs[i].Code < bs[j].Code
	})
}

// transferCost computes an indicative range: low = volume x cheapest listed AWS internet
// egress rate, high = volume (required + optional) x most expensive AWS egress rate plus the
// most expensive target transfer/replication service fee. Waiver programmes are listed as
// "may apply" and never subtracted.
func transferCost(p *Pricing, target string, required, optional int64) TransferCost {
	tc := TransferCost{Currency: "USD", RatesUsed: []Rate{}, ExitProgrammes: []string{},
		Disclaimer: p.Metadata.Disclaimer + " This is an indicative range, not a quote."}
	var egress, fees []Rate
	for _, r := range p.Rates {
		switch {
		case r.Provider == "aws" && r.Kind == "egress-internet":
			egress = append(egress, r)
		case r.Provider == target && (r.Kind == "transfer-service" || r.Kind == "replication-service"):
			fees = append(fees, r)
		}
	}
	if len(egress) == 0 {
		tc.Method = "no AWS egress-internet rate in the pricing assumptions; range not computed"
		return tc
	}
	sort.Slice(egress, func(i, j int) bool { return egress[i].USDPerGB < egress[j].USDPerGB })
	low, high := egress[0], egress[len(egress)-1]
	tc.RatesUsed = append(tc.RatesUsed, low)
	if high.ID != low.ID {
		tc.RatesUsed = append(tc.RatesUsed, high)
	}
	maxFee := 0.0
	if len(fees) > 0 {
		sort.Slice(fees, func(i, j int) bool { return fees[i].USDPerGB < fees[j].USDPerGB })
		f := fees[len(fees)-1]
		maxFee = f.USDPerGB
		tc.RatesUsed = append(tc.RatesUsed, f)
	}
	const gb = 1e9
	reqGB := float64(required) / gb
	allGB := float64(required+optional) / gb
	tc.LowUSD = math.Round(reqGB*low.USDPerGB*100) / 100
	tc.HighUSD = math.Round(allGB*(high.USDPerGB+maxFee)*100) / 100
	tc.Method = fmt.Sprintf("low = required data (%.1f GB) x cheapest listed AWS internet egress rate (%s); high = required+optional data (%.1f GB) x (highest AWS egress rate (%s) + highest %s transfer/replication fee). Free allowances, request charges and dual-running costs are not modelled.",
		reqGB, low.ID, allGB, high.ID, target)
	for _, n := range p.Notes {
		if strings.Contains(strings.ToLower(n), "exit") || strings.Contains(strings.ToLower(n), "data act") || strings.Contains(strings.ToLower(n), "waive") {
			tc.ExitProgrammes = append(tc.ExitProgrammes, "May apply (not subtracted): "+n)
		}
	}
	return tc
}

// Digest returns "sha256:<hex>" of the canonical JSON of the assessment.
func (a *Assessment) Digest() string {
	b, _ := json.Marshal(a)
	return digest(b)
}

// LoadAssessment reads an assessment JSON file.
func LoadAssessment(path string) (*Assessment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("assessment: %w", err)
	}
	var a Assessment
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("assessment %s: %w", path, err)
	}
	if a.APIVersion != APIVersion || a.Kind != "Assessment" {
		return nil, fmt.Errorf("assessment %s: not a %s Assessment", path, APIVersion)
	}
	return &a, nil
}
