package providers

import (
	"fmt"
	"regexp"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
)

// Alibaba Cloud: Serverless App Engine (SAE), ApsaraDB RDS for PostgreSQL, OSS, MNS, KMS
// Secrets Manager, SLS/CloudMonitor. Composition: infra/providers/alibaba/platform.
// The least mature target in the ecosystem (no TFLint ruleset, no Trivy checks, no
// Infracost pricing, no OpenTofu state-encryption key provider).

type alibabaAdapter struct{}

func (alibabaAdapter) Name() string        { return config.ProviderAlibaba }
func (alibabaAdapter) DisplayName() string { return "Alibaba Cloud" }

func (alibabaAdapter) Capabilities() CapabilityMatrix {
	return CapabilityMatrix{
		ComputePublicHTTPS:          {Implemented, "SAE internet SLB; HTTPS only when extensions.alibaba.certificate_id is set (protected + public + no certificate is an error)"},
		ComputeScaleToZero:          {NotImplemented, "SAE minimum is 1 instance; min_instances 0 becomes 1"},
		ComputeOtelSidecar:          {NotImplemented, "no collector sidecar"},
		DatabaseHighAvailability:    {Implemented, "RDS High-availability Edition with a secondary zone"},
		DatabasePostGIS:             {Partial, "available on ApsaraDB RDS for PostgreSQL; requires CREATE EXTENSION postgis after provisioning"},
		DatabaseIAMAuth:             {NotImplemented, "password authentication (HERMES_DB_AUTH=password); the account is created out-of-band"},
		DatabasePITR:                {Partial, "point-in-time restore on the High-availability Edition only"},
		DatabaseCustomerManagedKeys: {Implemented, "RDS disk encryption with a KMS key; requires a manual RDS-to-KMS authorization"},
		StorageVersioning:           {Implemented, "OSS versioning"},
		StorageRetention:            {Implemented, "OSS lifecycle expiration"},
		StorageCustomerManagedKeys:  {Implemented, "OSS server-side encryption with KMS"},
		MessagingDeadLetter:         {Implemented, "MNS dead-letter queue"},
		MessagingRetention:          {Implemented, "MNS queue message retention (maximum 7 days)"},
		SecretsNativeInjection:      {NotImplemented, "no injection into SAE; apps read secrets with their role; secrets need a KMS 3.0 instance (extensions.alibaba.kms_instance_id)"},
		ObservabilityLogsRetention:  {Implemented, "SLS logstore TTL"},
		ObservabilityAlerts:         {Partial, "CloudMonitor contact group and a subset of alarms; see docs/providers/alibaba.md"},
		ObservabilityTracing:        {Partial, "OTLP endpoint only from extensions.alibaba.otlp_endpoint; no collector"},
		StateRemoteBackend:          {Implemented, "OSS backend"},
		StateLocking:                {Implemented, "Tablestore locking (extensions.alibaba.state_tablestore_endpoint and state_tablestore_table)"},
		StateClientSideEncryption:   {NotImplemented, "OpenTofu has no Alibaba KMS key provider"},
		IdentityPerService:          {Partial, "a RAM role is created per service, trusted by the SAE service principal (or extensions.alibaba.workload_role_trusted_principals, never the account root); the provider cannot attach it to the SAE application (manual step)"},
	}
}

// alibabaRegions: snapshot, November 2026.
var alibabaRegions = []string{
	"cn-hangzhou", "cn-shanghai", "cn-qingdao", "cn-beijing", "cn-zhangjiakou", "cn-huhehaote", "cn-wulanchabu",
	"cn-shenzhen", "cn-heyuan", "cn-guangzhou", "cn-chengdu", "cn-hongkong", "cn-nanjing", "cn-fuzhou",
	"ap-southeast-1", "ap-southeast-3", "ap-southeast-5", "ap-southeast-6", "ap-southeast-7",
	"ap-northeast-1", "ap-northeast-2",
	"us-west-1", "us-east-1", "eu-central-1", "eu-west-1", "me-east-1", "me-central-1",
}

func (alibabaAdapter) Regions() []string { return alibabaRegions }

