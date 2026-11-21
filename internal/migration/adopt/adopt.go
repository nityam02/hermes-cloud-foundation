// Package adopt implements IaC adoption of EXISTING source resources: it generates import
// blocks from an Inventory into a separate "source adoption" workspace and gates every
// engine plan so that adoption can only import — never create, modify, replace or delete.
//
// Adoption is not migration: nothing moves, and the adoption state is never the target state.
package adopt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nityam02/hermes-cloud-foundation/internal/approval"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
	"github.com/nityam02/hermes-cloud-foundation/internal/tfplan"
)

// AWSProviderConstraint matches the constraint used by the Hermes AWS modules.
const AWSProviderConstraint = "~> 6.73"

// File names in an adoption workspace.
const (
	ManifestFile  = "adoption.json"
	PlanFile      = "adopt.tfplan"
	PlanJSONFile  = "adopt-plan.json"
	GeneratedFile = "generated.tf"
)

// Engine is the subset of *execution.Engine adoption needs (fakeable in tests).
type Engine interface {
	Init(ctx context.Context, dir string, args ...string) error
	Plan(ctx context.Context, dir, planFile string, args ...string) (bool, error)
	ShowJSON(ctx context.Context, dir, planFile string) ([]byte, error)
	ApplySaved(ctx context.Context, dir, planFile string) error
}

type mapping struct {
	tfType string
	id     func(r *inventory.Resource) string
}

func attr(k string) func(*inventory.Resource) string {
	return func(r *inventory.Resource) string { return r.AttrString(k) }
}
func name(r *inventory.Resource) string { return r.Name }
func arn(r *inventory.Resource) string  { return r.ID }

