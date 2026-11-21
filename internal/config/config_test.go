package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
)

const fullSpec = `apiVersion: hermes/v1alpha1
kind: Platform
project:
  name: sample-app
  environment: dev
  labels: {cost-center: demo}
cloud: {provider: aws, region: eu-north-1, account: "123456789012"}
state: {bucket: example-hermes-state}
workloads:
  compute:
    services:
      - name: api
        image: ghcr.io/example/api:1.0.0
        public: true
        env: {LOG_LEVEL: info}
        secret_env: {DB_PASSWORD: db-password}
        access: {buckets: {artifacts: read-write}, publish: [telemetry], database: true}
      - name: worker
        image: ghcr.io/example/worker@sha256:0000000000000000000000000000000000000000000000000000000000000000
        cpu: 0.25
        memory_mb: 512
        min_instances: 0
        access: {consume: [telemetry-ingest]}
  database: {size: small}
  storage:
    buckets: [{name: artifacts}]
  messaging:
    topics:
      - name: telemetry
        subscriptions: [{name: telemetry-ingest}]
  secrets: {names: [db-password]}
environments:
  dev: {}
  production:
    network: {zones: 3}
    workloads:
      database: {high_availability: true, size: medium}
`

func mustParse(t *testing.T, src string) *Document {
	t.Helper()
	doc, ds, err := Parse([]byte(src), "hermes.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ds.HasErrors() {
		t.Fatalf("Parse diagnostics: %v", ds)
	}
	return doc
}

