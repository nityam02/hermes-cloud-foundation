package providers

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
)

// AWS: ECS on Fargate behind a shared ALB, RDS for PostgreSQL, S3, SNS+SQS, Secrets
// Manager, CloudWatch + ADOT. Composition: infra/providers/aws/platform.

type awsAdapter struct{}

func (awsAdapter) Name() string        { return config.ProviderAWS }
func (awsAdapter) DisplayName() string { return "AWS" }

func (awsAdapter) Capabilities() CapabilityMatrix {
	return CapabilityMatrix{
		ComputePublicHTTPS:          {Implemented, "shared ALB; HTTPS only with extensions.aws.acm_certificate_arn (otherwise HTTP, rejected in protected environments)"},
		ComputeScaleToZero:          {Partial, "only queue consumers scale from zero on SQS backlog; HTTP services stay at min_instances"},
		ComputeOtelSidecar:          {Implemented, "AWS Distro for OpenTelemetry collector sidecar per task"},
		DatabaseHighAvailability:    {Implemented, "RDS Multi-AZ"},
		DatabasePostGIS:             {Partial, "available on RDS; requires CREATE EXTENSION postgis after provisioning"},
		DatabaseIAMAuth:             {Implemented, "rds-db:connect for database user app (HERMES_DB_AUTH=iam); create the user with the rds_iam role"},
		DatabasePITR:                {Implemented, "always on with automated backups (point_in_time_recovery: false cannot disable it)"},
		DatabaseCustomerManagedKeys: {Implemented, "KMS customer managed key per environment"},
		StorageVersioning:           {Implemented, "S3 versioning"},
		StorageRetention:            {Implemented, "S3 lifecycle expiration"},
		StorageCustomerManagedKeys:  {Implemented, "SSE-KMS with bucket keys"},
		MessagingDeadLetter:         {Implemented, "SQS dead-letter queue with redrive policy"},
		MessagingRetention:          {Implemented, "SQS message retention (contract maximum 7 days)"},
		SecretsNativeInjection:      {Implemented, "ECS task definition secrets (valueFrom Secrets Manager ARN)"},
		ObservabilityLogsRetention:  {Implemented, "CloudWatch Logs retention, rounded up to an accepted value"},
		ObservabilityAlerts:         {Implemented, "CloudWatch alarms to an SNS email topic"},
		ObservabilityTracing:        {Implemented, "OTLP to the ADOT sidecar, exported to X-Ray"},
		StateRemoteBackend:          {Implemented, "S3 backend"},
		StateLocking:                {Implemented, "S3 native lockfile (use_lockfile), or DynamoDB with state.lock_table"},
		StateClientSideEncryption:   {Implemented, "OpenTofu only: aws_kms key provider with aes_gcm"},
		IdentityPerService:          {Implemented, "one IAM task role per service, least privilege from access"},
	}
}

// awsRegions: commercial-partition regions (snapshot, November 2026). GovCloud and China
// partitions are intentionally absent: they need partition-aware ARNs not tested here.
var awsRegions = []string{
	"us-east-1", "us-east-2", "us-west-1", "us-west-2",
	"af-south-1",
	"ap-east-1", "ap-east-2", "ap-south-1", "ap-south-2",
	"ap-southeast-1", "ap-southeast-2", "ap-southeast-3", "ap-southeast-4", "ap-southeast-5", "ap-southeast-6", "ap-southeast-7",
	"ap-northeast-1", "ap-northeast-2", "ap-northeast-3",
	"ca-central-1", "ca-west-1",
	"eu-central-1", "eu-central-2", "eu-west-1", "eu-west-2", "eu-west-3",
	"eu-south-1", "eu-south-2", "eu-north-1",
	"il-central-1", "me-south-1", "me-central-1",
	"mx-central-1", "sa-east-1",
}

func (awsAdapter) Regions() []string { return awsRegions }

