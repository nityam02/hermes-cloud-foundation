// Package providers holds the Go side of the provider adapters (aws, gcp, alibaba):
// region and sizing validation, extension-key validation, provider-specific rules,
// the capability matrix used for portability scoring, and the provider configuration
// the renderer writes into the root module.
//
// Each provider lives in ONE file (aws.go, gcp.go, alibaba.go) containing its capability
// matrix, region list and rules, so the matrix can be reconciled with the HCL modules in a
// single place. Exact engine provider pins live in versions.go.
package providers

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
)

// Diagnostic codes owned by this package (documented in docs/reference/diagnostics.md).
const (
	CodeRegion         = "HCF200"
	CodeSizing         = "HCF201"
	CodeSizingRounded  = "HCF202"
	CodeExtensionKey   = "HCF203"
	CodeExtensionValue = "HCF204"
	CodeAccountFormat  = "HCF205"
	CodePlanAccount    = "HCF206"
	CodeCertificate    = "HCF207"
	CodeStateEncrypt   = "HCF208"
	CodeDBExtension    = "HCF209"
	CodeCapability     = "HCF210"
	CodeCPUBilling     = "HCF211"
	CodePlanState      = "HCF212"
)

// Feature is a capability feature name, e.g. "compute.public_https".
type Feature string

// Capability features (the portability vocabulary shared with docs/providers/*.md).
const (
	ComputePublicHTTPS          Feature = "compute.public_https"
	ComputeScaleToZero          Feature = "compute.scale_to_zero"
	ComputeOtelSidecar          Feature = "compute.otel_sidecar"
	DatabaseHighAvailability    Feature = "database.high_availability"
	DatabasePostGIS             Feature = "database.postgis"
	DatabaseIAMAuth             Feature = "database.iam_auth"
	DatabasePITR                Feature = "database.pitr"
	DatabaseCustomerManagedKeys Feature = "database.customer_managed_keys"
	StorageVersioning           Feature = "storage.versioning"
	StorageRetention            Feature = "storage.retention"
	StorageCustomerManagedKeys  Feature = "storage.customer_managed_keys"
	MessagingDeadLetter         Feature = "messaging.dead_letter"
	MessagingRetention          Feature = "messaging.retention"
	SecretsNativeInjection      Feature = "secrets.native_injection"
	ObservabilityLogsRetention  Feature = "observability.logs_retention"
	ObservabilityAlerts         Feature = "observability.alerts"
	ObservabilityTracing        Feature = "observability.tracing"
	StateRemoteBackend          Feature = "state.remote_backend"
	StateLocking                Feature = "state.locking"
	StateClientSideEncryption   Feature = "state.client_side_encryption"
	IdentityPerService          Feature = "identity.per_service"
)

// AllFeatures lists every feature in display order.
var AllFeatures = []Feature{
	ComputePublicHTTPS, ComputeScaleToZero, ComputeOtelSidecar,
	DatabaseHighAvailability, DatabasePostGIS, DatabaseIAMAuth, DatabasePITR, DatabaseCustomerManagedKeys,
	StorageVersioning, StorageRetention, StorageCustomerManagedKeys,
	MessagingDeadLetter, MessagingRetention,
	SecretsNativeInjection,
	ObservabilityLogsRetention, ObservabilityAlerts, ObservabilityTracing,
	StateRemoteBackend, StateLocking, StateClientSideEncryption,
	IdentityPerService,
}

// Status of a capability on a provider.
type Status string

const (
	Implemented    Status = "implemented"
	Partial        Status = "partial"
	NotImplemented Status = "not-implemented"
)

// Weight is the portability-score weight of a status.
func (s Status) Weight() float64 {
	switch s {
	case Implemented:
		return 1
	case Partial:
		return 0.5
	}
	return 0
}

// Capability is the status of one feature on one provider.
type Capability struct {
	Status Status `json:"status"`
	Note   string `json:"note"`
}

// CapabilityMatrix maps every feature to its status on one provider.
type CapabilityMatrix map[Feature]Capability

// Get returns the capability for f (not-implemented when missing from the matrix).
func (m CapabilityMatrix) Get(f Feature) Capability {
	if c, ok := m[f]; ok {
		return c
	}
	return Capability{Status: NotImplemented, Note: "not listed in the capability matrix"}
}

// MatrixVerification states how the capability matrices were established. Every status is
// "implemented, not tested against a real cloud": the HCL modules are exercised with
// mock providers only.
const MatrixVerification = "statuses describe the Hermes OpenTofu modules as designed and mock-tested; none is verified against a real cloud account"

// ExtensionKey documents one extensions.<provider> key read by the adapter or module.
type ExtensionKey struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type,omitempty"`    // string (default) | bool | list (of strings)
	Pattern     string `json:"pattern,omitempty"` // optional regexp each string value must match
}

// TerraformProvider identifies the engine provider plugin used by a Hermes provider.
type TerraformProvider struct {
	LocalName string `json:"local_name"` // name in required_providers and the provider block
	Source    string `json:"source"`
	Version   string `json:"version"` // exact pin
}