func mustResolve(t *testing.T, src, env string) (*Platform, diag.Diagnostics) {
	t.Helper()
	p, ds, err := mustParse(t, src).Resolve(env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ds.HasErrors() {
		t.Fatalf("Resolve diagnostics: %v", ds)
	}
	return p, ds
}

func hasCode(ds diag.Diagnostics, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestMinimalExampleFromDataContracts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "basic", "hermes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, pds, err := Parse(data, "hermes.yaml")
	if err != nil || pds.HasErrors() {
		t.Fatalf("parse: %v %v", err, pds)
	}
	p, ds, err := doc.Resolve("")
	if err != nil || ds.HasErrors() {
		t.Fatalf("resolve: %v %v", err, ds)
	}
	all := append(pds, ds...)
	// data-contracts §1: validates with warnings for apiVersion, cloud.account and state.bucket.
	for _, c := range []string{CodeAPIVersion, CodeAccount, CodeStateBucket} {
		if !hasCode(all, c) {
			t.Errorf("expected warning %s, got %v", c, all)
		}
	}
	if !p.Protected || p.Environment != "production" {
		t.Errorf("production must be protected by default: %+v", p)
	}
	w := p.Workloads
	if !w.Database.Enabled || !w.Database.DeletionProtection || !p.Security.CustomerManagedKeys {
		t.Errorf("protected defaults not applied: db=%+v sec=%+v", w.Database, p.Security)
	}
	if len(w.Storage.Buckets) != 1 || w.Storage.Buckets[0].Name != "data" || !w.Storage.Buckets[0].Versioning {
		t.Errorf("default bucket: %+v", w.Storage.Buckets)
	}
	if len(w.Messaging.Topics) != 1 || w.Messaging.Topics[0].Name != "events" ||
		w.Messaging.Topics[0].Subscriptions[0].Name != "events-default" {
		t.Errorf("default topic: %+v", w.Messaging.Topics)
	}
	if p.State.Backend != BackendGCS || p.State.Prefix != "hermes" || p.State.Region != "europe-north1" {
		t.Errorf("state defaults: %+v", p.State)
	}
	if w.Compute.Type != ComputeContainerized || w.Compute.Services == nil || len(w.Compute.Services) != 0 {
		t.Errorf("compute: %+v", w.Compute)
	}
	if !w.Observability.Enabled || w.Observability.LogRetentionDays != 30 || !w.Observability.Tracing {
		t.Errorf("observability: %+v", w.Observability)
	}
	if p.APIVersion != APIVersion || p.Kind != KindPlatform {
		t.Errorf("envelope: %s %s", p.APIVersion, p.Kind)
	}
}

func TestOverlayMergesMapsAndReplacesLists(t *testing.T) {
	dev, _ := mustResolve(t, fullSpec, "")
	if dev.Environment != "dev" || dev.Protected || dev.Network.Zones != 2 || dev.Workloads.Database.HighAvailability {
		t.Fatalf("dev: env=%s protected=%v zones=%d", dev.Environment, dev.Protected, dev.Network.Zones)
	}
	if dev.Workloads.Database.DeletionProtection || dev.Security.CustomerManagedKeys {
		t.Errorf("non-protected env must default deletion_protection/CMK to false")
	}
	prod, _ := mustResolve(t, fullSpec, "production")
	db := prod.Workloads.Database
	if prod.Network.Zones != 3 || !db.HighAvailability || db.Size != "medium" || !prod.Protected {
		t.Errorf("production overlay not applied: zones=%d db=%+v", prod.Network.Zones, db)
	}
	// maps merge: storage_gb from defaults, size from overlay, network.cidr kept.
	if db.StorageGB != 20 || prod.Network.CIDR != DefaultNetworkCIDR || !prod.Network.NAT {
		t.Errorf("deep merge lost base values: %+v %+v", db, prod.Network)
	}

	listSpec := strings.Replace(fullSpec, "  production:\n", "  production:\n    workloads:\n      secrets: {names: [other-secret]}\n", 1)
	listSpec = strings.Replace(listSpec, "    workloads:\n      database: {high_availability: true, size: medium}\n", "", 1)
	_, ds, err := mustParse(t, listSpec).Resolve("production")
	if err != nil {
		t.Fatal(err)
	}
	// lists replace: db-password disappears, so the api secret_env reference breaks.
	if !hasCode(ds, CodeSecretRef) {
		t.Errorf("expected list replacement to drop db-password, got %v", ds)
	}
}

func TestServiceDefaults(t *testing.T) {
	p, _ := mustResolve(t, fullSpec, "dev")
	api := p.Workloads.Compute.Services[0]
	if api.Port != 8080 || api.CPU != 0.5 || api.MemoryMB != 1024 || api.MinInstances != 1 || api.MaxInstances != 3 ||
		api.HealthCheckPath != "/healthz" || api.Command == nil || api.Access.Consume == nil {
		t.Errorf("service defaults: %+v", api)
	}
	sub := p.Workloads.Messaging.Topics[0].Subscriptions[0]
	if sub.AckDeadlineSeconds != 30 || sub.MaxDeliveryAttempts != 5 || p.Workloads.Messaging.Topics[0].RetentionHours != 168 {
		t.Errorf("messaging defaults: %+v", p.Workloads.Messaging)
	}
	if p.Workloads.Database.MaxStorageGB != 100 || p.Workloads.Database.Version != "16" {
		t.Errorf("database defaults: %+v", p.Workloads.Database)
	}
	if p.Extensions == nil || p.Extensions.For("aws") == nil {
		t.Errorf("extensions must be populated")
	}
}

func TestMinInstancesRaisesDefaultMax(t *testing.T) {
	src := strings.Replace(fullSpec, "        public: true\n", "        public: true\n        min_instances: 5\n", 1)
	p, _ := mustResolve(t, src, "dev")
	if s := p.Workloads.Compute.Services[0]; s.MaxInstances != 5 {
		t.Errorf("max_instances default must be >= min_instances, got %d", s.MaxInstances)
	}
}

func TestSemanticRules(t *testing.T) {
	cases := []struct {
		name string
		old  string
		new  string
		env  string
		code string
	}{
		{"unknown secret", "DB_PASSWORD: db-password", "DB_PASSWORD: nope", "", CodeSecretRef},
		{"unknown bucket", "buckets: {artifacts: read-write}", "buckets: {missing: read}", "", CodeBucketRef},
		{"unknown topic", "publish: [telemetry]", "publish: [nope]", "", CodeTopicRef},
		{"unknown subscription", "consume: [telemetry-ingest]", "consume: [nope]", "", CodeSubscriptionRef},
		{"database disabled", "database: {size: small}", "database: {enabled: false}", "", CodeDatabaseRef},
		{"reserved HERMES_ env", "LOG_LEVEL: info", "HERMES_REGION: x", "", CodeReservedEnv},
		{"reserved OTEL_ env", "LOG_LEVEL: info", "OTEL_SERVICE_NAME: x", "", CodeReservedEnv},
		{"env and secret_env clash", "LOG_LEVEL: info", "DB_PASSWORD: x", "", CodeReservedEnv},
		{"kubernetes reserved", "  compute:\n", "  compute:\n    type: kubernetes\n", "", CodeNotImplemented},
		{"mysql reserved", "database: {size: small}", "database: {engine: mysql}", "", CodeNotImplemented},
		{"dns reserved", "  secrets: {names: [db-password]}\n", "  secrets: {names: [db-password]}\n  dns: {enabled: true}\n", "", CodeNotImplemented},
		{"duplicate service", "      - name: worker\n", "      - name: api\n", "", CodeDuplicate},
		{"duplicate subscription", "subscriptions: [{name: telemetry-ingest}]", "subscriptions: [{name: telemetry-ingest}, {name: telemetry-ingest}]", "", CodeDuplicate},
		{"min > max", "        min_instances: 0\n", "        min_instances: 4\n        max_instances: 2\n", "", CodeRange},
		{"max storage < storage", "database: {size: small}", "database: {storage_gb: 200, max_storage_gb: 100}", "", CodeRange},
		{"latest image protected", "api:1.0.0", "api:latest", "production", CodeImageTag},
		{"untagged image protected", "api:1.0.0", "api", "production", CodeImageTag},
		{"local backend protected", "state: {bucket: example-hermes-state}", "state: {backend: local}", "production", CodeProtected},
		{"cidr too small", "    network: {zones: 3}", "    network: {zones: 3, cidr: 10.0.0.0/24}", "production", CodeNetwork},
		{"lock table non-s3", "state: {bucket: example-hermes-state}", "state: {bucket: example-hermes-state, backend: gcs, lock_table: locks}", "", CodeStateConfig},
		{"client side without key", "state: {bucket: example-hermes-state}", "state: {bucket: example-hermes-state, encryption: {client_side: true}}", "", CodeStateConfig},
		{"client side with terraform", "state: {bucket: example-hermes-state}", "engine: {binary: terraform}\nstate: {bucket: example-hermes-state, encryption: {client_side: true, kms_key: k}}", "", CodeStateConfig},
		{"eol postgres", "database: {size: small}", "database: {version: \"13\"}", "", CodeDBVersion},
		{"undeclared env", "", "", "prodution", CodeEnvironment},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := fullSpec
			if tc.old != "" {
				if !strings.Contains(src, tc.old) {
					t.Fatalf("fixture does not contain %q", tc.old)
				}
				src = strings.Replace(src, tc.old, tc.new, 1)
			}
			doc := mustParse(t, src)
			p, ds, err := doc.Resolve(tc.env)
			if err != nil {
				t.Fatal(err)
			}
			if !ds.HasErrors() || p != nil {
				t.Fatalf("expected errors, got %v", ds)
			}
			found := false
			for _, d := range ds {
				if d.Code == tc.code && d.Severity == diag.SeverityError {
					found = true
				}
			}
			if !found {
				t.Errorf("expected error %s, got %v", tc.code, ds)
			}
		})
	}
}

