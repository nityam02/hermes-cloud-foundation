package config

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
)

// Validate applies the provider-independent semantic rules to a normalized spec. It is
// called by Resolve; it is exported so that generated specs (e.g. migration drafts) can be
// checked after normalization. Provider-specific rules live in internal/providers.
func Validate(p *Platform) diag.Diagnostics {
	var ds diag.Diagnostics
	w := &p.Workloads

	// Reserved features.
	if w.Compute.Type == ComputeKubernetes {
		ds = append(ds, diag.Errorf(CodeNotImplemented, "workloads.compute.type", "compute.type kubernetes is not implemented in v1alpha1").
			WithHint("use type: containerized; run Kubernetes out-of-band if you need it"))
	}
	if w.Database.Enabled && w.Database.Engine != EnginePostgreSQL {
		ds = append(ds, diag.Errorf(CodeNotImplemented, "workloads.database.engine", "database.engine %s is not implemented in v1alpha1", w.Database.Engine).
			WithHint("use engine: postgresql"))
	}
	if w.DNS.Enabled {
		ds = append(ds, diag.Errorf(CodeNotImplemented, "workloads.dns.enabled", "dns.enabled: true is not implemented in v1alpha1").
			WithHint("manage DNS out-of-band and set dns.enabled: false"))
	}
	if w.Compute.Type == ComputeNone && len(w.Compute.Services) > 0 {
		ds = append(ds, diag.Errorf(CodeRange, "workloads.compute.type", "services are declared but compute.type is none").
			WithHint("set compute.type: containerized"))
	}

	// Unique names.
	ds = append(ds, unique("workloads.compute.services", "service", serviceNames(p))...)
	ds = append(ds, unique("workloads.storage.buckets", "bucket", bucketNames(p))...)
	ds = append(ds, unique("workloads.messaging.topics", "topic", topicNames(p))...)
	ds = append(ds, unique("workloads.messaging.topics", "subscription", p.SubscriptionNames())...)
	ds = append(ds, unique("workloads.secrets.names", "secret", w.Secrets.Names)...)

	buckets := setOf(bucketNames(p))
	topics := setOf(topicNames(p))
	subs := setOf(p.SubscriptionNames())
	secrets := setOf(w.Secrets.Names)

	for i, s := range w.Compute.Services {
		sp := fmt.Sprintf("workloads.compute.services[%d]", i)
		if s.MaxInstances < s.MinInstances {
			ds = append(ds, diag.Errorf(CodeRange, sp+".max_instances", "max_instances (%d) must be >= min_instances (%d)", s.MaxInstances, s.MinInstances))
		}
		for _, k := range sortedKeys(s.Env) {
			if reservedEnv(k) {
				ds = append(ds, diag.Errorf(CodeReservedEnv, sp+".env."+k, "env %s uses a reserved prefix (HERMES_ and OTEL_ are injected by the binding contract)", k))
			}
			if _, dup := s.SecretEnv[k]; dup {
				ds = append(ds, diag.Errorf(CodeReservedEnv, sp+".env."+k, "%s is set in both env and secret_env", k))
			}
		}
		for _, k := range sortedKeys(s.SecretEnv) {
			if reservedEnv(k) {
				ds = append(ds, diag.Errorf(CodeReservedEnv, sp+".secret_env."+k, "secret_env %s uses a reserved prefix (HERMES_ and OTEL_ are injected by the binding contract)", k))
			}
			ref := s.SecretEnv[k]
			if !w.Secrets.Enabled || !secrets[ref] {
				ds = append(ds, diag.Errorf(CodeSecretRef, sp+".secret_env."+k, "secret %q is not declared in workloads.secrets.names%s", ref, disabledNote(w.Secrets.Enabled, "secrets")))
			}
		}
		for _, b := range sortedKeys(s.Access.Buckets) {
			if !w.Storage.Enabled || !buckets[b] {
				ds = append(ds, diag.Errorf(CodeBucketRef, sp+".access.buckets."+b, "bucket %q is not declared in workloads.storage.buckets%s", b, disabledNote(w.Storage.Enabled, "storage")))
			}
		}
		for j, t := range s.Access.Publish {
			if !w.Messaging.Enabled || !topics[t] {
				ds = append(ds, diag.Errorf(CodeTopicRef, fmt.Sprintf("%s.access.publish[%d]", sp, j), "topic %q is not declared in workloads.messaging.topics%s", t, disabledNote(w.Messaging.Enabled, "messaging")))
			}
		}
		for j, c := range s.Access.Consume {
			if !w.Messaging.Enabled || !subs[c] {
				ds = append(ds, diag.Errorf(CodeSubscriptionRef, fmt.Sprintf("%s.access.consume[%d]", sp, j), "subscription %q is not declared under any workloads.messaging.topics[].subscriptions%s", c, disabledNote(w.Messaging.Enabled, "messaging")))
			}
		}
		if s.Access.Database && !w.Database.Enabled {
			ds = append(ds, diag.Errorf(CodeDatabaseRef, sp+".access.database", "access.database requires workloads.database to be enabled"))
		}
		ds = append(ds, checkImage(sp+".image", s.Image, p.Protected)...)
	}

	if w.Database.Enabled {
		if w.Database.MaxStorageGB < w.Database.StorageGB {
			ds = append(ds, diag.Errorf(CodeRange, "workloads.database.max_storage_gb", "max_storage_gb (%d) must be >= storage_gb (%d)", w.Database.MaxStorageGB, w.Database.StorageGB))
		}
		if v, err := strconv.Atoi(w.Database.Version); err == nil {
			switch {
			case v < 14:
				ds = append(ds, diag.Errorf(CodeDBVersion, "workloads.database.version", "PostgreSQL %d is past community end-of-life", v).WithHint("use 16 or 17"))
			case v == 14:
				ds = append(ds, diag.Warnf(CodeDBVersion, "workloads.database.version", "PostgreSQL 14 reaches community end-of-life in November 2026").WithHint("plan an upgrade to 16 or 17"))
			case v > 18:
				ds = append(ds, diag.Warnf(CodeDBVersion, "workloads.database.version", "PostgreSQL %d is newer than the versions Hermes knows about; check provider availability", v))
			}
		}
	}

	ds = append(ds, checkNetwork(p)...)
	ds = append(ds, checkState(p)...)

	// Protected-environment rules that can be checked without the provider adapter.
	if p.Cloud.Account == "" {
		switch {
		case p.Protected && p.Cloud.Provider != ProviderAlibaba:
			ds = append(ds, diag.Warnf(CodeAccount, "cloud.account", "cloud.account is required for protected environment %q; plan will fail without it", p.Environment).
				WithHint("set cloud.account%s", gcpEnvHint(p.Cloud.Provider)))
		case p.Cloud.Provider == ProviderAWS:
			ds = append(ds, diag.Warnf(CodeAccount, "cloud.account", "cloud.account is not set; the AWS provider cannot guard against applying to the wrong account").
				WithHint("set cloud.account to the 12-digit account ID (rendered as allowed_account_ids)"))
		case p.Cloud.Provider == ProviderGCP:
			ds = append(ds, diag.Warnf(CodeAccount, "cloud.account", "cloud.account (GCP project ID) is not set; plan requires it or GOOGLE_CLOUD_PROJECT"))
		}
	}
	return ds
}