// Adapter is the Go side of one provider.
type Adapter interface {
	// Name is the spec value of cloud.provider (aws | gcp | alibaba).
	Name() string
	// DisplayName is a human-readable provider name.
	DisplayName() string
	// Capabilities is the capability matrix.
	Capabilities() CapabilityMatrix
	// Regions lists the regions known to Hermes (unknown regions produce a warning).
	Regions() []string
	// ExtensionKeys lists the documented extensions.<name> keys.
	ExtensionKeys() []ExtensionKey
	// Validate checks a normalized spec targeting this provider (validate time).
	Validate(p *config.Platform) diag.Diagnostics
	// CheckWorkloads checks only the region-independent workload rules (sizing,
	// database extensions, capabilities); portability uses it to compare providers.
	CheckWorkloads(p *config.Platform) diag.Diagnostics
	// PlanChecks are the additional rules enforced before planning; getenv reads the
	// process environment (injected for tests).
	PlanChecks(p *config.Platform, getenv func(string) string) diag.Diagnostics
	// Providers are the engine provider plugins (exact pins) the composition needs, in
	// addition to SharedProviders(); the first one is the primary cloud provider.
	Providers() []TerraformProvider
	// ProviderConfigs returns the provider block bodies rendered into the root module,
	// keyed by provider local name.
	ProviderConfigs(p *config.Platform) map[string]map[string]any
}

var registry = map[string]Adapter{}

func register(a Adapter) { registry[a.Name()] = a }

// Get returns the adapter for a provider name.
func Get(name string) (Adapter, bool) {
	a, ok := registry[name]
	return a, ok
}