func TestWarningsOutsideProtected(t *testing.T) {
	src := strings.Replace(fullSpec, "api:1.0.0", "api:latest", 1)
	src = strings.Replace(src, `, account: "123456789012"`, "", 1)
	_, ds := mustResolve(t, src, "dev")
	if !hasCode(ds, CodeImageTag) || !hasCode(ds, CodeAccount) {
		t.Errorf("expected image and account warnings, got %v", ds)
	}
	// Protected env without an account is a warning at validate time (error at plan time).
	src2 := strings.Replace(fullSpec, `, account: "123456789012"`, "", 1)
	_, ds2 := mustResolve(t, src2, "production")
	if !hasCode(ds2, CodeAccount) {
		t.Errorf("expected account warning for protected env, got %v", ds2)
	}
}

func TestSchemaErrorsCarryLineNumbers(t *testing.T) {
	src := strings.Replace(fullSpec, "        cpu: 0.25\n", "        cpu: 3\n", 1)
	_, ds, err := Parse([]byte(src), "hermes.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var got *diag.Diagnostic
	for i := range ds {
		if ds[i].Code == CodeSchema {
			got = &ds[i]
		}
	}
	if got == nil {
		t.Fatalf("expected schema error, got %v", ds)
	}
	wantLine := 1 + strings.Count(src[:strings.Index(src, "cpu: 3")], "\n")
	if got.Path != "workloads.compute.services[1].cpu" || got.Line != wantLine || got.File != "hermes.yaml" {
		t.Errorf("diagnostic = %+v, want path workloads.compute.services[1].cpu line %d", *got, wantLine)
	}
	if !strings.Contains(got.String(), "hermes.yaml:") {
		t.Errorf("String() should include file:line: %s", got)
	}
}

func TestSemanticErrorsCarryOverlayLineNumbers(t *testing.T) {
	src := strings.Replace(fullSpec, "    network: {zones: 3}", "    network: {zones: 3, cidr: 10.0.0.0/24}", 1)
	_, ds, _ := mustParse(t, src).Resolve("production")
	for _, d := range ds {
		if d.Code == CodeNetwork && d.Severity == diag.SeverityError {
			want := 1 + strings.Count(src[:strings.Index(src, "cidr: 10.0.0.0/24")], "\n")
			if d.Line != want {
				t.Errorf("line = %d, want %d (overlay line)", d.Line, want)
			}
			return
		}
	}
	t.Fatalf("no network error: %v", ds)
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"bad yaml":        "project: {name: x\ncloud: [",
		"not a map":       "- a\n- b\n",
		"duplicate key":   "project: {name: abc}\nproject: {name: def}\n",
		"unknown field":   "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nworkload: {}\n",
		"bad provider":    "project: {name: abc}\ncloud: {provider: azure, region: x1}\n",
		"overlay project": "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nenvironments:\n  dev: {project: {protected_environments: []}}\n",
		"bad mda":         "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nworkloads: {messaging: {topics: [{name: t1, subscriptions: [{name: s1, max_delivery_attempts: 3}]}]}}\n",
		"retention":       "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nworkloads: {messaging: {topics: [{name: t1, retention_hours: 336}]}}\n",
		"unquoted ver":    "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nworkloads: {database: {version: 16}}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, ds, err := Parse([]byte(src), "x.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if !ds.HasErrors() {
				t.Fatalf("expected errors, got %v", ds)
			}
			for _, d := range ds {
				if d.Severity == diag.SeverityError && d.Line == 0 && name != "bad yaml" {
					t.Errorf("error without line number: %v", d)
				}
			}
		})
	}
}