func gcpEnvHint(provider string) string {
	if provider == ProviderGCP {
		return " (or export GOOGLE_CLOUD_PROJECT for plan)"
	}
	return ""
}

func checkNetwork(p *Platform) diag.Diagnostics {
	var ds diag.Diagnostics
	pfx, err := netip.ParsePrefix(p.Network.CIDR)
	if err != nil || !pfx.Addr().Is4() {
		return append(ds, diag.Errorf(CodeNetwork, "network.cidr", "network.cidr %q is not a valid IPv4 CIDR", p.Network.CIDR))
	}
	if pfx.Bits() > 20 {
		ds = append(ds, diag.Errorf(CodeNetwork, "network.cidr", "network.cidr must be /20 or larger (got /%d)", pfx.Bits()))
	}
	if pfx.Masked() != pfx {
		ds = append(ds, diag.Errorf(CodeNetwork, "network.cidr", "network.cidr %s has host bits set (did you mean %s?)", pfx, pfx.Masked()))
	}
	if !pfx.Addr().IsPrivate() {
		ds = append(ds, diag.Warnf(CodeNetwork, "network.cidr", "network.cidr %s is not an RFC 1918 private range", pfx))
	}
	return ds
}

func checkState(p *Platform) diag.Diagnostics {
	var ds diag.Diagnostics
	st := p.State
	if st.Backend == BackendLocal {
		if p.Protected {
			ds = append(ds, diag.Errorf(CodeProtected, "state.backend", "state.backend local is not allowed in protected environment %q", p.Environment).
				WithHint("use a remote backend (%s) created with `hermes state bootstrap`", DefaultBackendFor(p.Cloud.Provider)))
		}
	} else if st.Bucket == "" {
		ds = append(ds, diag.Warnf(CodeStateBucket, "state.bucket", "state.bucket is not set; plan requires it for the %s backend", st.Backend).
			WithHint("create one with `hermes state bootstrap` and set state.bucket"))
	}
	if want := DefaultBackendFor(p.Cloud.Provider); st.Backend != BackendLocal && st.Backend != want {
		ds = append(ds, diag.Warnf(CodeStateConfig, "state.backend", "state.backend %s differs from the %s default (%s); the rendered root will need credentials for both clouds", st.Backend, p.Cloud.Provider, want))
	}
	if st.LockTable != "" && st.Backend != BackendS3 {
		ds = append(ds, diag.Errorf(CodeStateConfig, "state.lock_table", "state.lock_table is only supported with the s3 backend"))
	}
	if st.Encryption.ClientSide {
		if st.Encryption.KMSKey == "" {
			ds = append(ds, diag.Errorf(CodeStateConfig, "state.encryption.kms_key", "state.encryption.client_side requires state.encryption.kms_key"))
		}
		if p.Engine.Binary == "terraform" {
			ds = append(ds, diag.Errorf(CodeStateConfig, "state.encryption.client_side", "client-side state encryption is an OpenTofu feature; engine.binary is terraform").
				WithHint("set engine.binary: tofu (or auto) or disable client_side"))
		}
	} else if st.Encryption.KMSKey != "" {
		ds = append(ds, diag.Warnf(CodeIgnored, "state.encryption.kms_key", "state.encryption.kms_key is ignored because client_side is false"))
	}
	return ds
}