// Mappings is the Inventory type -> Terraform AWS provider type and import id table.
var mappings = map[string]mapping{
	"AWS::S3::Bucket":                           {"aws_s3_bucket", name},
	"AWS::SQS::Queue":                           {"aws_sqs_queue", attr("queue_url")},
	"AWS::SNS::Topic":                           {"aws_sns_topic", arn},
	"AWS::SNS::Subscription":                    {"aws_sns_topic_subscription", arn},
	"AWS::RDS::DBInstance":                      {"aws_db_instance", attr("identifier")},
	"AWS::RDS::DBCluster":                       {"aws_rds_cluster", attr("identifier")},
	"AWS::RDS::DBSubnetGroup":                   {"aws_db_subnet_group", attr("identifier")},
	"AWS::EC2::VPC":                             {"aws_vpc", attr("resource_id")},
	"AWS::EC2::Subnet":                          {"aws_subnet", attr("resource_id")},
	"AWS::EC2::SecurityGroup":                   {"aws_security_group", attr("resource_id")},
	"AWS::EC2::RouteTable":                      {"aws_route_table", attr("resource_id")},
	"AWS::EC2::InternetGateway":                 {"aws_internet_gateway", attr("resource_id")},
	"AWS::EC2::NatGateway":                      {"aws_nat_gateway", attr("resource_id")},
	"AWS::EC2::Instance":                        {"aws_instance", attr("resource_id")},
	"AWS::EC2::Volume":                          {"aws_ebs_volume", attr("resource_id")},
	"AWS::EC2::VPCEndpoint":                     {"aws_vpc_endpoint", attr("resource_id")},
	"AWS::EC2::TransitGateway":                  {"aws_ec2_transit_gateway", attr("resource_id")},
	"AWS::EC2::VPNGateway":                      {"aws_vpn_gateway", attr("resource_id")},
	"AWS::EC2::VPCPeeringConnection":            {"aws_vpc_peering_connection", attr("resource_id")},
	"AWS::DynamoDB::Table":                      {"aws_dynamodb_table", name},
	"AWS::Lambda::Function":                     {"aws_lambda_function", name},
	"AWS::Lambda::EventSourceMapping":           {"aws_lambda_event_source_mapping", attr("uuid")},
	"AWS::ECS::Cluster":                         {"aws_ecs_cluster", name},
	"AWS::ECS::Service":                         {"aws_ecs_service", func(r *inventory.Resource) string { return r.AttrString("cluster_name") + "/" + r.Name }},
	"AWS::ECS::TaskDefinition":                  {"aws_ecs_task_definition", arn},
	"AWS::ECR::Repository":                      {"aws_ecr_repository", name},
	"AWS::EKS::Cluster":                         {"aws_eks_cluster", name},
	"AWS::EKS::Nodegroup":                       {"aws_eks_node_group", func(r *inventory.Resource) string { return r.AttrString("cluster_name") + ":" + r.Name }},
	"AWS::IAM::Role":                            {"aws_iam_role", name},
	"AWS::KMS::Key":                             {"aws_kms_key", attr("key_id")},
	"AWS::Logs::LogGroup":                       {"aws_cloudwatch_log_group", name},
	"AWS::CloudWatch::Alarm":                    {"aws_cloudwatch_metric_alarm", name},
	"AWS::SecretsManager::Secret":               {"aws_secretsmanager_secret", arn},
	"AWS::Route53::HostedZone":                  {"aws_route53_zone", attr("zone_id")},
	"AWS::ElasticLoadBalancingV2::LoadBalancer": {"aws_lb", arn},
	"AWS::ElasticLoadBalancingV2::Listener":     {"aws_lb_listener", arn},
	"AWS::ElasticLoadBalancingV2::TargetGroup":  {"aws_lb_target_group", arn},
	"AWS::ElastiCache::ReplicationGroup":        {"aws_elasticache_replication_group", attr("resource_id")},
	"AWS::ElastiCache::CacheCluster":            {"aws_elasticache_cluster", attr("resource_id")},
	"AWS::Kinesis::Stream":                      {"aws_kinesis_stream", name},
	"AWS::CertificateManager::Certificate":      {"aws_acm_certificate", arn},
	"AWS::CloudFront::Distribution":             {"aws_cloudfront_distribution", attr("distribution_id")},
	"AWS::ApiGatewayV2::Api":                    {"aws_apigatewayv2_api", attr("api_id")},
	"AWS::StepFunctions::StateMachine":          {"aws_sfn_state_machine", arn},
	"AWS::Events::EventBus":                     {"aws_cloudwatch_event_bus", name},
	"AWS::Events::Rule": {"aws_cloudwatch_event_rule", func(r *inventory.Resource) string {
		if b := r.AttrString("event_bus_name"); b != "" && b != "default" {
			return b + "/" + r.Name
		}
		return r.Name
	}},
}

// secretStateTypes are NOT adoptable: importing them writes secret VALUES into the adoption
// state (F-08), which contradicts "secret values are never managed by IaC".
//   - AWS::SSM::Parameter: aws_ssm_parameter reads the (decrypted) value, and the configuration
//     needs `value` to stay import-only, so the secret cannot be kept out of state or code.
//   - AWS::EC2::VPNConnection: aws_vpn_connection stores the tunnel pre-shared keys and the
//     customer gateway configuration (which contains them).
//
// Types whose state holds configuration that MAY be sensitive (Lambda and ECS environment
// values, CloudFront origin custom headers, SNS subscription endpoints) stay adoptable: the
// adoption state must be stored like a secret (StateWarning).
var secretStateTypes = map[string]string{
	"AWS::SSM::Parameter":     "not adoptable: importing aws_ssm_parameter writes the parameter value (decrypted for SecureString) into state; keep parameters out of IaC or manage only their metadata out-of-band",
	"AWS::EC2::VPNConnection": "not adoptable: importing aws_vpn_connection writes the tunnel pre-shared keys into state",
}

// StateWarning is shown whenever an adoption workspace is generated or planned.
const StateWarning = "adoption state contains the full configuration of every imported resource (for example Lambda and ECS environment variable values) and must be stored like a secret: an encrypted remote backend with restricted access, or OpenTofu state encryption"