func (awsAdapter) ExtensionKeys() []ExtensionKey {
	return []ExtensionKey{
		{Name: "acm_certificate_arn", Description: "ACM certificate for the ALB HTTPS listener; required for public services in protected environments",
			Pattern: `^arn:aws[a-z-]*:acm:[a-z0-9-]+:[0-9]{12}:certificate/[A-Za-z0-9-]+$`},
		{Name: "alb_ingress_cidrs", Description: "CIDRs allowed to reach the public ALB (default 0.0.0.0/0)", Type: "list",
			Pattern: `^[0-9a-fA-F:.]+/[0-9]{1,3}$`},
		{Name: "task_egress_cidrs", Description: "IPv4 CIDRs tasks may reach on TCP 443 (default 0.0.0.0/0); narrow it with VPC endpoints or an egress proxy", Type: "list",
			Pattern: `^[0-9]{1,3}(\.[0-9]{1,3}){3}/[0-9]{1,2}$`},
		{Name: "cpu_architecture", Description: "Fargate CPU architecture: X86_64 (default) or ARM64 (must match the images)", Pattern: `^(X86_64|ARM64)$`},
		{Name: "db_instance_class", Description: "explicit RDS instance class (overrides the size mapping)", Pattern: `^db\.[a-z0-9]+\.[a-z0-9]+$`},
		{Name: "readonly_root_filesystem", Description: "containers run with a read-only root filesystem (default true)", Type: "bool"},
		{Name: "container_health_check", Description: "add a container-level health check (default false; the ALB checks health_check_path)", Type: "bool"},
	}
}

// strictExtensions: the aws composition rejects unknown extensions.aws keys.
func (awsAdapter) strictExtensions() bool { return true }

var awsAccountRe = regexp.MustCompile(`^[0-9]{12}$`)

func (a awsAdapter) Validate(p *config.Platform) diag.Diagnostics {
	ds := validateCommon(a, p)
	if p.Cloud.Account != "" && !awsAccountRe.MatchString(p.Cloud.Account) {
		ds = append(ds, diag.Errorf(CodeAccountFormat, "cloud.account", "AWS account ID must be 12 digits (got %q)", p.Cloud.Account))
	}
	ds = append(ds, certificateCheck(p, a.Name(), "acm_certificate_arn", diag.SeverityWarning)...)
	// F-16: an AWS VPC is at most a /16 (the generic rule already requires /20 or larger).
	if pfx, err := netip.ParsePrefix(p.Network.CIDR); err == nil && pfx.Addr().Is4() && pfx.Bits() < 16 {
		ds = append(ds, diag.Errorf(CodeExtensionValue, "network.cidr", "network.cidr %s is wider than /16, the largest AWS VPC; use a prefix between /16 and /20", pfx))
	}
	ds = append(ds, checkCIDRList(a, p, "task_egress_cidrs", false)...)
	// IPv4 only: the module writes the entries to cidr_ipv4 (an IPv6 entry would fail at apply).
	ds = append(ds, checkCIDRList(a, p, "alb_ingress_cidrs", false)...)
	return ds
}

// checkCIDRList validates a CIDR-list extension when it is set (F-16): non-empty, every entry
// a valid network address without host bits (IPv4 only unless allowIPv6), and no duplicates
// after normalisation. These used to surface only at plan time (module validation or a
// duplicate map key).
func checkCIDRList(a Adapter, p *config.Platform, key string, allowIPv6 bool) diag.Diagnostics {
	provider := a.Name()
	var pattern *regexp.Regexp
	for _, k := range a.ExtensionKeys() {
		if k.Name == key && k.Pattern != "" {
			pattern = regexp.MustCompile(k.Pattern)
		}
	}
	raw, set := p.Extensions.For(provider)[key]
	if !set {
		return nil
	}
	path := "extensions." + provider + "." + key
	list, ok := raw.([]any)
	if !ok {
		return nil // wrong type: reported by checkExtensions
	}
	if len(list) == 0 {
		return diag.Diagnostics{diag.Errorf(CodeExtensionValue, path, "%s must not be empty (omit it to use the default)", path)}
	}
	var ds diag.Diagnostics
	seen := map[netip.Prefix]string{}
	for i, x := range list {
		s, _ := x.(string)
		if pattern != nil && !pattern.MatchString(s) {
			continue // already reported by checkExtensions
		}
		pfx, err := netip.ParsePrefix(s)
		switch {
		case err != nil:
			ds = append(ds, diag.Errorf(CodeExtensionValue, fmt.Sprintf("%s[%d]", path, i), "%q is not a valid CIDR", s))
			continue
		case !allowIPv6 && !pfx.Addr().Is4():
			ds = append(ds, diag.Errorf(CodeExtensionValue, fmt.Sprintf("%s[%d]", path, i), "%q is not an IPv4 CIDR", s))
			continue
		case pfx.Masked() != pfx:
			ds = append(ds, diag.Errorf(CodeExtensionValue, fmt.Sprintf("%s[%d]", path, i), "%s has host bits set (did you mean %s?)", pfx, pfx.Masked()))
			continue
		}
		if prev, dup := seen[pfx]; dup {
			ds = append(ds, diag.Errorf(CodeExtensionValue, fmt.Sprintf("%s[%d]", path, i), "%s is listed twice (also %q)", pfx, prev))
		}
		seen[pfx] = s
	}
	return ds
}

