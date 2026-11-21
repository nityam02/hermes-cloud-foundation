package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"

	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/internal/schema"
	"github.com/nityam02/hermes-cloud-foundation/schemas"
)

var envNameRe = regexp.MustCompile(`^[a-z][a-z0-9]{1,11}$`)

// Resolve produces the normalized spec for env ("" = project.environment): overlay,
// defaults, protected-environment rules and semantic validation. The returned error is
// non-nil only for internal failures; spec problems are error diagnostics, and the
// Platform is nil whenever the diagnostics contain errors.
func (d *Document) Resolve(env string) (*Platform, diag.Diagnostics, error) {
	if env == "" {
		env = d.DefaultEnvironment()
	}
	if env == "" {
		return nil, d.locate(diag.Diagnostics{
			diag.Errorf(CodeEnvironment, "project.environment", "no environment selected").
				WithHint("set project.environment or pass --env"),
		}, ""), nil
	}
	if !envNameRe.MatchString(env) {
		return nil, diag.Diagnostics{diag.Errorf(CodeEnvironment, "", "environment %q must match ^[a-z][a-z0-9]{1,11}$", env)}, nil
	}
	if !slices.Contains(d.Environments(), env) && !slices.Contains(d.protectedEnvironments(), env) {
		// A typo in --env must never silently produce an unprotected environment.
		return nil, d.locate(diag.Diagnostics{
			diag.Errorf(CodeEnvironment, "environments", "environment %q is not declared (declared: %v)", env, d.Environments()).
				WithHint("declare it under environments: (an empty overlay `%s: {}` is enough)", env),
		}, ""), nil
	}

	merged := d.Raw()
	delete(merged, "environments")
	if envs, ok := d.raw["environments"].(map[string]any); ok {
		if ov, ok := envs[env].(map[string]any); ok {
			merged = deepMerge(merged, deepCopy(ov).(map[string]any))
		}
	}

	var ds diag.Diagnostics
	// The base document was schema-validated in Parse (including overlays against the
	// overlay schema). Re-validate the merged result only to catch combinations that are
	// invalid after merging; skip when the base already has schema errors.
	if base, _ := schema.Validate(schemas.PlatformV1Alpha1, d.raw); len(base) == 0 {
		mds, err := schema.Validate(schemas.PlatformV1Alpha1, merged)
		if err != nil {
			return nil, nil, fmt.Errorf("config: %w", err)
		}
		ds = append(ds, mds...)
	} else {
		ds = append(ds, base...)
	}
	if ds.HasErrors() {
		return nil, d.locate(ds, env), nil
	}

	protected := slices.Contains(d.protectedEnvironments(), env)
	ds = append(ds, applyDefaults(merged, protected)...)

	p, err := decode(merged)
	if err != nil {
		return nil, nil, fmt.Errorf("config: decode normalized spec: %w", err)
	}
	p.Environment = env
	p.Protected = protected
	p.Source = d.Filename

	ds = append(ds, Validate(p)...)
	ds = d.locate(ds, env)
	if ds.HasErrors() {
		return nil, ds, nil
	}
	return p, ds, nil
}

// deepMerge merges overlay onto base: maps merge recursively, everything else (including
// lists) is replaced. An explicit null in the overlay replaces the base value with null.
func deepMerge(base, overlay map[string]any) map[string]any {
	for k, ov := range overlay {
		if om, ok := ov.(map[string]any); ok {
			if bm, ok := base[k].(map[string]any); ok {
				base[k] = deepMerge(bm, om)
				continue
			}
		}
		base[k] = ov
	}
	return base
}