// All returns every adapter sorted by name.
func All() []Adapter {
	out := make([]Adapter, 0, len(registry))
	for _, a := range registry {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Labels returns the user labels applied to every resource: project.labels plus a
// sanitized "owner" label from project.owner (unless labels already sets owner).
func Labels(p *config.Platform) map[string]string {
	out := map[string]string{}
	for k, v := range p.Project.Labels {
		out[k] = v
	}
	if _, set := out["owner"]; !set && p.Project.Owner != "" {
		if v := sanitizeLabel(p.Project.Owner); v != "" {
			out["owner"] = v
		}
	}
	return out
}

// ManagedTags are the hermes-* tags/labels set as provider defaults.
func ManagedTags(p *config.Platform) map[string]string {
	out := Labels(p)
	out["hermes-project"] = p.Project.Name
	out["hermes-environment"] = p.Environment
	out["hermes-managed-by"] = "hermes"
	return out
}

var labelInvalid = regexp.MustCompile(`[^a-z0-9_-]+`)

func sanitizeLabel(s string) string {
	s = labelInvalid.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}

// --- shared rule helpers -----------------------------------------------------------

func checkRegion(a Adapter, path, region string) diag.Diagnostics {
	for _, r := range a.Regions() {
		if r == region {
			return nil
		}
	}
	return diag.Diagnostics{diag.Warnf(CodeRegion, path, "region %q is not in the list of %s regions known to Hermes", region, a.DisplayName()).
		WithHint("check the spelling (%s regions look like %s); new regions may simply be missing from the list", a.Name(), strings.Join(a.Regions()[:2], ", "))}
}

func checkExtensions(a Adapter, p *config.Platform) diag.Diagnostics {
	var ds diag.Diagnostics
	known := map[string]ExtensionKey{}
	var names []string
	for _, k := range a.ExtensionKeys() {
		known[k.Name] = k
		names = append(names, k.Name)
	}
	ext := p.Extensions.For(a.Name())
	keys := make([]string, 0, len(ext))
	for k := range ext {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		path := "extensions." + a.Name() + "." + k
		spec, ok := known[k]
		if !ok {
			list := "none"
			if len(names) > 0 {
				list = strings.Join(names, ", ")
			}
			d := diag.Warnf(CodeExtensionKey, path, "unknown %s extension key %q (known: %s)", a.Name(), k, list)
			if s, ok := a.(interface{ strictExtensions() bool }); ok && s.strictExtensions() {
				d.Severity = diag.SeverityError // the composition rejects unknown keys
			}
			ds = append(ds, d)
			continue
		}
		var values []string
		switch spec.Type {
		case "bool":
			if _, ok := ext[k].(bool); !ok {
				ds = append(ds, diag.Errorf(CodeExtensionValue, path, "%s must be a boolean", path))
			}
			continue
		case "list":
			l, ok := ext[k].([]any)
			if !ok {
				ds = append(ds, diag.Errorf(CodeExtensionValue, path, "%s must be a list of strings", path))
				continue
			}
			for _, x := range l {
				s, ok := x.(string)
				if !ok {
					ds = append(ds, diag.Errorf(CodeExtensionValue, path, "%s must be a list of strings", path))
					break
				}
				values = append(values, s)
			}
		default:
			s, ok := ext[k].(string)
			if !ok {
				ds = append(ds, diag.Errorf(CodeExtensionValue, path, "%s must be a string", path))
				continue
			}
			values = []string{s}
		}
		for _, s := range values {
			if spec.Pattern != "" && s != "" && !regexp.MustCompile(spec.Pattern).MatchString(s) {
				ds = append(ds, diag.Errorf(CodeExtensionValue, path, "%s value %q does not match %s", path, s, spec.Pattern))
			}
		}
	}
	return ds
}

// checkCapabilities warns when the spec relies on a feature this provider does not
// implement, and notes partial support.
func checkCapabilities(a Adapter, p *config.Platform) diag.Diagnostics {
	var ds diag.Diagnostics
	m := a.Capabilities()
	for _, f := range Requested(p) {
		c := m.Get(f)
		switch c.Status {
		case NotImplemented:
			ds = append(ds, diag.Warnf(CodeCapability, "", "%s is requested by the spec but not implemented on %s: %s", f, a.DisplayName(), c.Note))
		case Partial:
			ds = append(ds, diag.Infof(CodeCapability, "", "%s is only partially supported on %s: %s", f, a.DisplayName(), c.Note))
		}
	}
	return ds
}

// checkDBExtensions validates requested PostgreSQL extensions against per-provider lists.
func checkDBExtensions(a Adapter, p *config.Platform, available, unavailable map[string]string) diag.Diagnostics {
	if !p.Workloads.Database.Enabled {
		return nil
	}
	var ds diag.Diagnostics
	for i, e := range p.Workloads.Database.Extensions {
		path := fmt.Sprintf("workloads.database.extensions[%d]", i)
		if why, bad := unavailable[e]; bad {
			ds = append(ds, diag.Errorf(CodeDBExtension, path, "PostgreSQL extension %q is not available on %s: %s", e, a.DisplayName(), why))
			continue
		}
		if _, ok := available[e]; !ok {
			ds = append(ds, diag.Warnf(CodeDBExtension, path, "PostgreSQL extension %q is not in the list Hermes knows to be available on %s; verify before relying on it", e, a.DisplayName()))
		}
	}
	return ds
}

// commonExtensions are PostgreSQL extensions documented as available on all three
// managed services (RDS, Cloud SQL, ApsaraDB RDS) for supported major versions.
var commonExtensions = map[string]string{
	"postgis": "", "postgis_topology": "", "pgcrypto": "", "pg_trgm": "", "uuid-ossp": "", "hstore": "",
	"citext": "", "pg_stat_statements": "", "btree_gist": "", "btree_gin": "", "fuzzystrmatch": "",
	"ltree": "", "intarray": "", "unaccent": "", "tablefunc": "", "vector": "",
}

func requireAccountForPlan(p *config.Platform) diag.Diagnostics {
	if p.Protected && p.Cloud.Account == "" {
		return diag.Diagnostics{diag.Errorf(CodePlanAccount, "cloud.account", "cloud.account is required to plan protected environment %q", p.Environment)}
	}
	return nil
}

// certificateCheck: public services in a protected environment need a TLS certificate
// (extensions.<provider>.<key>). It is a warning at validate time, so a freshly generated spec
// validates before the certificate exists, and an error before planning (PlanChecks), so a
// protected environment can never be planned, let alone applied, without HTTPS.
func certificateCheck(p *config.Platform, provider, key string, sev diag.Severity) diag.Diagnostics {
	if !p.Protected || !p.HasPublicService() || p.Extensions.String(provider, key) != "" {
		return nil
	}
	path := "extensions." + provider + "." + key
	d := diag.Errorf(CodeCertificate, path, "public services in protected environment %q require %s (HTTPS)", p.Environment, path)
	if sev == diag.SeverityWarning {
		d = diag.Warnf(CodeCertificate, path, "public services in protected environment %q require %s (HTTPS); plan will fail without it", p.Environment, path)
	}
	return diag.Diagnostics{d.WithHint("create or import a certificate for the service host name and set %s", path)}
}

// planStateChecks are the provider-independent state rules enforced before planning.
func planStateChecks(p *config.Platform) diag.Diagnostics {
	if p.State.Backend != config.BackendLocal && p.State.Bucket == "" {
		return diag.Diagnostics{diag.Errorf(CodePlanState, "state.bucket", "state.bucket is required to plan with the %s backend", p.State.Backend).
			WithHint("run `hermes state bootstrap` to create one, then set state.bucket")}
	}
	return nil
}

func validateCommon(a Adapter, p *config.Platform) diag.Diagnostics {
	var ds diag.Diagnostics
	ds = append(ds, checkRegion(a, "cloud.region", p.Cloud.Region)...)
	if p.State.Region != p.Cloud.Region && p.State.Backend == config.DefaultBackendFor(a.Name()) {
		ds = append(ds, checkRegion(a, "state.region", p.State.Region)...)
	}
	ds = append(ds, checkExtensions(a, p)...)
	ds = append(ds, a.CheckWorkloads(p)...)
	return ds
}

func init() {
	register(awsAdapter{})
	register(gcpAdapter{})
	register(alibabaAdapter{})
}