// fargateMemory returns the valid Fargate memory sizes (MiB) for a vCPU value.
func fargateMemory(cpu float64) []int {
	steps := func(from, to int) []int {
		var out []int
		for m := from; m <= to; m += 1024 {
			out = append(out, m)
		}
		return out
	}
	switch cpu {
	case 0.25:
		return []int{512, 1024, 2048}
	case 0.5:
		return steps(1024, 4096)
	case 1:
		return steps(2048, 8192)
	case 2:
		return steps(4096, 16384)
	case 4:
		return steps(8192, 30720)
	}
	return nil
}

var awsDBUnavailable = map[string]string{
	"timescaledb": "Amazon RDS for PostgreSQL does not offer TimescaleDB (see the reference architecture §5.2)",
}

func (a awsAdapter) CheckWorkloads(p *config.Platform) diag.Diagnostics {
	var ds diag.Diagnostics
	for i, s := range p.Workloads.Compute.Services {
		valid := fargateMemory(s.CPU)
		if !slices.Contains(valid, s.MemoryMB) {
			ds = append(ds, diag.Errorf(CodeSizing, fmt.Sprintf("workloads.compute.services[%d].memory_mb", i),
				"Fargate does not support cpu %g with memory_mb %d", s.CPU, s.MemoryMB).
				WithHint("valid memory_mb for cpu %g: %s", s.CPU, joinInts(valid)))
		}
	}
	if db := p.Workloads.Database; db.Enabled && !db.PointInTimeRecovery {
		ds = append(ds, diag.Warnf(CodeCapability, "workloads.database.point_in_time_recovery",
			"point_in_time_recovery: false has no effect on AWS: RDS point-in-time restore is always on while automated backups are enabled"))
	}
	if o := p.Workloads.Observability; o.Enabled {
		if r := CloudWatchRetention(o.LogRetentionDays); r != o.LogRetentionDays {
			ds = append(ds, diag.Warnf(CodeSizingRounded, "workloads.observability.log_retention_days",
				"CloudWatch Logs does not accept %d days; the aws composition keeps logs for %d days", o.LogRetentionDays, r))
		}
	}
	if !p.Network.NAT && len(p.Workloads.Compute.Services) > 0 {
		ds = append(ds, diag.Warnf(CodeCapability, "network.nat",
			"network.nat is false: tasks have no egress (Hermes creates no VPC interface endpoints), so image pulls from outside ECR and AWS API calls will fail"))
	}
	ds = append(ds, checkDBExtensions(a, p, commonExtensions, awsDBUnavailable)...)
	ds = append(ds, checkCapabilities(a, p)...)
	return ds
}

// cloudWatchRetentions are the retention values CloudWatch Logs accepts.
var cloudWatchRetentions = []int{1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653}

// CloudWatchRetention mirrors the aws composition: round up to the next accepted value.
func CloudWatchRetention(days int) int {
	for _, d := range cloudWatchRetentions {
		if d >= days {
			return d
		}
	}
	return cloudWatchRetentions[len(cloudWatchRetentions)-1]
}

func (a awsAdapter) PlanChecks(p *config.Platform, _ func(string) string) diag.Diagnostics {
	ds := append(requireAccountForPlan(p), planStateChecks(p)...)
	return append(ds, certificateCheck(p, a.Name(), "acm_certificate_arn", diag.SeverityError)...)
}

func (awsAdapter) Providers() []TerraformProvider { return []TerraformProvider{PinAWS} }

func (awsAdapter) ProviderConfigs(p *config.Platform) map[string]map[string]any {
	return map[string]map[string]any{PinAWS.LocalName: awsProviderConfig(p)}
}

func awsProviderConfig(p *config.Platform) map[string]any {
	cfg := map[string]any{
		"region":       p.Cloud.Region,
		"default_tags": map[string]any{"tags": ManagedTags(p)},
	}
	if p.Cloud.Account != "" {
		cfg["allowed_account_ids"] = []string{p.Cloud.Account}
	}
	return cfg
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprint(x)
	}
	return strings.Join(parts, ", ")
}