// SupportedTypes lists the adoptable inventory types.
func SupportedTypes() []string {
	out := make([]string, 0, len(mappings))
	for k := range mappings {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Options selects what to adopt.
type Options struct {
	Types          []string // inventory types (empty = all adoptable)
	IDs            []string // resource ids (empty = all)
	IncludeManaged bool     // also adopt resources managed by Terraform/CloudFormation (dual management!)
	BackendBucket  string   // S3 backend bucket for the adoption state (encrypted, lockfile)
	BackendRegion  string
	// AllowLocalState permits the local backend (state/source-adoption.tfstate). Without it,
	// Generate refuses: the state holds full resource configuration (StateWarning).
	AllowLocalState bool
}

// ErrLocalState is returned when the adoption state would be local without AllowLocalState.
var ErrLocalState = errors.New("adoption state would be local and unencrypted")

// Import is one generated import block.
type Import struct {
	Address    string `json:"address"`
	ImportID   string `json:"import_id"`
	Provider   string `json:"provider"`
	SourceID   string `json:"source_id"`
	SourceType string `json:"source_type"`
}

// Skipped is a resource not adopted, with the reason.
type Skipped struct {
	SourceID string `json:"source_id"`
	Type     string `json:"type"`
	Reason   string `json:"reason"`
}

// Manifest is written to adoption.json and checked by verify.
type Manifest struct {
	APIVersion      string `json:"apiVersion"`
	Kind            string `json:"kind"`
	InventoryDigest string `json:"inventory_digest"`
	AccountID       string `json:"account_id"`
	// LocalStateAllowed records --allow-local-state; plan and apply refuse a local backend
	// otherwise (also when backend.tf is edited after generation).
	LocalStateAllowed bool      `json:"local_state_allowed,omitempty"`
	Imports           []Import  `json:"imports"`
	Skipped           []Skipped `json:"skipped"`
}

var nonIdent = regexp.MustCompile(`[^a-z0-9_]+`)

func ident(s string) string {
	n := strings.Trim(nonIdent.ReplaceAllString(strings.ToLower(s), "_"), "_")
	if n == "" || n[0] < 'a' || n[0] > 'z' {
		n = "r_" + n
	}
	if len(n) > 60 {
		sum := sha256.Sum256([]byte(s))
		n = n[:53] + "_" + hex.EncodeToString(sum[:])[:6]
	}
	return n
}

func providerAlias(region string) string { return strings.ReplaceAll(region, "-", "_") }

// Generate writes providers.tf, imports.tf, backend.tf, README.md and adoption.json into dir.
func Generate(inv *inventory.Inventory, dir string, o Options) (*Manifest, error) {
	if inv.Metadata.Provider != "aws" {
		return nil, fmt.Errorf("adopt: inventory provider is %q; only aws is supported", inv.Metadata.Provider)
	}
	acct := inv.Metadata.AccountID
	if acct == "" || acct == inventory.Redacted {
		return nil, errors.New("adopt: the inventory has no account id (redacted inventories cannot be adopted: allowed_account_ids would be unknown)")
	}
	if o.BackendBucket == "" && !o.AllowLocalState {
		return nil, fmt.Errorf("adopt: %w: %s; pass --backend-bucket <encrypted S3 bucket>, or --allow-local-state to keep it on this machine deliberately", ErrLocalState, StateWarning)
	}
	typeSet := map[string]bool{}
	for _, t := range o.Types {
		if reason, secret := secretStateTypes[t]; secret {
			return nil, fmt.Errorf("adopt: type %s is %s", t, reason)
		}
		if _, ok := mappings[t]; !ok {
			return nil, fmt.Errorf("adopt: type %s is not adoptable (supported: %s)", t, strings.Join(SupportedTypes(), ", "))
		}
		typeSet[t] = true
	}
	idSet := map[string]bool{}
	for _, id := range o.IDs {
		idSet[id] = true
	}
	m := &Manifest{APIVersion: "hermes/v1alpha1", Kind: "AdoptionManifest", InventoryDigest: inv.Digest(), AccountID: acct,
		LocalStateAllowed: o.BackendBucket == "" && o.AllowLocalState, Imports: []Import{}, Skipped: []Skipped{}}
	used := map[string]bool{}
	regions := map[string]bool{}
	home := ""
	if len(inv.Metadata.Regions) > 0 {
		home = inv.Metadata.Regions[0]
	}
	for i := range inv.Resources {
		r := &inv.Resources[i]
		if len(typeSet) > 0 && !typeSet[r.Type] || len(idSet) > 0 && !idSet[r.ID] {
			continue
		}
		if reason, secret := secretStateTypes[r.Type]; secret {
			m.Skipped = append(m.Skipped, Skipped{r.ID, r.Type, reason})
			continue
		}
		mp, ok := mappings[r.Type]
		if !ok {
			m.Skipped = append(m.Skipped, Skipped{r.ID, r.Type, "no import mapping for this type"})
			continue
		}
		if !o.IncludeManaged && (r.ManagedBy == inventory.ManagedTerraform || r.ManagedBy == inventory.ManagedCloudFormation) {
			m.Skipped = append(m.Skipped, Skipped{r.ID, r.Type, "already managed by " + r.ManagedBy + " (" + r.ManagedByEvidence + "); adopting it would create dual management"})
			continue
		}
		id := mp.id(r)
		if id == "" {
			m.Skipped = append(m.Skipped, Skipped{r.ID, r.Type, "import id unknown (identifier-only or partial inventory)"})
			continue
		}
		region := r.Region
		if region == "" || region == "global" || region == "unknown" {
			region = home
		}
		regions[region] = true
		addr := mp.tfType + "." + ident(r.Name)
		for n := 2; used[addr]; n++ {
			addr = fmt.Sprintf("%s.%s_%d", mp.tfType, ident(r.Name), n)
		}
		used[addr] = true
		m.Imports = append(m.Imports, Import{Address: addr, ImportID: id, Provider: "aws." + providerAlias(region), SourceID: r.ID, SourceType: r.Type})
	}
	if len(m.Imports) == 0 {
		return m, errors.New("adopt: nothing to adopt with the given filters")
	}
	sort.Slice(m.Imports, func(i, j int) bool { return m.Imports[i].Address < m.Imports[j].Address })
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	var regs []string
	for r := range regions {
		regs = append(regs, r)
	}
	sort.Strings(regs)
	files := map[string]string{
		"providers.tf": providersTF(regs, acct),
		"imports.tf":   importsTF(m.Imports),
		"backend.tf":   backendTF(o, acct),
		"README.md":    readme(m, dir),
	}
	mj, _ := json.MarshalIndent(m, "", "  ")
	files[ManifestFile] = string(mj) + "\n"
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil { //nolint:gosec // generated IaC meant to be reviewed and committed
			return nil, err
		}
	}
	return m, nil
}

func providersTF(regions []string, acct string) string {
	var b strings.Builder
	b.WriteString("# Source adoption workspace generated by `hermes adopt aws`. It imports EXISTING resources\n")
	b.WriteString("# into a separate state; it must never create, change or delete anything.\n")
	b.WriteString("terraform {\n  required_version = \">= 1.10.0\"\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n")
	fmt.Fprintf(&b, "      version = %q\n    }\n  }\n}\n", AWSProviderConstraint)
	for _, r := range regions {
		fmt.Fprintf(&b, "\nprovider \"aws\" {\n  alias               = %q\n  region              = %q\n  allowed_account_ids = [%q]\n}\n", providerAlias(r), r, acct)
	}
	return b.String()
}

func importsTF(imports []Import) string {
	var b strings.Builder
	b.WriteString("# One import block per adopted resource. Generate matching configuration with\n# `hermes adopt plan` (engine -generate-config-out) and REVIEW it before any apply.\n")
	for _, im := range imports {
		fmt.Fprintf(&b, "\n# %s %s\nimport {\n  to       = %s\n  id       = %q\n  provider = %s\n}\n", im.SourceType, im.SourceID, im.Address, im.ImportID, im.Provider)
	}
	return b.String()
}

func backendTF(o Options, acct string) string {
	if o.BackendBucket != "" {
		region := o.BackendRegion
		if region == "" {
			region = "us-east-1"
		}
		return fmt.Sprintf("# Separate SOURCE ADOPTION state. Never point this at the Hermes target state.\nterraform {\n  backend \"s3\" {\n    bucket       = %q\n    key          = \"source-adoption/%s/terraform.tfstate\"\n    region       = %q\n    encrypt      = true\n    use_lockfile = true\n  }\n}\n", o.BackendBucket, acct, region)
	}
	return "# Separate SOURCE ADOPTION state, LOCAL and unencrypted (generated with --allow-local-state).\n# It contains the full configuration of every imported resource (for example Lambda and ECS\n# environment values): protect it like a secret, move it to an encrypted remote backend before\n# sharing, and never point it at the Hermes target state.\nterraform {\n  backend \"local\" {\n    path = \"state/source-adoption.tfstate\"\n  }\n}\n"
}

func readme(m *Manifest, dir string) string {
	var b strings.Builder
	b.WriteString("# Source adoption workspace (import only)\n\n")
	fmt.Fprintf(&b, "Generated by `hermes adopt aws` for account %s: %d import block(s), %d resource(s) skipped.\n\n", m.AccountID, len(m.Imports), len(m.Skipped))
	b.WriteString("This workspace brings existing, hand-built source resources under IaC **without changing them**. It is IaC adoption, not migration: nothing moves, and this state is separate from any Hermes target state.\n\n")
	b.WriteString("**State sensitivity:** the " + StateWarning + ". SSM parameters and VPN connections are never adopted (their state would hold secret values).\n\n")
	b.WriteString("## Steps\n\n")
	fmt.Fprintf(&b, "1. Review `imports.tf`, `providers.tf` (allowed_account_ids) and `backend.tf`.\n")
	fmt.Fprintf(&b, "2. With read-only credentials for the source account: `hermes adopt plan --dir %s` (runs init and `plan -generate-config-out=generated.tf`).\n", dir)
	b.WriteString("3. Review `generated.tf` carefully: generated configuration is experimental and can contain sensitive values. Remove secrets and fix arguments until the plan shows only imports.\n")
	fmt.Fprintf(&b, "4. `hermes adopt verify --dir %s`: refuses unless every change is a pure import (no create, update, replace or delete).\n", dir)
	fmt.Fprintf(&b, "5. `hermes adopt apply --dir %s --approve <plan-id-prefix>` applies only the verified import-only saved plan (applying an import plan writes state; it needs credentials that can read the resources).\n\n", dir)
	if len(m.Skipped) > 0 {
		b.WriteString("## Skipped\n\n| Type | Resource | Reason |\n|---|---|---|\n")
		for _, s := range m.Skipped {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", s.Type, s.SourceID, strings.ReplaceAll(s.Reason, "|", "\\|"))
		}
	}
	return b.String()
}

// LoadManifest reads adoption.json.
func LoadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("adopt: %w (run hermes adopt aws first)", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("adopt: %s: %w", ManifestFile, err)
	}
	return &m, nil
}

