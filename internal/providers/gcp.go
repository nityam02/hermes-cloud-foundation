package providers

import (
	"fmt"
	"regexp"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
)

// GCP: Cloud Run (v2), Cloud SQL for PostgreSQL, Cloud Storage, Pub/Sub, Secret Manager,
// Cloud Logging/Monitoring/Trace. Composition: infra/providers/gcp/platform.

type gcpAdapter struct{}

func (gcpAdapter) Name() string        { return config.ProviderGCP }
func (gcpAdapter) DisplayName() string { return "Google Cloud" }

func (gcpAdapter) Capabilities() CapabilityMatrix {
	return CapabilityMatrix{
		ComputePublicHTTPS:          {Implemented, "Cloud Run managed HTTPS endpoint (run.app URL); custom domains are out of scope (dns is reserved)"},
		ComputeScaleToZero:          {Implemented, "Cloud Run min_instances 0 with request-driven scale from zero"},
		ComputeOtelSidecar:          {Implemented, "OpenTelemetry Collector sidecar container exporting to Cloud Trace/Monitoring"},
		DatabaseHighAvailability:    {Implemented, "Cloud SQL availability_type REGIONAL"},
		DatabasePostGIS:             {Partial, "available on Cloud SQL; requires CREATE EXTENSION postgis after provisioning"},
		DatabaseIAMAuth:             {Implemented, "one Cloud SQL IAM database user per service account (HERMES_DB_AUTH=iam); GRANTs are manual"},
		DatabasePITR:                {Implemented, "automated backups with point_in_time_recovery_enabled"},
		DatabaseCustomerManagedKeys: {Implemented, "Cloud SQL CMEK via the google-beta service identity (Cloud Run and the log bucket stay Google-managed)"},
		StorageVersioning:           {Implemented, "object versioning"},
		StorageRetention:            {Implemented, "lifecycle Delete rule at age = retention_days"},
		StorageCustomerManagedKeys:  {Implemented, "CMEK default key on the bucket"},
		MessagingDeadLetter:         {Implemented, "Pub/Sub dead_letter_policy (5-100 attempts) with a dead-letter topic"},
		MessagingRetention:          {Implemented, "topic and subscription message retention (subscription maximum 7 days)"},
		SecretsNativeInjection:      {Implemented, "Cloud Run secret_key_ref environment variables (version latest)"},
		ObservabilityLogsRetention:  {Implemented, "dedicated log bucket + sink with a _Default exclusion (the exclusion is not verified on a real cloud)"},
		ObservabilityAlerts:         {Implemented, "Cloud Monitoring alert policies to an email notification channel"},
		ObservabilityTracing:        {Implemented, "OTLP to the collector sidecar, exported to Cloud Trace"},
		StateRemoteBackend:          {Implemented, "GCS backend"},
		StateLocking:                {Implemented, "GCS backend native locking"},
		StateClientSideEncryption:   {Implemented, "OpenTofu only: gcp_kms key provider with aes_gcm"},
		IdentityPerService:          {Implemented, "one service account per service, least privilege from access"},
	}
}

// gcpRegions: snapshot, November 2026.
var gcpRegions = []string{
	"us-central1", "us-east1", "us-east4", "us-east5", "us-south1", "us-west1", "us-west2", "us-west3", "us-west4",
	"northamerica-northeast1", "northamerica-northeast2", "northamerica-south1",
	"southamerica-east1", "southamerica-west1",
	"europe-central2", "europe-north1", "europe-north2", "europe-southwest1",
	"europe-west1", "europe-west2", "europe-west3", "europe-west4", "europe-west6", "europe-west8",
	"europe-west9", "europe-west10", "europe-west12",
	"me-central1", "me-central2", "me-west1", "africa-south1",
	"asia-east1", "asia-east2", "asia-northeast1", "asia-northeast2", "asia-northeast3",
	"asia-south1", "asia-south2", "asia-southeast1", "asia-southeast2",
	"australia-southeast1", "australia-southeast2",
}

func (gcpAdapter) Regions() []string { return gcpRegions }

func (gcpAdapter) ExtensionKeys() []ExtensionKey {
	return []ExtensionKey{
		{Name: "vpc_egress", Description: "Cloud Run Direct VPC egress: private-ranges-only (default) or all-traffic", Pattern: `^(private-ranges-only|all-traffic)$`},
		{Name: "otel_collector_image", Description: "OpenTelemetry Collector sidecar image (default: a pinned contrib image)", Pattern: `^\S+$`},
	}
}

// strictExtensions: the gcp composition rejects unknown extensions.gcp keys with a
// precondition, so the adapter reports them as errors.
func (gcpAdapter) strictExtensions() bool { return true }

var gcpProjectRe = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