func (alibabaAdapter) ExtensionKeys() []ExtensionKey {
	return []ExtensionKey{
		{Name: "certificate_id", Description: "SSL Certificates Service certificate for the SAE internet SLB; required for public services in protected environments"},
		{Name: "kms_instance_id", Description: "KMS 3.0 instance that holds secrets; required when secrets are declared"},
		{Name: "otlp_endpoint", Description: "OTLP endpoint (ARMS/SLS) services export traces to", Pattern: `^https?://`},
		{Name: "otlp_protocol", Description: "OTLP protocol (default grpc)", Pattern: `^(grpc|http/protobuf)$`},
		{Name: "zone_ids", Description: "explicit vSwitch zones (list) when the region reports fewer than network.zones", Type: "list"},
		{Name: "db_instance_type", Description: "explicit RDS instance class (overrides the size mapping; needed for xlarge without HA)"},
		{Name: "db_credentials_secret", Description: "name of the out-of-band secret holding the database credentials"},
		{Name: "oss_redundancy_type", Description: "OSS redundancy type", Pattern: `^(LRS|ZRS)$`},
		{Name: "mns_sse", Description: "enable MNS server-side encryption", Type: "bool"},
		{Name: "acr_instance_id", Description: "Container Registry instance for image pulls"},
		{Name: "workload_role_trusted_principals", Description: "RAM role/user ARNs that may assume the per-service workload roles (replaces the default SAE service principal; the account root is rejected)",
			Type: "list", Pattern: `^acs:ram::[0-9]{10,20}:(role|user)/[A-Za-z0-9._@/-]+$`},
		{Name: "state_tablestore_endpoint", Description: "read by hermes (not the module): Tablestore endpoint for OSS backend state locking, from `hermes state bootstrap`", Pattern: `^https://`},
		{Name: "state_tablestore_table", Description: "read by hermes (not the module): Tablestore table for OSS backend state locking", Pattern: `^[A-Za-z_][A-Za-z0-9_]{0,254}$`},
	}
}

var alibabaAccountRe = regexp.MustCompile(`^[0-9]{10,20}$`)

func (a alibabaAdapter) Validate(p *config.Platform) diag.Diagnostics {
	ds := validateCommon(a, p)
	if p.Cloud.Account != "" && !alibabaAccountRe.MatchString(p.Cloud.Account) {
		ds = append(ds, diag.Errorf(CodeAccountFormat, "cloud.account", "Alibaba Cloud account ID must be numeric (got %q)", p.Cloud.Account))
	}
	if p.State.Encryption.ClientSide {
		ds = append(ds, diag.Errorf(CodeStateEncrypt, "state.encryption.client_side",
			"client-side state encryption is not supported on alibaba: OpenTofu has no Alibaba KMS key provider").
			WithHint("rely on OSS server-side encryption (encrypt = true) and bucket access control"))
	}
	ds = append(ds, certificateCheck(p, a.Name(), "certificate_id", diag.SeverityWarning)...)
	if p.State.Backend == config.BackendOSS && !alibabaLocking(p) {
		msg := "the OSS backend has no state locking without both extensions.alibaba.state_tablestore_endpoint and state_tablestore_table"
		if p.Protected {
			msg += fmt.Sprintf("; protected environment %q cannot be planned without it", p.Environment)
		}
		d := diag.Warnf(CodePlanState, "extensions.alibaba.state_tablestore_endpoint", "%s", msg).
			WithHint("`hermes state bootstrap` creates the Tablestore table and prints both values")
		ds = append(ds, d)
	}
	ext := p.Extensions.For(a.Name())
	if p.Workloads.Secrets.Enabled && len(p.Workloads.Secrets.Names) > 0 && p.Extensions.String(a.Name(), "kms_instance_id") == "" {
		ds = append(ds, diag.Errorf(CodeExtensionValue, "extensions.alibaba.kms_instance_id",
			"secrets on Alibaba Cloud are created inside a KMS 3.0 instance: set extensions.alibaba.kms_instance_id"))
	}
	db := p.Workloads.Database
	if db.Enabled && !db.HighAvailability && db.Size == "xlarge" && ext["db_instance_type"] == nil {
		ds = append(ds, diag.Errorf(CodeSizing, "workloads.database.size",
			"RDS for PostgreSQL Basic Edition (high_availability: false) has no verified class for size xlarge").
			WithHint("enable high_availability or set extensions.alibaba.db_instance_type"))
	}
	return ds
}