// Verification is the result of checking an adoption plan.
type Verification struct {
	PlanID     string        `json:"plan_id"`
	Imports    int           `json:"imports"`
	Offending  []tfplan.Item `json:"offending"`
	Unexpected []string      `json:"unexpected_imports"`
	OK         bool          `json:"ok"`
	// Policy is the advisory policy check of the adoption plan (set by the CLI).
	Policy *execution.PolicyRecord `json:"policy,omitempty"`
}

// ErrNotImportOnly is returned when an adoption plan would change infrastructure.
var ErrNotImportOnly = errors.New("adoption plan is not import-only")

// Verify checks the saved plan: every change must be a pure import of an address from the
// manifest. It returns ErrNotImportOnly with the offending addresses otherwise.
func Verify(ctx context.Context, e Engine, dir string) (*Verification, error) {
	m, err := LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	planPath := filepath.Join(dir, PlanFile)
	raw, err := os.ReadFile(planPath)
	if err != nil {
		return nil, fmt.Errorf("adopt: %w (run hermes adopt plan first)", err)
	}
	js, err := e.ShowJSON(ctx, dir, PlanFile)
	if err != nil {
		return nil, fmt.Errorf("adopt: show plan: %w", err)
	}
	_ = os.WriteFile(filepath.Join(dir, PlanJSONFile), js, 0o600)
	p, err := tfplan.Parse(js)
	if err != nil {
		return nil, err
	}
	sum := tfplan.Summarize(p)
	sha := sha256.Sum256(raw)
	v := &Verification{PlanID: hex.EncodeToString(sha[:]), Imports: sum.Import, Offending: sum.NonImportChanges(), Unexpected: []string{}}
	if v.Offending == nil {
		v.Offending = []tfplan.Item{}
	}
	known := map[string]bool{}
	for _, im := range m.Imports {
		known[im.Address] = true
	}
	for _, it := range sum.Items {
		if it.Importing && !known[it.Address] {
			v.Unexpected = append(v.Unexpected, it.Address)
		}
	}
	if p.Errored {
		return v, fmt.Errorf("%w: the plan errored", ErrNotImportOnly)
	}
	if len(v.Offending) > 0 || len(v.Unexpected) > 0 {
		var addrs []string
		for _, it := range v.Offending {
			addrs = append(addrs, fmt.Sprintf("%s (%s)", it.Address, it.Action))
		}
		for _, a := range v.Unexpected {
			addrs = append(addrs, a+" (import not in adoption.json)")
		}
		return v, fmt.Errorf("%w: %s", ErrNotImportOnly, strings.Join(addrs, ", "))
	}
	v.OK = true
	return v, nil
}