// checkImage enforces pinned images: a tag (not latest) or a digest.
func checkImage(path, image string, protected bool) diag.Diagnostics {
	if strings.Contains(image, "@sha256:") {
		return nil
	}
	name := image
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	tag := ""
	if i := strings.LastIndex(name, ":"); i >= 0 {
		tag = name[i+1:]
	}
	if tag != "" && tag != "latest" {
		return nil
	}
	what := "is untagged"
	if tag == "latest" {
		what = "uses the mutable :latest tag"
	}
	if protected {
		return diag.Diagnostics{diag.Errorf(CodeImageTag, path, "image %q %s; protected environments require a pinned tag or @sha256 digest", image, what)}
	}
	return diag.Diagnostics{diag.Warnf(CodeImageTag, path, "image %q %s; deployments are not reproducible", image, what).
		WithHint("pin a version tag or an @sha256 digest")}
}

func reservedEnv(k string) bool {
	return strings.HasPrefix(k, reservedEnvPrefixHerm) || strings.HasPrefix(k, reservedEnvPrefixOtel)
}

func disabledNote(enabled bool, what string) string {
	if enabled {
		return ""
	}
	return fmt.Sprintf(" (%s is disabled)", what)
}

func unique(path, kind string, names []string) diag.Diagnostics {
	var ds diag.Diagnostics
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			ds = append(ds, diag.Errorf(CodeDuplicate, path, "duplicate %s name %q", kind, n))
		}
		seen[n] = true
	}
	return ds
}

func serviceNames(p *Platform) []string {
	var out []string
	for _, s := range p.Workloads.Compute.Services {
		out = append(out, s.Name)
	}
	return out
}

func bucketNames(p *Platform) []string {
	var out []string
	for _, b := range p.Workloads.Storage.Buckets {
		out = append(out, b.Name)
	}
	return out
}

func topicNames(p *Platform) []string {
	var out []string
	for _, t := range p.Workloads.Messaging.Topics {
		out = append(out, t.Name)
	}
	return out
}

func setOf(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