func alibabaLocking(p *config.Platform) bool {
	return p.Extensions.String(config.ProviderAlibaba, "state_tablestore_endpoint") != "" &&
		p.Extensions.String(config.ProviderAlibaba, "state_tablestore_table") != ""
}

// saeSizes are the SAE CPU (vCPU) / memory (MiB) combinations the alibaba composition
// uses, smallest first. SAE's smallest size is 0.5 vCPU / 1 GiB.
var saeSizes = []struct {
	cpu float64
	mem int
}{
	{0.5, 1024},
	{1, 1024}, {1, 2048}, {1, 4096},
	{2, 2048}, {2, 4096}, {2, 8192},
	{4, 4096}, {4, 8192}, {4, 16384},
}

// SAEEffectiveSize returns the SAE size used for a requested cpu/memory pair: the
// smallest supported combination with at least the requested CPU and memory.
func SAEEffectiveSize(cpu float64, memoryMB int) (float64, int) {
	for _, s := range saeSizes {
		if s.cpu >= cpu && s.mem >= memoryMB {
			return s.cpu, s.mem
		}
	}
	last := saeSizes[len(saeSizes)-1]
	return last.cpu, last.mem
}

func (a alibabaAdapter) CheckWorkloads(p *config.Platform) diag.Diagnostics {
	var ds diag.Diagnostics
	for i, s := range p.Workloads.Compute.Services {
		cpu, mem := SAEEffectiveSize(s.CPU, s.MemoryMB)
		if cpu != s.CPU || mem != s.MemoryMB {
			ds = append(ds, diag.Warnf(CodeSizingRounded, fmt.Sprintf("workloads.compute.services[%d]", i),
				"SAE has no %g vCPU / %d MiB size; service %s will run with %g vCPU / %d MiB", s.CPU, s.MemoryMB, s.Name, cpu, mem).
				WithHint("set cpu: %g and memory_mb: %d to make the effective size explicit", cpu, mem))
		}
		if s.MinInstances == 0 {
			ds = append(ds, diag.Warnf(CodeSizingRounded, fmt.Sprintf("workloads.compute.services[%d].min_instances", i),
				"SAE keeps at least 1 instance: min_instances 0 becomes 1 for service %s", s.Name))
		}
	}
	if db := p.Workloads.Database; db.Enabled {
		if db.BackupRetentionDays < 7 {
			ds = append(ds, diag.Warnf(CodeSizingRounded, "workloads.database.backup_retention_days",
				"ApsaraDB RDS keeps backups for at least 7 days; backup_retention_days %d becomes 7", db.BackupRetentionDays))
		}
		if db.PointInTimeRecovery && !db.HighAvailability {
			ds = append(ds, diag.Warnf(CodeCapability, "workloads.database.point_in_time_recovery",
				"point-in-time restore is only available on the RDS High-availability Edition (high_availability: true)"))
		}
	}
	ds = append(ds, checkDBExtensions(a, p, commonExtensions, nil)...)
	ds = append(ds, checkCapabilities(a, p)...)
	return ds
}

func (a alibabaAdapter) PlanChecks(p *config.Platform, _ func(string) string) diag.Diagnostics {
	ds := append(planStateChecks(p), certificateCheck(p, a.Name(), "certificate_id", diag.SeverityError)...)
	if p.Protected && p.State.Backend == config.BackendOSS && !alibabaLocking(p) {
		ds = append(ds, diag.Errorf(CodePlanState, "extensions.alibaba.state_tablestore_endpoint",
			"protected environment %q requires state locking: set extensions.alibaba.state_tablestore_endpoint and state_tablestore_table", p.Environment))
	}
	return ds
}

func (alibabaAdapter) Providers() []TerraformProvider { return []TerraformProvider{PinAlicloud} }

// ProviderConfigs: the alicloud provider has no default_tags or account guard; the
// composition tags every resource from var.labels and the hermes-* tags.
func (alibabaAdapter) ProviderConfigs(p *config.Platform) map[string]map[string]any {
	return map[string]map[string]any{PinAlicloud.LocalName: {"region": p.Cloud.Region}}
}
