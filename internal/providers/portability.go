package providers

import (
	"math"
	"sort"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
)

// Requested returns the capability features a normalized spec relies on, in AllFeatures
// order. This is what portability is measured against: a feature the spec does not use
// does not affect its score.
func Requested(p *config.Platform) []Feature {
	w := &p.Workloads
	set := map[Feature]bool{}
	services := w.Compute.Type == config.ComputeContainerized && len(w.Compute.Services) > 0
	if services {
		set[IdentityPerService] = true
	}
	for _, s := range w.Compute.Services {
		if s.Public {
			set[ComputePublicHTTPS] = true
		}
		if s.MinInstances == 0 {
			set[ComputeScaleToZero] = true
		}
		if len(s.SecretEnv) > 0 {
			set[SecretsNativeInjection] = true
		}
		if s.Access.Database && w.Database.Enabled {
			set[DatabaseIAMAuth] = true
		}
	}
	if w.Observability.Enabled {
		set[ObservabilityLogsRetention] = true
		if w.Observability.Tracing {
			set[ObservabilityTracing] = true
			if services {
				set[ComputeOtelSidecar] = true
			}
		}
		if w.Observability.AlertEmail != "" {
			set[ObservabilityAlerts] = true
		}
	}
	if w.Database.Enabled {
		if w.Database.HighAvailability {
			set[DatabaseHighAvailability] = true
		}
		if w.Database.PointInTimeRecovery {
			set[DatabasePITR] = true
		}
		for _, e := range w.Database.Extensions {
			if e == "postgis" {
				set[DatabasePostGIS] = true
			}
		}
		if p.Security.CustomerManagedKeys {
			set[DatabaseCustomerManagedKeys] = true
		}
	}
	if w.Storage.Enabled && len(w.Storage.Buckets) > 0 {
		if p.Security.CustomerManagedKeys {
			set[StorageCustomerManagedKeys] = true
		}
		for _, b := range w.Storage.Buckets {
			if b.Versioning {
				set[StorageVersioning] = true
			}
			if b.RetentionDays > 0 {
				set[StorageRetention] = true
			}
		}
	}
	if w.Messaging.Enabled && len(w.Messaging.Topics) > 0 {
		set[MessagingRetention] = true
		for _, t := range w.Messaging.Topics {
			for _, s := range t.Subscriptions {
				if s.MaxDeliveryAttempts > 0 {
					set[MessagingDeadLetter] = true
				}
			}
		}
	}
	if p.State.Backend != config.BackendLocal {
		set[StateRemoteBackend] = true
		set[StateLocking] = true
	}
	if p.State.Encryption.ClientSide {
		set[StateClientSideEncryption] = true
	}
	var out []Feature
	for _, f := range AllFeatures {
		if set[f] {
			out = append(out, f)
		}
	}
	return out
}

// FeatureResult is one requested feature on one provider.
type FeatureResult struct {
	Feature Feature `json:"feature"`
	Status  Status  `json:"status"`
	Note    string  `json:"note"`
}

// ProviderScore is the portability result for one provider.
type ProviderScore struct {
	Provider    string           `json:"provider"`
	Selected    bool             `json:"selected"` // the spec's cloud.provider
	Score       float64          `json:"score"`    // 0-100, weighted share of requested features
	Features    []FeatureResult  `json:"features"`
	Diagnostics diag.Diagnostics `json:"diagnostics,omitempty"` // provider-specific workload findings
	Blocked     bool             `json:"blocked"`               // workload rules produce errors on this provider
}

// Difference is a requested feature whose status differs between providers.
type Difference struct {
	Feature  Feature               `json:"feature"`
	Statuses map[string]Capability `json:"statuses"`
}

// Report is the portability assessment of one environment of a spec.
type Report struct {
	Project      string          `json:"project"`
	Environment  string          `json:"environment"`
	Requested    []Feature       `json:"requested"`
	Providers    []ProviderScore `json:"providers"`
	Differences  []Difference    `json:"differences"`
	Verification string          `json:"verification"`
	Method       string          `json:"method"`
}

// ScoringMethod documents how scores are computed.
const ScoringMethod = "score = 100 * sum(weight(status of each requested feature)) / count(requested features); implemented = 1, partial = 0.5, not-implemented = 0. A provider is 'blocked' when its workload rules (sizing, extensions) report errors for this spec."

// Portability scores a normalized spec against every provider.
func Portability(p *config.Platform) Report {
	req := Requested(p)
	r := Report{
		Project: p.Project.Name, Environment: p.Environment, Requested: req,
		Verification: MatrixVerification, Method: ScoringMethod,
	}
	for _, a := range All() {
		m := a.Capabilities()
		ps := ProviderScore{Provider: a.Name(), Selected: a.Name() == p.Cloud.Provider, Score: 100}
		total := 0.0
		for _, f := range req {
			c := m.Get(f)
			ps.Features = append(ps.Features, FeatureResult{Feature: f, Status: c.Status, Note: c.Note})
			total += c.Status.Weight()
		}
		if len(req) > 0 {
			ps.Score = math.Round(1000*total/float64(len(req))) / 10
		}
		q := *p
		q.Cloud.Provider = a.Name()
		for _, d := range a.CheckWorkloads(&q) {
			if d.Code == CodeCapability {
				continue // already represented by the feature statuses
			}
			ps.Diagnostics = append(ps.Diagnostics, d)
		}
		ps.Blocked = ps.Diagnostics.HasErrors()
		r.Providers = append(r.Providers, ps)
	}
	for _, f := range req {
		st := map[string]Capability{}
		distinct := map[Status]bool{}
		for _, a := range All() {
			c := a.Capabilities().Get(f)
			st[a.Name()] = c
			distinct[c.Status] = true
		}
		if len(distinct) > 1 {
			r.Differences = append(r.Differences, Difference{Feature: f, Statuses: st})
		}
	}
	sort.SliceStable(r.Differences, func(i, j int) bool { return r.Differences[i].Feature < r.Differences[j].Feature })
	return r
}