func TestEnvironments(t *testing.T) {
	doc := mustParse(t, fullSpec)
	got := strings.Join(doc.Environments(), ",")
	if got != "dev,production" {
		t.Errorf("Environments() = %s", got)
	}
	// A custom protected list replaces the default: production is then unprotected (and warned
	// about, HCF122), live is protected.
	src := strings.Replace(fullSpec, "  environment: dev\n", "  environment: dev\n  protected_environments: [live]\n", 1)
	src = strings.Replace(src, "  dev: {}\n", "  dev: {}\n  live: {}\n", 1)
	doc2 := mustParse(t, src)
	if got := strings.Join(doc2.Environments(), ","); got != "dev,live,production" {
		t.Errorf("Environments() = %s", got)
	}
	p, _ := mustResolve(t, src, "production")
	if p.Protected {
		t.Errorf("production is not in the custom protected list")
	}
	if live, _ := mustResolve(t, src, "live"); !live.Protected {
		t.Errorf("live must be protected")
	}
}

// F-02: protection is membership of project.protected_environments, so a name mismatch must
// never silently drop it.
func TestProtectedEnvironmentNames(t *testing.T) {
	base := "apiVersion: hermes/v1alpha1\nkind: Platform\nproject:\n  name: shop\n  environment: dev\n%s" +
		"cloud: {provider: aws, region: eu-north-1, account: \"123456789012\"}\nstate: {bucket: example-state}\n" +
		"workloads: {}\nenvironments:\n%s"
	cases := []struct {
		name, protected, envs string
		errs, warns           []string // "code path"
	}{
		{name: "typo in protected list", protected: "  protected_environments: [prodution]\n", envs: "  dev: {}\n  production: {}\n",
			errs:  []string{"HCF121 project.protected_environments[0]"},
			warns: []string{"HCF122 environments.production"}},
		{name: "default list, prod-like names unprotected", envs: "  dev: {}\n  prod: {}\n  prd: {}\n",
			warns: []string{"HCF122 environments.prd", "HCF122 environments.prod"}},
		{name: "default list protects production", envs: "  dev: {}\n  production: {}\n"},
		{name: "default list without production is fine", envs: "  dev: {}\n  staging: {}\n"},
		{name: "explicitly empty list", protected: "  protected_environments: []\n", envs: "  dev: {}\n  live: {}\n",
			warns: []string{"HCF122 environments.live"}},
		{name: "custom list covering every prod-like env", protected: "  protected_environments: [prodeu, live]\n", envs: "  dev: {}\n  prodeu: {}\n  live: {}\n  preprod: {}\n"},
		{name: "project.environment counts as declared", protected: "  protected_environments: [dev]\n", envs: "  staging: {}\n"},
		{name: "custom list, no prod-like names", protected: "  protected_environments: [staging]\n", envs: "  staging: {}\n",
			warns: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := fmt.Sprintf(base, tc.protected, tc.envs)
			_, ds, err := Parse([]byte(src), "hermes.yaml")
			if err != nil {
				t.Fatal(err)
			}
			var errs, warns []string
			for _, d := range ds {
				if d.Code != CodeProtectedName && d.Code != CodeUnprotectedProd {
					continue
				}
				if d.Line == 0 {
					t.Errorf("diagnostic without a line: %v", d)
				}
				entry := d.Code + " " + d.Path
				if d.Severity == diag.SeverityError {
					errs = append(errs, entry)
				} else {
					warns = append(warns, entry)
				}
			}
			sort.Strings(errs)
			sort.Strings(warns)
			if strings.Join(errs, ";") != strings.Join(tc.errs, ";") || strings.Join(warns, ";") != strings.Join(tc.warns, ";") {
				t.Errorf("errors %v (want %v), warnings %v (want %v)", errs, tc.errs, warns, tc.warns)
			}
		})
	}

	// project.environment itself looks like production and is unprotected.
	src := strings.Replace(fmt.Sprintf(base, "  protected_environments: [staging]\n", "  staging: {}\n"), "environment: dev", "environment: live", 1)
	_, ds, err := Parse([]byte(src), "hermes.yaml")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range ds {
		if d.Code == CodeUnprotectedProd && d.Path == "project.environment" && d.Severity == diag.SeverityWarning {
			found = true
		}
	}
	if !found {
		t.Errorf("expected HCF122 at project.environment, got %v", ds)
	}

	// The QA reproduction: an undeclared protected name can no longer be resolved either.
	typo := fmt.Sprintf(base, "  protected_environments: [prodution]\n", "  dev: {}\n  production: {}\n")
	if _, ds, _ := Parse([]byte(typo), "hermes.yaml"); !ds.HasErrors() {
		t.Errorf("a protected_environments typo must be an error, got %v", ds)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected I/O error")
	}
}