func (a gcpAdapter) Validate(p *config.Platform) diag.Diagnostics {
	ds := validateCommon(a, p)
	if p.Cloud.Account != "" && !gcpProjectRe.MatchString(p.Cloud.Account) {
		ds = append(ds, diag.Errorf(CodeAccountFormat, "cloud.account", "GCP project ID must match %s (got %q)", gcpProjectRe, p.Cloud.Account))
	}
	return ds
}

// CloudRunEffectiveSize mirrors the rounding in infra/modules/gcp/container-service:
// CPU is rounded up to 1, 2, 4, 6 or 8 vCPU (fractional CPU is never used: below 1 vCPU
// Cloud Run forces concurrency 1), and raised so that memory above 4/8/16/24 GiB gets at
// least 2/4/6/8 vCPU; memory is raised to 2 GiB for 4 vCPU and 4 GiB for 6+ vCPU.
func CloudRunEffectiveSize(cpu float64, memoryMB int) (float64, int) {
	minCPU := 1.0
	switch {
	case memoryMB > 24576:
		minCPU = 8
	case memoryMB > 16384:
		minCPU = 6
	case memoryMB > 8192:
		minCPU = 4
	case memoryMB > 4096:
		minCPU = 2
	}
	eff := 8.0
	for _, c := range []float64{1, 2, 4, 6, 8} {
		if c >= max(cpu, minCPU) {
			eff = c
			break
		}
	}
	mem := memoryMB
	switch {
	case eff >= 6:
		mem = max(mem, 4096)
	case eff >= 4:
		mem = max(mem, 2048)
	default:
		mem = max(mem, 512)
	}
	return eff, mem
}

var gcpDBUnavailable = map[string]string{
	"timescaledb": "Cloud SQL for PostgreSQL does not offer TimescaleDB (see the reference architecture §5.2)",
}

func (a gcpAdapter) CheckWorkloads(p *config.Platform) diag.Diagnostics {
	var ds diag.Diagnostics
	sidecar := p.Workloads.Observability.Enabled && p.Workloads.Observability.Tracing
	for i, s := range p.Workloads.Compute.Services {
		path := fmt.Sprintf("workloads.compute.services[%d]", i)
		cpu, mem := CloudRunEffectiveSize(s.CPU, s.MemoryMB)
		if cpu != s.CPU || mem != s.MemoryMB {
			ds = append(ds, diag.Warnf(CodeSizingRounded, path,
				"Cloud Run sizing: service %s requests %g vCPU / %d MiB and will run with %g vCPU / %d MiB (the gcp composition rounds CPU up to 1, 2, 4, 6 or 8 and raises memory to Cloud Run minimums)",
				s.Name, s.CPU, s.MemoryMB, cpu, mem).
				WithHint("set cpu: %g and memory_mb: %d to make the effective size explicit", cpu, mem))
		}
		if !sidecar && len(s.Access.Consume) > 0 {
			ds = append(ds, diag.Warnf(CodeCPUBilling, path,
				"service %s consumes a subscription, but without the tracing sidecar the gcp composition allocates CPU only while serving requests; background pulling may stall", s.Name).
				WithHint("enable observability.tracing (CPU always allocated) or consume via push"))
		}
	}
	ds = append(ds, checkDBExtensions(a, p, commonExtensions, gcpDBUnavailable)...)
	ds = append(ds, checkCapabilities(a, p)...)
	return ds
}

func (a gcpAdapter) PlanChecks(p *config.Platform, getenv func(string) string) diag.Diagnostics {
	ds := requireAccountForPlan(p)
	if !p.Protected && p.Cloud.Account == "" && getenv("GOOGLE_CLOUD_PROJECT") == "" {
		ds = append(ds, diag.Errorf(CodePlanAccount, "cloud.account", "GCP needs a project: set cloud.account or export GOOGLE_CLOUD_PROJECT"))
	}
	return append(ds, planStateChecks(p)...)
}

// Providers: the composition uses google-beta for service identities (CMEK); both
// providers get the same configuration.
func (gcpAdapter) Providers() []TerraformProvider {
	return []TerraformProvider{PinGoogle, PinGoogleBeta}
}

func (gcpAdapter) ProviderConfigs(p *config.Platform) map[string]map[string]any {
	return map[string]map[string]any{
		PinGoogle.LocalName:     gcpProviderConfig(p),
		PinGoogleBeta.LocalName: gcpProviderConfig(p),
	}
}

func gcpProviderConfig(p *config.Platform) map[string]any {
	cfg := map[string]any{
		"region":         p.Cloud.Region,
		"default_labels": ManagedTags(p),
	}
	if p.Cloud.Account != "" {
		cfg["project"] = p.Cloud.Account
	}
	return cfg
}