// applyDefaults fills every unset field of the merged spec (a schema-valid map) in place.
func applyDefaults(m map[string]any, protected bool) diag.Diagnostics {
	var ds diag.Diagnostics
	setDefault(m, "apiVersion", APIVersion)
	setDefault(m, "kind", KindPlatform)

	project := child(m, "project")
	setDefault(project, "owner", "")
	setDefault(project, "labels", map[string]any{})
	if _, ok := project["protected_environments"]; !ok {
		project["protected_environments"] = []any{DefaultProtectedEnv}
	} else if project["protected_environments"] == nil {
		project["protected_environments"] = []any{}
	}

	cloud := child(m, "cloud")
	setDefault(cloud, "account", "")
	provider, _ := cloud["provider"].(string)
	region, _ := cloud["region"].(string)

	setDefault(child(m, "engine"), "binary", "auto")

	state := child(m, "state")
	if b, _ := state["backend"].(string); b == "" || b == "auto" {
		state["backend"] = DefaultBackendFor(provider)
	}
	setDefault(state, "bucket", "")
	setDefault(state, "prefix", DefaultStatePrefix)
	setDefault(state, "region", region)
	setDefault(state, "lock_table", "")
	enc := child(state, "encryption")
	setDefault(enc, "client_side", false)
	setDefault(enc, "kms_key", "")

	network := child(m, "network")
	setDefault(network, "cidr", DefaultNetworkCIDR)
	setDefault(network, "zones", 2)
	setDefault(network, "nat", true)

	setDefault(child(m, "security"), "customer_managed_keys", protected)

	w := child(m, "workloads")

	// compute: an explicit block defaults to containerized; no block means none.
	if w["compute"] != nil {
		setDefault(child(w, "compute"), "type", ComputeContainerized)
	}
	compute := child(w, "compute")
	setDefault(compute, "type", ComputeNone)
	setDefault(compute, "services", []any{})
	for _, sv := range list(compute["services"]) {
		s := sv.(map[string]any)
		setDefault(s, "port", 8080)
		setDefault(s, "cpu", 0.5)
		setDefault(s, "memory_mb", 1024)
		setDefault(s, "min_instances", 1)
		if _, ok := s["max_instances"]; !ok || s["max_instances"] == nil {
			s["max_instances"] = max(3, toInt(s["min_instances"]))
		}
		setDefault(s, "public", false)
		setDefault(s, "health_check_path", "/healthz")
		setDefault(s, "command", []any{})
		setDefault(s, "env", map[string]any{})
		setDefault(s, "secret_env", map[string]any{})
		acc := child(s, "access")
		setDefault(acc, "buckets", map[string]any{})
		setDefault(acc, "publish", []any{})
		setDefault(acc, "consume", []any{})
		setDefault(acc, "database", false)
	}

	// database/storage/messaging/secrets: presence of the block implies enabled unless
	// enabled: false. observability is enabled unless explicitly disabled.
	for _, name := range []string{"database", "storage", "messaging", "secrets"} {
		present := w[name] != nil
		blk := child(w, name)
		setDefault(blk, "enabled", present)
	}
	setDefault(child(w, "observability"), "enabled", true)
	setDefault(child(w, "dns"), "enabled", false)

	db := child(w, "database")
	setDefault(db, "engine", EnginePostgreSQL)
	setDefault(db, "version", "16")
	setDefault(db, "size", "small")
	setDefault(db, "storage_gb", 20)
	setDefault(db, "max_storage_gb", max(100, toInt(db["storage_gb"])))
	setDefault(db, "high_availability", false)
	setDefault(db, "backup_retention_days", 7)
	setDefault(db, "point_in_time_recovery", true)
	setDefault(db, "deletion_protection", protected)
	setDefault(db, "extensions", []any{})

	st := child(w, "storage")
	if st["enabled"] == true {
		if _, ok := st["buckets"]; !ok || st["buckets"] == nil {
			st["buckets"] = []any{map[string]any{"name": DefaultBucketName}}
		}
	} else if len(list(st["buckets"])) > 0 {
		ds = append(ds, diag.Warnf(CodeIgnored, "workloads.storage.buckets", "buckets are ignored because storage is disabled"))
		st["buckets"] = []any{}
	}
	setDefault(st, "buckets", []any{})
	for _, bv := range list(st["buckets"]) {
		b := bv.(map[string]any)
		setDefault(b, "versioning", true)
		setDefault(b, "retention_days", 0)
	}

	msg := child(w, "messaging")
	if msg["enabled"] == true {
		if _, ok := msg["topics"]; !ok || msg["topics"] == nil {
			msg["topics"] = []any{map[string]any{
				"name":          DefaultTopicName,
				"subscriptions": []any{map[string]any{"name": DefaultSubscription}},
			}}
		}
	} else if len(list(msg["topics"])) > 0 {
		ds = append(ds, diag.Warnf(CodeIgnored, "workloads.messaging.topics", "topics are ignored because messaging is disabled"))
		msg["topics"] = []any{}
	}
	setDefault(msg, "topics", []any{})
	for _, tv := range list(msg["topics"]) {
		t := tv.(map[string]any)
		setDefault(t, "retention_hours", 168)
		setDefault(t, "subscriptions", []any{})
		for _, sv := range list(t["subscriptions"]) {
			s := sv.(map[string]any)
			setDefault(s, "ack_deadline_seconds", 30)
			setDefault(s, "max_delivery_attempts", 5)
		}
	}

	sec := child(w, "secrets")
	if sec["enabled"] != true && len(list(sec["names"])) > 0 {
		ds = append(ds, diag.Warnf(CodeIgnored, "workloads.secrets.names", "secret names are ignored because secrets are disabled"))
		sec["names"] = []any{}
	}
	setDefault(sec, "names", []any{})

	obs := child(w, "observability")
	setDefault(obs, "log_retention_days", 30)
	setDefault(obs, "metrics", true)
	setDefault(obs, "tracing", true)
	setDefault(obs, "alert_email", "")

	ext := child(m, "extensions")
	for _, p := range Providers {
		setDefault(ext, p, map[string]any{})
	}
	return ds
}

func child(m map[string]any, key string) map[string]any {
	if c, ok := m[key].(map[string]any); ok {
		return c
	}
	c := map[string]any{}
	m[key] = c
	return c
}

func setDefault(m map[string]any, key string, v any) {
	if cur, ok := m[key]; !ok || cur == nil {
		m[key] = v
	}
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	}
	return 0
}

func decode(m map[string]any) (*Platform, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var p Platform
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