// F-01: the QA "billion laughs" spec (9 levels of 9 aliases, 1.1 KB) must fail fast with
// HCF100 instead of expanding to gigabytes; any anchor or alias is rejected, and so is a spec
// larger than 1 MiB.
func TestRejectsYAMLAliasesAndHugeSpecs(t *testing.T) {
	var b strings.Builder
	b.WriteString("apiVersion: hermes/v1alpha1\nkind: Platform\n")
	b.WriteString("a: &a [\"lol\",\"lol\",\"lol\",\"lol\",\"lol\",\"lol\",\"lol\",\"lol\",\"lol\"]\n")
	prev := "a"
	for _, c := range "bcdefghi" {
		fmt.Fprintf(&b, "%c: &%c [%s]\n", c, c, strings.TrimSuffix(strings.Repeat("*"+prev+",", 9), ","))
		prev = string(c)
	}
	b.WriteString("project: {name: qa-proj}\ncloud: {provider: aws, region: eu-north-1}\n")
	cases := map[string]string{
		"billion laughs": b.String(),
		"plain alias":    strings.Replace(fullSpec, "  environment: dev\n", "  environment: &e dev\n  owner: *e\n", 1),
		"anchor only":    strings.Replace(fullSpec, "  environment: dev\n", "  environment: &e dev\n", 1),
	}
	for name, src := range cases {
		start := time.Now()
		_, ds, err := Parse([]byte(src), "hermes.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if !ds.HasErrors() || ds[0].Code != CodeYAML || !strings.Contains(ds[0].Message, "anchors and aliases") || ds[0].Line == 0 {
			t.Errorf("%s: want HCF100 for anchors/aliases with a line, got %v", name, ds)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s: must fail fast, took %v", name, d)
		}
	}

	big := fullSpec + "# " + strings.Repeat("x", 1<<20) + "\n"
	if _, ds, err := Parse([]byte(big), "hermes.yaml"); err != nil || !ds.HasErrors() || ds[0].Code != CodeYAML || !strings.Contains(ds[0].Message, "larger than") {
		t.Errorf("an oversized spec must be HCF100: %v %v", err, ds)
	}
	f := filepath.Join(t.TempDir(), "hermes.yaml")
	if err := os.WriteFile(f, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ds, err := Load(f); err != nil || !ds.HasErrors() || ds[0].Code != CodeYAML {
		t.Errorf("Load must report an oversized spec as HCF100: %v %v", err, ds)
	}
}