// checkBackend refuses a workspace whose state would be local (a `backend "local"` block, or
// no backend block at all) unless the manifest records --allow-local-state.
func checkBackend(dir string, m *Manifest) error {
	if m.LocalStateAllowed {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil {
		return err
	}
	remote := false
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		for _, mt := range backendRe.FindAllStringSubmatch(string(data), -1) {
			if mt[1] == "local" {
				return fmt.Errorf("adopt: %w: %s declares a local backend; %s (regenerate with --allow-local-state to accept that deliberately)", ErrLocalState, filepath.Base(f), StateWarning)
			}
			remote = true
		}
	}
	if !remote {
		return fmt.Errorf("adopt: %w: no backend block in %s (the engine would default to local state); %s", ErrLocalState, dir, StateWarning)
	}
	return nil
}

var backendRe = regexp.MustCompile(`(?m)^\s*backend\s+"([^"]+)"`)

// Plan runs init and an import plan with configuration generation (first run only).
func Plan(ctx context.Context, e Engine, dir string) (*Verification, error) {
	m, err := LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	if err := checkBackend(dir, m); err != nil {
		return nil, err
	}
	if err := e.Init(ctx, dir); err != nil {
		return nil, fmt.Errorf("adopt: init: %w", err)
	}
	var args []string
	if _, err := os.Stat(filepath.Join(dir, GeneratedFile)); errors.Is(err, os.ErrNotExist) {
		args = append(args, "-generate-config-out="+GeneratedFile)
	}
	if _, err := e.Plan(ctx, dir, PlanFile, args...); err != nil {
		return nil, fmt.Errorf("adopt: plan: %w", err)
	}
	return Verify(ctx, e, dir)
}

// Apply re-verifies the saved plan, checks the approval prefix and applies it.
func Apply(ctx context.Context, e Engine, dir, approve string) (*Verification, error) {
	m, err := LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	if err := checkBackend(dir, m); err != nil {
		return nil, err
	}
	v, err := Verify(ctx, e, dir)
	if err != nil {
		return v, err
	}
	if err := approval.Check(v.PlanID, approve); err != nil {
		return v, fmt.Errorf("%w: --approve must be at least %d hexadecimal characters of plan id %s (%w)", ErrApproval, approval.MinPrefix, v.PlanID[:12], err)
	}
	if v.Imports == 0 {
		return v, errors.New("adopt: the plan contains no imports; nothing to apply")
	}
	if err := e.ApplySaved(ctx, dir, PlanFile); err != nil {
		return v, fmt.Errorf("adopt: apply: %w", err)
	}
	return v, nil
}

// ErrApproval marks a missing or wrong --approve.
var ErrApproval = errors.New("approval required")
