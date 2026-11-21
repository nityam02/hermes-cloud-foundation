package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

// The draft target spec is a plain struct rendered to YAML/JSON. It deliberately does not
// import internal/config: it is a proposal that `hermes validate` checks once written.

// DraftPlatform is the draft `kind: Platform` document.
type DraftPlatform struct {
	APIVersion string     `json:"apiVersion" yaml:"apiVersion"`
	Kind       string     `json:"kind" yaml:"kind"`
	Project    DProject   `json:"project" yaml:"project"`
	Cloud      DCloud     `json:"cloud" yaml:"cloud"`
	Network    DNetwork   `json:"network" yaml:"network"`
	Workloads  DWorkloads `json:"workloads" yaml:"workloads"`
}

type DProject struct {
	Name        string            `json:"name" yaml:"name"`
	Environment string            `json:"environment" yaml:"environment"`
	Labels      map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

type DCloud struct {
	Provider string `json:"provider" yaml:"provider"`
	Region   string `json:"region" yaml:"region"`
	Account  string `json:"account" yaml:"account"`
}

type DNetwork struct {
	CIDR  string `json:"cidr" yaml:"cidr"`
	Zones int    `json:"zones" yaml:"zones"`
	NAT   bool   `json:"nat" yaml:"nat"`
}

type DWorkloads struct {
	Compute       *DCompute       `json:"compute,omitempty" yaml:"compute,omitempty"`
	Database      *DDatabase      `json:"database,omitempty" yaml:"database,omitempty"`
	Storage       *DStorage       `json:"storage,omitempty" yaml:"storage,omitempty"`
	Messaging     *DMessaging     `json:"messaging,omitempty" yaml:"messaging,omitempty"`
	Secrets       *DSecrets       `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Observability *DObservability `json:"observability,omitempty" yaml:"observability,omitempty"`
}

type DCompute struct {
	Type     string     `json:"type" yaml:"type"`
	Services []DService `json:"services" yaml:"services"`
}

type DService struct {
	Name            string            `json:"name" yaml:"name"`
	Image           string            `json:"image" yaml:"image"`
	Port            int               `json:"port" yaml:"port"`
	CPU             float64           `json:"cpu" yaml:"cpu"`
	MemoryMB        int               `json:"memory_mb" yaml:"memory_mb"`
	MinInstances    int               `json:"min_instances" yaml:"min_instances"`
	MaxInstances    int               `json:"max_instances" yaml:"max_instances"`
	Public          bool              `json:"public" yaml:"public"`
	HealthCheckPath string            `json:"health_check_path,omitempty" yaml:"health_check_path,omitempty"`
	SecretEnv       map[string]string `json:"secret_env,omitempty" yaml:"secret_env,omitempty"`
	Access          *DAccess          `json:"access,omitempty" yaml:"access,omitempty"`
}

type DAccess struct {
	Database bool `json:"database,omitempty" yaml:"database,omitempty"`
}

type DDatabase struct {
	Enabled             bool     `json:"enabled" yaml:"enabled"`
	Engine              string   `json:"engine" yaml:"engine"`
	Version             string   `json:"version" yaml:"version"`
	Size                string   `json:"size" yaml:"size"`
	StorageGB           int      `json:"storage_gb" yaml:"storage_gb"`
	MaxStorageGB        int      `json:"max_storage_gb" yaml:"max_storage_gb"`
	HighAvailability    bool     `json:"high_availability" yaml:"high_availability"`
	BackupRetentionDays int      `json:"backup_retention_days" yaml:"backup_retention_days"`
	PointInTimeRecovery bool     `json:"point_in_time_recovery" yaml:"point_in_time_recovery"`
	DeletionProtection  *bool    `json:"deletion_protection,omitempty" yaml:"deletion_protection,omitempty"`
	Extensions          []string `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

type DStorage struct {
	Enabled bool      `json:"enabled" yaml:"enabled"`
	Buckets []DBucket `json:"buckets" yaml:"buckets"`
}

type DBucket struct {
	Name          string `json:"name" yaml:"name"`
	Versioning    bool   `json:"versioning" yaml:"versioning"`
	RetentionDays int    `json:"retention_days" yaml:"retention_days"`
}

type DMessaging struct {
	Enabled bool     `json:"enabled" yaml:"enabled"`
	Topics  []DTopic `json:"topics" yaml:"topics"`
}

type DTopic struct {
	Name           string `json:"name" yaml:"name"`
	RetentionHours int    `json:"retention_hours" yaml:"retention_hours"`
	Subscriptions  []DSub `json:"subscriptions" yaml:"subscriptions"`
}

type DSub struct {
	Name                string `json:"name" yaml:"name"`
	AckDeadlineSeconds  int    `json:"ack_deadline_seconds" yaml:"ack_deadline_seconds"`
	MaxDeliveryAttempts int    `json:"max_delivery_attempts" yaml:"max_delivery_attempts"`
}

type DSecrets struct {
	Enabled bool     `json:"enabled" yaml:"enabled"`
	Names   []string `json:"names" yaml:"names"`
}

type DObservability struct {
	Enabled          bool `json:"enabled" yaml:"enabled"`
	LogRetentionDays int  `json:"log_retention_days" yaml:"log_retention_days"`
	Metrics          bool `json:"metrics" yaml:"metrics"`
	Tracing          bool `json:"tracing" yaml:"tracing"`
}

// SpecMapping records where a source resource lands in the draft spec.
type SpecMapping struct {
	SourceID   string `json:"source_id"`
	SourceType string `json:"source_type"`
	SourceName string `json:"source_name"`
	TargetKind string `json:"target_kind"` // network|database|bucket|topic|subscription|secret|service|absorbed
	TargetName string `json:"target_name,omitempty"`
	TargetPath string `json:"target_path"`
	Review     bool   `json:"review,omitempty"`
	Note       string `json:"note,omitempty"`
}

// UnsupportedItem is a source resource Hermes cannot generate on the target.
type UnsupportedItem struct {
	SourceID           string   `json:"source_id"`
	SourceType         string   `json:"source_type"`
	SourceName         string   `json:"source_name"`
	Category           string   `json:"category"`
	Reason             string   `json:"reason"`
	RecommendedService string   `json:"recommended_service"`
	Alternatives       []string `json:"alternatives,omitempty"`
	Suggestion         string   `json:"suggestion"`
}

// DraftOptions configures BuildDraftSpec.
type DraftOptions struct {
	Target  string
	Project string
	Env     string
}

// DraftResult is the draft spec plus its mapping tables.
type DraftResult struct {
	Platform    DraftPlatform
	Spec        map[string]any
	Mapping     []SpecMapping
	Unsupported []UnsupportedItem
}

var (
	nonAlnum   = regexp.MustCompile(`[^a-z0-9]+`)
	nameRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,18}[a-z0-9]$`)
	projectRe  = regexp.MustCompile(`^[a-z][a-z0-9-]{1,28}[a-z0-9]$`)
	envRe      = regexp.MustCompile(`^[a-z][a-z0-9]{1,11}$`)
	envVarRe   = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	cpuChoices = []float64{0.25, 0.5, 1, 2, 4}
)

// SanitizeName converts a source name to a spec name of at most maxLen characters
// (lowercase letters, digits, hyphens; starts with a letter). Long names are truncated and
// suffixed with 6 hex chars of sha256(original) so they stay unique and stable.
func SanitizeName(s string, maxLen int) string {
	n := strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if n == "" {
		n = "res"
	}
	if n[0] < 'a' || n[0] > 'z' {
		n = "r-" + n
	}
	if len(n) > maxLen {
		sum := sha256.Sum256([]byte(s))
		n = strings.TrimRight(n[:maxLen-7], "-") + "-" + hex.EncodeToString(sum[:])[:6]
	}
	if len(n) < 2 {
		n += "0"
	}
	return n
}

type namer struct{ used map[string]bool }

func (nm *namer) take(s string, maxLen int) string {
	if nm.used == nil {
		nm.used = map[string]bool{}
	}
	base := SanitizeName(s, maxLen)
	n := base
	for i := 2; nm.used[n]; i++ {
		suf := "-" + strconv.Itoa(i)
		cut := base
		if len(cut)+len(suf) > maxLen {
			cut = strings.TrimRight(cut[:maxLen-len(suf)], "-")
		}
		n = cut + suf
	}
	nm.used[n] = true
	return n
}

// regionMap is a nearest-by-geography default; latency, data residency and service
// availability must be reviewed.
var regionMap = map[string]map[string]string{
	"gcp": {
		"us-east-1": "us-east4", "us-east-2": "us-east5", "us-west-1": "us-west2", "us-west-2": "us-west1",
		"ca-central-1": "northamerica-northeast1", "sa-east-1": "southamerica-east1",
		"eu-west-1": "europe-west1", "eu-west-2": "europe-west2", "eu-west-3": "europe-west9", "eu-central-1": "europe-west3",
		"eu-central-2": "europe-west6", "eu-north-1": "europe-north1", "eu-south-1": "europe-west8", "eu-south-2": "europe-southwest1",
		"ap-south-1": "asia-south1", "ap-southeast-1": "asia-southeast1", "ap-southeast-2": "australia-southeast1",
		"ap-southeast-3": "asia-southeast2", "ap-northeast-1": "asia-northeast1", "ap-northeast-2": "asia-northeast3",
		"ap-northeast-3": "asia-northeast2", "ap-east-1": "asia-east2", "me-central-1": "me-central1",
	},
	"alibaba": {
		"us-east-1": "us-east-1", "us-east-2": "us-east-1", "us-west-1": "us-west-1", "us-west-2": "us-west-1",
		"eu-west-1": "eu-west-1", "eu-west-2": "eu-west-1", "eu-west-3": "eu-central-1", "eu-central-1": "eu-central-1",
		"eu-north-1": "eu-central-1", "eu-south-1": "eu-central-1", "ap-southeast-1": "ap-southeast-1",
		"ap-southeast-3": "ap-southeast-5", "ap-northeast-1": "ap-northeast-1", "ap-northeast-2": "ap-northeast-2",
		"ap-south-1": "ap-southeast-1", "ap-east-1": "cn-hongkong", "me-central-1": "me-east-1",
	},
}

var defaultRegion = map[string]string{"gcp": "europe-west1", "alibaba": "eu-central-1"}

type draft struct {
	inv      *inventory.Inventory
	idx      map[string]*inventory.Resource
	ar       map[string]*AssessedResource
	o        DraftOptions
	p        DraftPlatform
	mapping  []SpecMapping
	unsup    []UnsupportedItem
	covered  map[string]bool
	notes    []string
	relsFrom map[string][]inventory.Relationship
	relsTo   map[string][]inventory.Relationship
}

// BuildDraftSpec derives a draft Platform spec for the target from the inventory and
// assessment. Every source resource ends up in exactly one of Mapping or Unsupported.
func BuildDraftSpec(inv *inventory.Inventory, a *Assessment, o DraftOptions) DraftResult {
	if !projectRe.MatchString(o.Project) {
		o.Project = "migrated-platform"
	}
	if !envRe.MatchString(o.Env) {
		o.Env = "production"
	}
	d := &draft{inv: inv, idx: inv.Index(), ar: map[string]*AssessedResource{}, o: o, covered: map[string]bool{},
		relsFrom: map[string][]inventory.Relationship{}, relsTo: map[string][]inventory.Relationship{}}
	for i := range a.Resources {
		d.ar[a.Resources[i].ID] = &a.Resources[i]
	}
	for _, r := range inv.Relationships {
		d.relsFrom[r.From] = append(d.relsFrom[r.From], r)
		d.relsTo[r.To] = append(d.relsTo[r.To], r)
	}
	d.p = DraftPlatform{APIVersion: APIVersion, Kind: "Platform",
		Project: DProject{Name: o.Project, Environment: o.Env, Labels: map[string]string{"migrated-from": "aws"}},
		Cloud:   DCloud{Provider: o.Target, Region: d.region()}}
	d.network()
	d.database()
	d.storage()
	d.messaging()
	d.secrets()
	d.compute()
	d.observability()
	d.rest()
	sort.Slice(d.mapping, func(i, j int) bool {
		if d.mapping[i].TargetPath != d.mapping[j].TargetPath {
			return d.mapping[i].TargetPath < d.mapping[j].TargetPath
		}
		return d.mapping[i].SourceID < d.mapping[j].SourceID
	})
	sort.Slice(d.unsup, func(i, j int) bool {
		if d.unsup[i].Category != d.unsup[j].Category {
			return d.unsup[i].Category < d.unsup[j].Category
		}
		return d.unsup[i].SourceID < d.unsup[j].SourceID
	})
	res := DraftResult{Platform: d.p, Mapping: d.mapping, Unsupported: d.unsup}
	if res.Mapping == nil {
		res.Mapping = []SpecMapping{}
	}
	if res.Unsupported == nil {
		res.Unsupported = []UnsupportedItem{}
	}
	b, _ := json.Marshal(d.p)
	_ = json.Unmarshal(b, &res.Spec)
	return res
}

func (d *draft) mapTo(r *inventory.Resource, kind, name, path, note string, review bool) {
	d.covered[r.ID] = true
	d.mapping = append(d.mapping, SpecMapping{SourceID: r.ID, SourceType: r.Type, SourceName: r.Name, TargetKind: kind, TargetName: name, TargetPath: path, Review: review, Note: note})
}

func (d *draft) unsupported(r *inventory.Resource, reason string) {
	d.covered[r.ID] = true
	ar := d.ar[r.ID]
	item := UnsupportedItem{SourceID: r.ID, SourceType: r.Type, SourceName: r.Name, Reason: reason}
	if ar != nil {
		item.Category, item.RecommendedService, item.Alternatives = ar.Category, ar.TargetService, ar.Alternatives
	}
	if item.RecommendedService == "" {
		item.RecommendedService = "manual review"
	}
	item.Suggestion = suggestion(d.o.Target, item.Category)
	d.unsup = append(d.unsup, item)
}

func (d *draft) ofType(types ...string) []*inventory.Resource {
	var out []*inventory.Resource
	for i := range d.inv.Resources {
		r := &d.inv.Resources[i]
		for _, t := range types {
			if r.Type == t && !d.covered[r.ID] {
				out = append(out, r)
			}
		}
	}
	return out
}

func (d *draft) region() string {
	counts := map[string]int{}
	for _, r := range d.inv.Resources {
		if r.Region != "" && r.Region != "global" && r.Region != "unknown" {
			counts[r.Region]++
		}
	}
	best, n := "", -1
	for reg, c := range counts {
		if c > n || (c == n && reg < best) {
			best, n = reg, c
		}
	}
	if t, ok := regionMap[d.o.Target][best]; ok {
		return t
	}
	return defaultRegion[d.o.Target]
}

func (d *draft) network() {
	vpcs := d.ofType("AWS::EC2::VPC")
	d.p.Network = DNetwork{CIDR: "10.20.0.0/16", Zones: 2, NAT: true}
	if len(vpcs) == 0 {
		return
	}
	// Primary VPC: the one most resources live in.
	members := map[string]int{}
	for _, rel := range d.inv.Relationships {
		if rel.Type == inventory.RelInVPC {
			members[rel.To]++
		}
	}
	sort.SliceStable(vpcs, func(i, j int) bool {
		if members[vpcs[i].ID] != members[vpcs[j].ID] {
			return members[vpcs[i].ID] > members[vpcs[j].ID]
		}
		return vpcs[i].ID < vpcs[j].ID
	})
	primary := vpcs[0]
	var sourceCIDRs []*net.IPNet
	for _, v := range vpcs {
		for _, c := range append([]string{v.AttrString("cidr_block")}, v.AttrStrings("secondary_cidr_blocks")...) {
			if _, n, err := net.ParseCIDR(c); err == nil {
				sourceCIDRs = append(sourceCIDRs, n)
			}
		}
	}
	prefix := 16
	if _, n, err := net.ParseCIDR(primary.AttrString("cidr_block")); err == nil {
		prefix, _ = n.Mask.Size()
	}
	if prefix < 16 {
		prefix = 16
	}
	if prefix > 20 {
		prefix = 20
	}
	cidr := nonOverlapping(prefix, sourceCIDRs)
	azs := map[string]bool{}
	nat := false
	for _, r := range d.inv.Resources {
		if r.AttrString("vpc_id") == primary.AttrString("resource_id") {
			switch r.Type {
			case "AWS::EC2::Subnet":
				azs[r.AttrString("availability_zone")] = true
			case "AWS::EC2::NatGateway":
				nat = true
			}
		}
	}
	zones := len(azs)
	if zones < 2 {
		zones = 2
	}
	if zones > 3 {
		zones = 3
	}
	d.p.Network = DNetwork{CIDR: cidr, Zones: zones, NAT: nat}
	d.mapTo(primary, "network", d.o.Project, "network",
		fmt.Sprintf("source %s -> target %s: same size, chosen not to overlap any source VPC so hybrid connectivity can route between them; %d zone(s), nat=%v", primary.AttrString("cidr_block"), cidr, zones, nat), false)
	for _, v := range vpcs[1:] {
		d.unsupported(v, "Hermes v1alpha1 creates one network per environment; additional VPCs need a separate environment or manual design")
	}
	pid := primary.AttrString("resource_id")
	for _, r := range d.ofType("AWS::EC2::Subnet", "AWS::EC2::RouteTable", "AWS::EC2::InternetGateway", "AWS::EC2::NatGateway", "AWS::EC2::SecurityGroup") {
		inPrimary := r.AttrString("vpc_id") == pid || contains(r.AttrStrings("attached_vpc_ids"), pid)
		if !inPrimary {
			continue
		}
		note := "absorbed into the network block (subnets per zone, routing and NAT are generated by the provider module)"
		if r.Type == "AWS::EC2::SecurityGroup" {
			note = "rules are not translated; Hermes generates least-privilege firewall rules from the spec (review source rules for anything still needed)"
		}
		d.mapTo(r, "absorbed", "", "network", note, r.Type == "AWS::EC2::SecurityGroup")
	}
}

func nonOverlapping(prefix int, used []*net.IPNet) string {
	overlaps := func(n *net.IPNet) bool {
		for _, u := range used {
			if u.Contains(n.IP) || n.Contains(u.IP) {
				return true
			}
		}
		return false
	}
	var cands []string
	for b := 20; b < 256; b++ {
		cands = append(cands, fmt.Sprintf("10.%d.0.0/%d", b, prefix))
	}
	for b := 0; b < 20; b++ {
		cands = append(cands, fmt.Sprintf("10.%d.0.0/%d", b, prefix))
	}
	for b := 16; b < 32; b++ {
		cands = append(cands, fmt.Sprintf("172.%d.0.0/%d", b, prefix))
	}
	for _, c := range cands {
		if _, n, err := net.ParseCIDR(c); err == nil && !overlaps(n) {
			return n.String()
		}
	}
	return "10.20.0.0/16"
}

func dbSize(class string) string {
	parts := strings.Split(class, ".")
	size := parts[len(parts)-1]
	switch {
	case size == "micro" || size == "small" || size == "nano":
		return "small"
	case size == "medium" || size == "large":
		return "medium"
	case size == "xlarge" || size == "2xlarge":
		return "large"
	case strings.HasSuffix(size, "xlarge"):
		return "xlarge"
	}
	return "small"
}

func (d *draft) database() {
	var pg []*inventory.Resource
	for _, r := range d.ofType("AWS::RDS::DBInstance", "AWS::RDS::DBCluster") {
		eng := r.AttrString("engine")
		if r.Type == "AWS::RDS::DBInstance" && r.AttrString("cluster_identifier") != "" {
			continue // cluster members are represented by their cluster
		}
		if r.AttrString("read_replica_source") != "" {
			d.mapTo(r, "absorbed", "", "workloads.database", "read replica of a migrated database; re-create replicas on the target if needed", true)
			continue
		}
		if eng == "postgres" || eng == "aurora-postgresql" {
			pg = append(pg, r)
		}
	}
	if len(pg) == 0 {
		return
	}
	sort.SliceStable(pg, func(i, j int) bool {
		return pg[i].AttrInt("allocated_storage_gb") > pg[j].AttrInt("allocated_storage_gb")
	})
	r := pg[0]
	ver := r.AttrString("engine_version")
	major := strings.SplitN(ver, ".", 2)[0]
	storage := int(r.AttrInt("allocated_storage_gb"))
	note := []string{fmt.Sprintf("%s %s -> PostgreSQL %s", r.AttrString("engine"), ver, major)}
	if storage < 10 {
		if r.Type == "AWS::RDS::DBCluster" {
			note = append(note, "Aurora storage is not provisioned; storage_gb set to 20, size it from the actual database size")
			storage = 20
		} else {
			storage = 10
		}
	}
	maxStorage := int(r.AttrInt("max_allocated_storage_gb"))
	if maxStorage < storage {
		maxStorage = storage
	}
	retention := int(r.AttrInt("backup_retention_days"))
	if retention < 1 {
		note = append(note, "source has automated backups disabled; target retention set to 7 days")
		retention = 7
	}
	if retention > 35 {
		retention = 35
	}
	db := &DDatabase{Enabled: true, Engine: "postgresql", Version: major, Size: dbSize(r.AttrString("instance_class")),
		StorageGB: storage, MaxStorageGB: maxStorage, HighAvailability: r.AttrBool("multi_az"),
		BackupRetentionDays: retention, PointInTimeRecovery: true}
	if v, ok := r.Attr("deletion_protection").(bool); ok {
		db.DeletionProtection = &v
	}
	pgs := append(r.AttrStrings("parameter_groups"), r.AttrString("parameter_group"))
	for _, g := range pgs {
		if strings.Contains(strings.ToLower(g), "postgis") {
			db.Extensions = []string{"postgis"}
			note = append(note, "parameter group "+g+" suggests PostGIS; confirm installed extensions with SELECT extname FROM pg_extension")
		}
	}
	note = append(note, fmt.Sprintf("size %s from instance class %s (heuristic)", db.Size, r.AttrString("instance_class")))
	d.p.Workloads.Database = db
	d.mapTo(r, "database", "database", "workloads.database", strings.Join(note, "; "), false)
	for _, other := range pg[1:] {
		d.unsupported(other, "Hermes v1alpha1 provisions one database per environment; migrate this database to a second environment or provision it manually")
	}
	for _, sg := range d.ofType("AWS::RDS::DBSubnetGroup") {
		d.mapTo(sg, "absorbed", "", "workloads.database", "subnet placement is generated by the provider module", false)
	}
}

func (d *draft) storage() {
	buckets := d.ofType("AWS::S3::Bucket")
	if len(buckets) == 0 {
		return
	}
	st := &DStorage{Enabled: true}
	var nm namer
	for _, b := range buckets {
		name := nm.take(b.Name, 20)
		st.Buckets = append(st.Buckets, DBucket{Name: name, Versioning: b.AttrString("versioning") == "Enabled", RetentionDays: 0})
		var notes []string
		if name != b.Name {
			notes = append(notes, "renamed to fit the spec pattern; the native name gets the <project>-<env>- prefix and a random suffix")
		}
		if b.AttrInt("lifecycle_rule_count") > 0 {
			notes = append(notes, "source has lifecycle rules: set retention_days if objects expire")
		}
		if b.AttrBool("replication_enabled") {
			notes = append(notes, "source replicates to another bucket; replication is not generated")
		}
		d.mapTo(b, "bucket", name, "workloads.storage.buckets", strings.Join(notes, "; "), false)
	}
	d.p.Workloads.Storage = st
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (d *draft) messaging() {
	topics := d.ofType("AWS::SNS::Topic")
	queues := map[string]*inventory.Resource{}
	for _, q := range d.ofType("AWS::SQS::Queue") {
		queues[q.ID] = q
	}
	if len(topics) == 0 && len(queues) == 0 {
		return
	}
	var topicNames, subNames namer
	m := &DMessaging{Enabled: true}
	dlqs := map[string]bool{}
	for _, q := range queues {
		if rp, ok := q.Attr("redrive_policy").(map[string]any); ok {
			if t, _ := rp["dead_letter_target_arn"].(string); t != "" {
				dlqs[t] = true
			}
		}
	}
	subscribed := map[string]bool{}
	sub := func(q *inventory.Resource) DSub {
		ack := clamp(int(q.AttrInt("visibility_timeout_seconds")), 10, 600)
		mda := 0
		if rp, ok := q.Attr("redrive_policy").(map[string]any); ok {
			switch n := rp["max_receive_count"].(type) {
			case int64:
				mda = clamp(int(n), 5, 100)
			case float64:
				mda = clamp(int(n), 5, 100)
			}
		}
		return DSub{Name: subNames.take(q.Name, 20), AckDeadlineSeconds: ack, MaxDeliveryAttempts: mda}
	}
	retention := func(qs []*inventory.Resource) int {
		h := 168
		for _, q := range qs {
			if s := q.AttrInt("message_retention_seconds"); s > 0 && int(s/3600) < h {
				h = int(s / 3600)
			}
		}
		return clamp(h, 1, 168)
	}
	for _, t := range topics {
		var qs []*inventory.Resource
		for _, rel := range d.relsTo[t.ID] {
			if q, ok := queues[rel.From]; ok && rel.Type == inventory.RelSubscribesTo {
				qs = append(qs, q)
			}
		}
		sort.Slice(qs, func(i, j int) bool { return qs[i].ID < qs[j].ID })
		tn := topicNames.take(t.Name, 20)
		dt := DTopic{Name: tn, RetentionHours: retention(qs), Subscriptions: []DSub{}}
		d.mapTo(t, "topic", tn, "workloads.messaging.topics", "", false)
		for _, q := range qs {
			s := sub(q)
			dt.Subscriptions = append(dt.Subscriptions, s)
			subscribed[q.ID] = true
			note := "SQS subscription to " + t.Name
			if q.AttrBool("fifo_queue") {
				note += "; FIFO semantics differ (use ordering keys)"
			}
			d.mapTo(q, "subscription", s.Name, "workloads.messaging.topics.subscriptions", note, q.AttrBool("fifo_queue"))
		}
		if len(qs) == 0 {
			d.notes = append(d.notes, "topic "+t.Name+" has no SQS subscriptions; other protocols (email, Lambda, HTTP) are re-created manually")
		}
		m.Topics = append(m.Topics, dt)
	}
	var standalone []*inventory.Resource
	for _, q := range queues {
		if !subscribed[q.ID] {
			standalone = append(standalone, q)
		}
	}
	sort.Slice(standalone, func(i, j int) bool { return standalone[i].ID < standalone[j].ID })
	for _, q := range standalone {
		if dlqs[q.ID] {
			d.mapTo(q, "absorbed", "", "workloads.messaging", "dead-letter queue: represented by max_delivery_attempts dead-lettering on the target subscription", false)
			continue
		}
		tn := topicNames.take(q.Name, 20)
		s := sub(q)
		s.Name = subNames.take(q.Name+"-sub", 20)
		m.Topics = append(m.Topics, DTopic{Name: tn, RetentionHours: retention([]*inventory.Resource{q}), Subscriptions: []DSub{s}})
		note := "standalone SQS queue -> topic " + tn + " with one subscription; producers publish to the topic, consumers pull from the subscription"
		if q.AttrBool("fifo_queue") {
			note += "; FIFO semantics differ (use ordering keys)"
		}
		d.mapTo(q, "subscription", s.Name, "workloads.messaging.topics.subscriptions", note, true)
	}
	d.p.Workloads.Messaging = m
}

func (d *draft) secrets() {
	secrets := d.ofType("AWS::SecretsManager::Secret")
	if len(secrets) == 0 {
		return
	}
	var nm namer
	s := &DSecrets{Enabled: true, Names: []string{}}
	for _, sec := range secrets {
		if owner := sec.AttrString("owning_service"); owner != "" {
			d.mapTo(sec, "absorbed", "", "workloads.secrets", "managed by "+owner+" on the source (for example an RDS master password); the target service manages its own credentials", false)
			continue
		}
		n := nm.take(sec.Name, 40)
		s.Names = append(s.Names, n)
		d.mapTo(sec, "secret", n, "workloads.secrets.names", "container only; copy the value out-of-band (never through Hermes)", false)
	}
	d.p.Workloads.Secrets = s
}

func (d *draft) secretNameFor(ref string) string {
	for _, m := range d.mapping {
		if m.TargetKind == "secret" && (m.SourceID == ref || strings.HasPrefix(m.SourceID, ref+"-")) {
			return m.TargetName
		}
	}
	return ""
}

func (d *draft) compute() {
	svcs := d.ofType("AWS::ECS::Service")
	hosts := []*inventory.Resource{}
	for _, in := range d.ofType("AWS::EC2::Instance") {
		if in.AttrBool("container_host") {
			hosts = append(hosts, in)
		}
	}
	clusters := d.ofType("AWS::ECS::Cluster")
	if len(svcs) == 0 && len(hosts) == 0 && len(clusters) == 0 {
		return
	}
	c := &DCompute{Type: "containerized", Services: []DService{}}
	var nm namer
	dbSGs := map[string]bool{}
	if d.p.Workloads.Database != nil {
		for _, m := range d.mapping {
			if m.TargetKind == "database" {
				for _, rel := range d.relsFrom[m.SourceID] {
					if rel.Type == inventory.RelUsesSecurityGroup {
						for _, in := range d.relsFrom[rel.To] {
							if in.Type == inventory.RelAllowsIngressFrom {
								dbSGs[in.To] = true
							}
						}
					}
				}
			}
		}
	}
	for _, sv := range svcs {
		td := d.idx[sv.AttrString("task_definition")]
		ds := DService{Name: nm.take(sv.Name, 20), Port: 8080, CPU: 0.5, MemoryMB: 1024}
		var notes []string
		review := false
		var main map[string]any
		if td != nil {
			if cs := asMaps(td.Attr("containers")); len(cs) > 0 {
				for _, m := range cs {
					if e, _ := m["essential"].(bool); main == nil && (e || len(cs) == 1) {
						main = m
					}
				}
				if len(cs) > 1 {
					notes = append(notes, fmt.Sprintf("task has %d containers; only the main container is mapped (sidecars need review)", len(cs)))
					review = true
				}
			}
			if cpu, err := strconv.ParseFloat(td.AttrString("cpu"), 64); err == nil && cpu > 0 {
				ds.CPU = snapCPU(cpu / 1024)
			}
			if mem, err := strconv.Atoi(td.AttrString("memory")); err == nil && mem > 0 {
				ds.MemoryMB = clamp(mem, 512, 16384)
			}
			d.mapTo(td, "absorbed", "", "workloads.compute.services", "task definition of service "+sv.Name, false)
		} else {
			notes = append(notes, "task definition not inventoried: image, CPU and memory are placeholders")
			review = true
		}
		if main != nil {
			ds.Image, _ = main["image"].(string)
			if ports := asMaps(main["port_mappings"]); len(ports) > 0 {
				if p, ok := ports[0]["container_port"].(int64); ok {
					ds.Port = int(p)
				}
			} else {
				notes = append(notes, "no container port: port defaulted to 8080")
			}
			if ns := asStrings(main["environment_names"]); len(ns) > 0 {
				notes = append(notes, "environment variable values must be supplied in env: "+strings.Join(ns, ", "))
				review = true
			}
			if secs := asMaps(main["secrets"]); len(secs) > 0 {
				for _, m := range secs {
					env, _ := m["name"].(string)
					ref, _ := m["value_from"].(string)
					sn := d.secretNameFor(ref)
					if sn == "" || !envVarRe.MatchString(env) || strings.HasPrefix(env, "HERMES_") || strings.HasPrefix(env, "OTEL_") {
						notes = append(notes, "secret "+env+" not mapped (unknown secret or reserved name)")
						review = true
						continue
					}
					if ds.SecretEnv == nil {
						ds.SecretEnv = map[string]string{}
					}
					ds.SecretEnv[env] = sn
				}
			}
		}
		if ds.Image == "" {
			ds.Image = "registry.example.com/review-required/" + ds.Name + ":0.0.0"
			review = true
		}
		desired := int(sv.AttrInt("desired_count"))
		ds.MinInstances = clamp(desired, 0, 100)
		ds.MaxInstances = clamp(desired*2, 1, 100)
		if ds.MaxInstances < ds.MinInstances {
			ds.MaxInstances = ds.MinInstances
		}
		ds.Public, ds.HealthCheckPath = d.exposure(sv.ID)
		for _, g := range sv.AttrStrings("security_group_ids") {
			if dbSGs[d.sgARN(sv, g)] {
				ds.Access = &DAccess{Database: true}
			}
		}
		c.Services = append(c.Services, ds)
		d.mapTo(sv, "service", ds.Name, "workloads.compute.services", strings.Join(notes, "; "), review)
	}
	for _, h := range hosts {
		ds := DService{Name: nm.take(h.Name, 20), Image: "registry.example.com/review-required/" + SanitizeName(h.Name, 40) + ":0.0.0",
			Port: 8080, CPU: 1, MemoryMB: 2048, MinInstances: 1, MaxInstances: 2}
		c.Services = append(c.Services, ds)
		d.mapTo(h, "service", ds.Name, "workloads.compute.services",
			"REVIEW: EC2-hosted container workload (tags indicate Docker); image, port, CPU/memory and environment are placeholders to replace", true)
	}
	for _, cl := range clusters {
		d.mapTo(cl, "absorbed", "", "workloads.compute", "container runtime is generated by the provider module", false)
	}
	d.p.Workloads.Compute = c
}

func (d *draft) sgARN(sv *inventory.Resource, sgID string) string {
	parts := strings.SplitN(sv.ID, ":", 6)
	if len(parts) < 5 {
		return sgID
	}
	return fmt.Sprintf("arn:%s:ec2:%s:%s:security-group/%s", parts[1], parts[3], parts[4], sgID)
}

// exposure reports whether a service sits behind an internet-facing load balancer, and the
// target group health check path.
func (d *draft) exposure(serviceID string) (bool, string) {
	public, path := false, ""
	for _, rel := range d.relsTo[serviceID] {
		if rel.Type != inventory.RelTargets {
			continue
		}
		tg := d.idx[rel.From]
		if tg == nil || tg.Type != "AWS::ElasticLoadBalancingV2::TargetGroup" {
			continue
		}
		if p := tg.AttrString("health_check_path"); p != "" {
			path = p
		}
		for _, lbARN := range tg.AttrStrings("load_balancer_arns") {
			if lb := d.idx[lbARN]; lb != nil && lb.AttrString("scheme") == "internet-facing" {
				public = true
			}
		}
	}
	return public, path
}

func snapCPU(v float64) float64 {
	for _, c := range cpuChoices {
		if v <= c+1e-9 {
			return c
		}
	}
	return cpuChoices[len(cpuChoices)-1]
}

func (d *draft) observability() {
	counts := map[int]int{}
	groups := d.ofType("AWS::Logs::LogGroup")
	for _, g := range groups {
		if v := int(g.AttrInt("retention_days")); v > 0 {
			counts[v]++
		}
	}
	ret, n := 30, 0
	for v, c := range counts {
		if c > n || (c == n && v > ret) {
			ret, n = v, c
		}
	}
	d.p.Workloads.Observability = &DObservability{Enabled: true, LogRetentionDays: clamp(ret, 1, 3650), Metrics: true, Tracing: true}
	for _, g := range groups {
		d.mapTo(g, "absorbed", "", "workloads.observability", "log data is not migrated; retention mapped to log_retention_days", false)
	}
}

// rest classifies every resource not yet mapped: within v1alpha1 scope it is absorbed by its
// capability block; otherwise it is listed as unsupported.
func (d *draft) rest() {
	for i := range d.inv.Resources {
		r := &d.inv.Resources[i]
		if d.covered[r.ID] {
			continue
		}
		ar := d.ar[r.ID]
		if ar == nil {
			continue
		}
		switch {
		case r.Type == "AWS::RDS::DBInstance" || r.Type == "AWS::RDS::DBCluster":
			if r.AttrString("cluster_identifier") != "" {
				d.mapTo(r, "absorbed", "", "workloads.database", "member of a mapped cluster", false)
				continue
			}
			d.unsupported(r, fmt.Sprintf("engine %s is not provisioned by Hermes v1alpha1 (PostgreSQL only)", r.AttrString("engine")))
		case r.Type == "AWS::Route53::HostedZone":
			d.unsupported(r, "DNS is reserved in v1alpha1 (dns.enabled must stay false); re-create the zone and records with the target DNS service")
		case ar.HermesSupport == "implemented":
			path := map[string]string{"network": "network", "compute": "workloads.compute", "database": "workloads.database",
				"storage": "workloads.storage", "messaging": "workloads.messaging", "secrets": "workloads.secrets",
				"observability": "workloads.observability", "identity": "per-service identities"}[ar.Capability]
			if path == "" {
				path = "workloads"
			}
			d.mapTo(r, "absorbed", "", path, "covered by the "+path+" block; no 1:1 element", true)
		case ar.HermesSupport == "partial":
			d.unsupported(r, "only partially covered by Hermes v1alpha1: "+firstNote(ar.Notes))
		default:
			d.unsupported(r, "outside the Hermes v1alpha1 provisioning scope")
		}
	}
}

func firstNote(ns []string) string {
	if len(ns) == 0 {
		return "see catalog differences"
	}
	return ns[0]
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// suggestion names a registry module or provider resource to start from. Names are a
// starting point for a search and must be verified in the registry [U].
func suggestion(target, category string) string {
	gcp := map[string]string{
		"dns": "terraform-google-modules/cloud-dns/google", "cache": "terraform-google-modules/memorystore/google",
		"kubernetes": "terraform-google-modules/kubernetes-engine/google", "container-orchestration": "terraform-google-modules/kubernetes-engine/google",
		"load-balancing": "terraform-google-modules/lb-http/google", "cdn": "terraform-google-modules/lb-http/google (Cloud CDN)",
		"key-management": "terraform-google-modules/kms/google", "identity": "terraform-google-modules/iam/google",
		"hybrid-connectivity": "terraform-google-modules/vpn/google", "networking": "terraform-google-modules/network/google",
		"analytics": "terraform-google-modules/bigquery/google", "serverless-function": "google_cloudfunctions2_function (provider resource)",
		"nosql-database":     "google_firestore_database / google_bigtable_instance (provider resources)",
		"container-registry": "google_artifact_registry_repository (provider resource)", "certificates": "google_certificate_manager_certificate (provider resource)",
		"monitoring": "google_monitoring_alert_policy (provider resource)", "api-gateway": "google_api_gateway_api (provider resource)",
		"workflow": "google_workflows_workflow (provider resource)", "streaming": "google_pubsub_topic / google_managed_kafka_cluster (provider resources)",
		"relational-database": "terraform-google-modules/sql-db/google", "block-storage": "google_compute_disk (provider resource)",
		"compute": "terraform-google-modules/vm/google", "secrets": "google_secret_manager_secret (provider resource)",
		"iot": "no managed equivalent: run an MQTT broker on the container runtime", "time-series": "google_bigtable_instance / BigQuery (provider resources)",
	}
	ali := map[string]string{ //nolint:gosec // provider resource names, not credentials
		"dns": "alicloud_alidns_domain (provider resource)", "cache": "alicloud_kvstore_instance (provider resource)",
		"kubernetes": "alicloud_cs_managed_kubernetes (provider resource)", "container-orchestration": "alicloud_cs_managed_kubernetes (provider resource)",
		"load-balancing": "alicloud_alb_load_balancer (provider resource)", "cdn": "alicloud_cdn_domain_new (provider resource)",
		"key-management": "alicloud_kms_key (provider resource)", "identity": "alicloud_ram_role (provider resource)",
		"hybrid-connectivity": "alicloud_vpn_gateway (provider resource)", "networking": "alicloud_vpc / alicloud_vswitch (provider resources)",
		"analytics": "alicloud_maxcompute_project (provider resource)", "serverless-function": "alicloud_fcv3_function (provider resource)",
		"nosql-database": "alicloud_ots_instance (provider resource)", "container-registry": "alicloud_cr_ee_instance (provider resource)",
		"certificates": "alicloud_ssl_certificates_service_certificate (provider resource)", "monitoring": "alicloud_cms_alarm (provider resource)",
		"api-gateway": "alicloud_api_gateway_api (provider resource)", "relational-database": "alicloud_db_instance (provider resource)",
		"block-storage": "alicloud_ecs_disk (provider resource)", "compute": "alicloud_instance (provider resource)",
		"secrets": "alicloud_kms_secret (provider resource)", "iot": "no managed equivalent for new subscriptions: run an MQTT broker on the container runtime",
	}
	m := gcp
	if target == "alibaba" {
		m = ali
	}
	if s, ok := m[category]; ok {
		return s + " [verify in the registry]"
	}
	return "no generic suggestion; see the catalog entry and provider documentation"
}

// asMaps accepts []map[string]any (in-memory inventories) and []any (decoded JSON).
func asMaps(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		var out []map[string]any
		for _, x := range t {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func asStrings(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		var out []string
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}
