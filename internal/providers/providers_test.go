package providers

import (
	"strconv"
	"strings"
	"testing"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
)

func resolve(t *testing.T, src, env string) *config.Platform {
	t.Helper()
	doc, ds, err := config.Parse([]byte(src), "hermes.yaml")
	if err != nil || ds.HasErrors() {
		t.Fatalf("parse: %v %v", err, ds)
	}
	p, ds, err := doc.Resolve(env)
	if err != nil || ds.HasErrors() {
		t.Fatalf("resolve: %v %v", err, ds)
	}
	return p
}

func spec(provider, region, extra string) string {
	return "apiVersion: hermes/v1alpha1\nproject: {name: demo-app, environment: dev}\ncloud: {provider: " + provider +
		", region: " + region + "}\nstate: {bucket: example-hermes-state}\n" + extra
}

func has(ds diag.Diagnostics, code string, sev diag.Severity) bool {
	for _, d := range ds {
		if d.Code == code && d.Severity == sev {
			return true
		}
	}
	return false
}

func TestRegistry(t *testing.T) {
	names := []string{}
	for _, a := range All() {
		names = append(names, a.Name())
	}
	if strings.Join(names, ",") != "alibaba,aws,gcp" {
		t.Fatalf("All() = %v", names)
	}
	if _, ok := Get("azure"); ok {
		t.Fatal("unexpected adapter")
	}
}

func TestCapabilityMatrixSanity(t *testing.T) {
	for _, a := range All() {
		m := a.Capabilities()
		if len(m) != len(AllFeatures) {
			t.Errorf("%s: matrix has %d features, want %d", a.Name(), len(m), len(AllFeatures))
		}
		for _, f := range AllFeatures {
			c, ok := m[f]
			if !ok {
				t.Errorf("%s: missing %s", a.Name(), f)
				continue
			}
			if c.Status != Implemented && c.Status != Partial && c.Status != NotImplemented {
				t.Errorf("%s %s: bad status %q", a.Name(), f, c.Status)
			}
			if strings.TrimSpace(c.Note) == "" {
				t.Errorf("%s %s: every status needs a note", a.Name(), f)
			}
		}
		if len(a.Regions()) < 2 {
			t.Errorf("%s: region list too short", a.Name())
		}
		for _, p := range append(a.Providers(), SharedProviders()...) {
			if p.Version == "" || strings.ContainsAny(p.Version, "~<>= ") {
				t.Errorf("%s: provider pin must be exact, got %q", a.Name(), p.Version)
			}
			if _, ok := a.ProviderConfigs(&config.Platform{})[p.LocalName]; !ok && p.LocalName != "random" {
				t.Errorf("%s: no provider block for %s", a.Name(), p.LocalName)
			}
		}
	}
	// Known hard facts from the research (open-source-landscape §10 C4).
	if c := (alibabaAdapter{}).Capabilities()[StateClientSideEncryption]; c.Status != NotImplemented {
		t.Errorf("alibaba client-side state encryption must be not-implemented")
	}
}

func TestRegions(t *testing.T) {
	cases := []struct {
		provider, region string
		warn             bool
	}{
		{"aws", "eu-north-1", false}, {"aws", "europe-north1", true},
		{"gcp", "europe-north1", false}, {"gcp", "eu-north-1", true},
		{"alibaba", "eu-central-1", false}, {"alibaba", "mars-1", true},
	}
	for _, c := range cases {
		a, _ := Get(c.provider)
		p := resolve(t, spec(c.provider, c.region, ""), "")
		if got := has(a.Validate(p), CodeRegion, diag.SeverityWarning); got != c.warn {
			t.Errorf("%s %s: region warning = %v, want %v", c.provider, c.region, got, c.warn)
		}
	}
}

func svc(cpu string, mem int, extra string) string {
	return "workloads:\n  compute:\n    services:\n      - {name: api, image: 'ghcr.io/example/api:1.0.0', cpu: " + cpu +
		", memory_mb: " + strconv.Itoa(mem) + extra + "}\n"
}

func TestFargateCombinations(t *testing.T) {
	cases := []struct {
		cpu  string
		mem  int
		want bool // error expected
	}{
		{"0.25", 512, false}, {"0.25", 2048, false}, {"0.25", 4096, true},
		{"0.5", 512, true}, {"0.5", 1024, false}, {"0.5", 4096, false}, {"0.5", 1536, true},
		{"1", 2048, false}, {"1", 1024, true}, {"2", 16384, false}, {"4", 4096, true}, {"4", 16384, false},
	}
	a, _ := Get("aws")
	for _, c := range cases {
		p := resolve(t, spec("aws", "eu-north-1", svc(c.cpu, c.mem, "")), "")
		if got := has(a.Validate(p), CodeSizing, diag.SeverityError); got != c.want {
			t.Errorf("fargate cpu=%s mem=%d: error=%v want %v", c.cpu, c.mem, got, c.want)
		}
	}
}

func TestCloudRunRounding(t *testing.T) {
	cases := []struct {
		cpu     float64
		mem     int
		wantCPU float64
		wantMem int
	}{{0.25, 512, 1, 512}, {0.5, 1024, 1, 1024}, {1, 4096, 1, 4096}, {1, 8192, 2, 8192}, {2, 16384, 4, 16384}, {4, 1024, 4, 2048}}
	for _, c := range cases {
		cpu, mem := CloudRunEffectiveSize(c.cpu, c.mem)
		if cpu != c.wantCPU || mem != c.wantMem {
			t.Errorf("CloudRunEffectiveSize(%g,%d) = %g/%d, want %g/%d", c.cpu, c.mem, cpu, mem, c.wantCPU, c.wantMem)
		}
	}
	a, _ := Get("gcp")
	p := resolve(t, spec("gcp", "europe-north1", svc("0.5", 2048, "")), "")
	ds := a.Validate(p)
	if !has(ds, CodeSizingRounded, diag.SeverityWarning) {
		t.Fatalf("expected rounding warning, got %v", ds)
	}
	for _, d := range ds {
		if d.Code == CodeSizingRounded && !strings.Contains(d.Message, "1 vCPU / 2048 MiB") {
			t.Errorf("warning must describe the effective size: %s", d.Message)
		}
	}
	if ds := a.Validate(resolve(t, spec("gcp", "europe-north1", "extensions: {gcp: {vpc_egress: everything}}\n"), "")); !has(ds, CodeExtensionValue, diag.SeverityError) {
		t.Errorf("invalid vpc_egress must fail: %v", ds)
	}
	if ds := a.Validate(resolve(t, spec("gcp", "europe-north1", "extensions: {gcp: {surprise: x}}\n"), "")); !has(ds, CodeExtensionKey, diag.SeverityError) {
		t.Errorf("unknown gcp key must be an error (the composition rejects it): %v", ds)
	}
}

func TestSAERounding(t *testing.T) {
	cases := []struct {
		cpu     float64
		mem     int
		wantCPU float64
		wantMem int
	}{{0.25, 512, 0.5, 1024}, {0.5, 1024, 0.5, 1024}, {0.5, 2048, 1, 2048}, {1, 3072, 1, 4096}, {2, 16384, 4, 16384}}
	for _, c := range cases {
		cpu, mem := SAEEffectiveSize(c.cpu, c.mem)
		if cpu != c.wantCPU || mem != c.wantMem {
			t.Errorf("SAEEffectiveSize(%g,%d) = %g/%d, want %g/%d", c.cpu, c.mem, cpu, mem, c.wantCPU, c.wantMem)
		}
	}
	a, _ := Get("alibaba")
	p := resolve(t, spec("alibaba", "eu-central-1", svc("0.25", 512, "")), "")
	if !has(a.Validate(p), CodeSizingRounded, diag.SeverityWarning) {
		t.Error("expected SAE rounding warning")
	}
}

func TestProviderSpecificRules(t *testing.T) {
	pub := "workloads:\n  compute:\n    services:\n      - {name: api, image: 'ghcr.io/example/api:1.0.0', public: true}\n"
	prodHeader := func(provider, region, account string) string {
		return "project: {name: demo-app, environment: production}\ncloud: {provider: " + provider + ", region: " + region +
			", account: '" + account + "'}\nstate: {bucket: example-hermes-state}\n"
	}
	aws, _ := Get("aws")
	// F-15: a warning at validate (a fresh `hermes init` spec validates), an error before planning.
	noCert := resolve(t, prodHeader("aws", "eu-north-1", "123456789012")+pub, "")
	if ds := aws.Validate(noCert); !has(ds, CodeCertificate, diag.SeverityWarning) || has(ds, CodeCertificate, diag.SeverityError) {
		t.Errorf("aws protected public service without certificate must warn at validate: %v", ds)
	}
	if ds := aws.PlanChecks(noCert, func(string) string { return "" }); !has(ds, CodeCertificate, diag.SeverityError) {
		t.Errorf("aws protected public service without certificate must not be planned: %v", ds)
	}
	withCert := prodHeader("aws", "eu-north-1", "123456789012") + pub +
		"extensions: {aws: {acm_certificate_arn: 'arn:aws:acm:eu-north-1:123456789012:certificate/abc-123'}}\n"
	if ds := aws.Validate(resolve(t, withCert, "")); ds.HasErrors() {
		t.Errorf("unexpected errors: %v", ds)
	}
	badCert := strings.Replace(withCert, "arn:aws:acm", "arn:aws:iam", 1)
	if ds := aws.Validate(resolve(t, badCert, "")); !has(ds, CodeExtensionValue, diag.SeverityError) {
		t.Errorf("malformed ARN must fail: %v", ds)
	}
	if ds := aws.Validate(resolve(t, spec("aws", "eu-north-1", "extensions: {aws: {surprise: x}}\n"), "")); !has(ds, CodeExtensionKey, diag.SeverityError) {
		t.Errorf("unknown aws extension key must be an error (the composition rejects it): %v", ds)
	}
	typed := "extensions: {aws: {cpu_architecture: ARM64, readonly_root_filesystem: false, alb_ingress_cidrs: ['10.0.0.0/8'], task_egress_cidrs: ['10.0.0.0/16', '192.0.2.0/24']}}\n"
	if ds := aws.Validate(resolve(t, spec("aws", "eu-north-1", typed), "")); ds.HasErrors() {
		t.Errorf("valid typed extensions rejected: %v", ds)
	}
	badTyped := "extensions: {aws: {cpu_architecture: arm, readonly_root_filesystem: 'no', alb_ingress_cidrs: '10.0.0.0/8', task_egress_cidrs: ['10.0.0.0/16', 'anywhere']}}\n"
	if ds := aws.Validate(resolve(t, spec("aws", "eu-north-1", badTyped), "")); ds.Count(diag.SeverityError) != 4 {
		t.Errorf("expected 4 extension value errors: %v", ds)
	}
	retention := "workloads:\n  observability: {log_retention_days: 10}\n  database: {point_in_time_recovery: false}\n"
	rds := aws.Validate(resolve(t, spec("aws", "eu-north-1", retention), ""))
	if !has(rds, CodeSizingRounded, diag.SeverityWarning) || !has(rds, CodeCapability, diag.SeverityWarning) || CloudWatchRetention(10) != 14 {
		t.Errorf("expected retention rounding and PITR warnings: %v", rds)
	}
	noNAT := strings.Replace(spec("aws", "eu-north-1", svc("0.5", 1024, "")), "state:", "network: {nat: false}\nstate:", 1)
	if ds := aws.Validate(resolve(t, noNAT, "")); !has(ds, CodeCapability, diag.SeverityWarning) {
		t.Errorf("nat: false must warn: %v", ds)
	}
	if ds := aws.Validate(resolve(t, strings.Replace(spec("aws", "eu-north-1", ""), "eu-north-1}", "eu-north-1, account: '1234'}", 1), "")); !has(ds, CodeAccountFormat, diag.SeverityError) {
		t.Errorf("bad account must fail: %v", ds)
	}

	ali, _ := Get("alibaba")
	enc := spec("alibaba", "eu-central-1", "")
	enc = strings.Replace(enc, "state: {bucket: example-hermes-state}", "state: {bucket: example-hermes-state, encryption: {client_side: true, kms_key: key-1}}", 1)
	if ds := ali.Validate(resolve(t, enc, "")); !has(ds, CodeStateEncrypt, diag.SeverityError) {
		t.Errorf("alibaba client-side encryption must fail: %v", ds)
	}
	aliNoCert := resolve(t, prodHeader("alibaba", "eu-central-1", "")+pub, "")
	if ds := ali.Validate(aliNoCert); !has(ds, CodeCertificate, diag.SeverityWarning) || has(ds, CodeCertificate, diag.SeverityError) {
		t.Errorf("alibaba protected public service without certificate must warn at validate: %v", ds)
	}
	if ds := ali.PlanChecks(aliNoCert, nil); !has(ds, CodeCertificate, diag.SeverityError) {
		t.Errorf("alibaba protected public service without certificate must not be planned: %v", ds)
	}
	trusted := "extensions: {alibaba: {workload_role_trusted_principals: ['acs:ram::1234567890123456:role/example-deployer']}}\n"
	if ds := ali.Validate(resolve(t, spec("alibaba", "eu-central-1", trusted), "")); ds.HasErrors() || has(ds, CodeExtensionKey, diag.SeverityWarning) {
		t.Errorf("explicit workload role principals must be accepted as a known key: %v", ds)
	}
	rootTrust := "extensions: {alibaba: {workload_role_trusted_principals: ['acs:ram::1234567890123456:root']}}\n"
	if ds := ali.Validate(resolve(t, spec("alibaba", "eu-central-1", rootTrust), "")); !has(ds, CodeExtensionValue, diag.SeverityError) {
		t.Errorf("trusting the account root must be rejected: %v", ds)
	}

	gcp, _ := Get("gcp")
	pg := resolve(t, spec("gcp", "europe-north1", ""), "")
	if ds := gcp.PlanChecks(pg, func(string) string { return "" }); !has(ds, CodePlanAccount, diag.SeverityError) {
		t.Errorf("gcp without project must fail at plan: %v", ds)
	}
	env := func(k string) string {
		if k == "GOOGLE_CLOUD_PROJECT" {
			return "demo-project-1"
		}
		return ""
	}
	if ds := gcp.PlanChecks(pg, env); ds.HasErrors() {
		t.Errorf("GOOGLE_CLOUD_PROJECT must satisfy the plan check: %v", ds)
	}
	prodGCP := resolve(t, "project: {name: demo-app, environment: production}\ncloud: {provider: gcp, region: europe-north1}\nstate: {bucket: example-hermes-state}\n", "")
	if ds := gcp.PlanChecks(prodGCP, env); !has(ds, CodePlanAccount, diag.SeverityError) {
		t.Errorf("protected gcp requires cloud.account in the spec: %v", ds)
	}
	noBucket := resolve(t, "project: {name: demo-app, environment: dev}\ncloud: {provider: aws, region: eu-north-1}\n", "")
	if ds := aws.PlanChecks(noBucket, env); !has(ds, CodePlanState, diag.SeverityError) {
		t.Errorf("missing state bucket must fail at plan: %v", ds)
	}
}

func TestDatabaseExtensions(t *testing.T) {
	src := spec("aws", "eu-north-1", "workloads:\n  database: {extensions: [postgis, timescaledb, made-up]}\n")
	aws, _ := Get("aws")
	ds := aws.Validate(resolve(t, src, ""))
	if !has(ds, CodeDBExtension, diag.SeverityError) || !has(ds, CodeDBExtension, diag.SeverityWarning) {
		t.Errorf("expected timescaledb error and unknown-extension warning: %v", ds)
	}
}

func TestProviderConfig(t *testing.T) {
	src := strings.Replace(spec("aws", "eu-north-1", ""), "eu-north-1}", "eu-north-1, account: '123456789012'}", 1)
	src = strings.Replace(src, "environment: dev}", "environment: dev, owner: 'Platform Team', labels: {cost-center: demo}}", 1)
	p := resolve(t, src, "")
	aws, _ := Get("aws")
	cfg := aws.ProviderConfigs(p)["aws"]
	if ids := cfg["allowed_account_ids"].([]string); len(ids) != 1 || ids[0] != "123456789012" {
		t.Errorf("allowed_account_ids: %v", cfg)
	}
	tags := cfg["default_tags"].(map[string]any)["tags"].(map[string]string)
	if tags["hermes-managed-by"] != "hermes" || tags["hermes-environment"] != "dev" || tags["owner"] != "platform-team" || tags["cost-center"] != "demo" {
		t.Errorf("tags: %v", tags)
	}
}

func TestPortability(t *testing.T) {
	src := spec("aws", "eu-north-1", `workloads:
  compute:
    services:
      - {name: api, image: 'ghcr.io/example/api:1.0.0', public: true, access: {database: true}}
      - {name: worker, image: 'ghcr.io/example/w:1.0.0', min_instances: 0, cpu: 0.25, memory_mb: 512}
  database: {extensions: [postgis], high_availability: true}
  storage: {buckets: [{name: media, retention_days: 30}]}
  messaging: {enabled: true}
`)
	r := Portability(resolve(t, src, ""))
	if len(r.Requested) == 0 || len(r.Providers) != 3 {
		t.Fatalf("report: %+v", r)
	}
	scores := map[string]float64{}
	for _, ps := range r.Providers {
		scores[ps.Provider] = ps.Score
		if ps.Score < 0 || ps.Score > 100 || len(ps.Features) != len(r.Requested) {
			t.Errorf("%s: score %v features %d", ps.Provider, ps.Score, len(ps.Features))
		}
		if ps.Provider == "aws" && !ps.Selected {
			t.Error("aws must be marked selected")
		}
	}
	if !(scores["gcp"] > scores["alibaba"]) {
		t.Errorf("expected gcp to score above alibaba for this spec: %v", scores)
	}
	if len(r.Differences) == 0 {
		t.Error("expected differences (e.g. compute.scale_to_zero)")
	}
	// Manual check of the formula for one provider.
	for _, ps := range r.Providers {
		sum := 0.0
		for _, f := range ps.Features {
			sum += f.Status.Weight()
		}
		want := float64(int(1000*sum/float64(len(ps.Features))+0.5)) / 10
		if ps.Score != want {
			t.Errorf("%s score %v != %v", ps.Provider, ps.Score, want)
		}
	}
}

// F-16: CIDR-list extensions and the AWS VPC size are checked at validate time (they used to
// fail only at plan or apply).
func TestAWSNetworkAndCIDRListRules(t *testing.T) {
	aws, _ := Get("aws")
	errs := func(ds diag.Diagnostics) []string {
		var out []string
		for _, d := range ds {
			if d.Severity == diag.SeverityError {
				out = append(out, d.Path+": "+d.Message)
			}
		}
		return out
	}
	for name, ext := range map[string]string{
		"empty egress":        "extensions: {aws: {task_egress_cidrs: []}}\n",
		"duplicate egress":    "extensions: {aws: {task_egress_cidrs: ['10.0.0.0/16', '10.0.0.0/16']}}\n",
		"normalised dup":      "extensions: {aws: {task_egress_cidrs: ['10.0.0.0/16', '010.0.0.0/16']}}\n",
		"host bits":           "extensions: {aws: {task_egress_cidrs: ['10.0.0.5/16']}}\n",
		"prefix out of range": "extensions: {aws: {task_egress_cidrs: ['10.0.0.0/40']}}\n",
		"ipv6 egress":         "extensions: {aws: {task_egress_cidrs: ['2001:db8::/32']}}\n",
		"empty ingress":       "extensions: {aws: {alb_ingress_cidrs: []}}\n",
		"dup ingress":         "extensions: {aws: {alb_ingress_cidrs: ['203.0.113.0/24', '203.0.113.0/24']}}\n",
		"ipv6 ingress":        "extensions: {aws: {alb_ingress_cidrs: ['::/0']}}\n",
	} {
		if got := errs(aws.Validate(resolve(t, spec("aws", "eu-north-1", ext), ""))); len(got) != 1 {
			t.Errorf("%s: want exactly one error, got %v", name, got)
		}
	}
	ok := "extensions: {aws: {task_egress_cidrs: ['10.0.0.0/16', '198.51.100.0/24'], alb_ingress_cidrs: ['0.0.0.0/0']}}\n"
	if got := errs(aws.Validate(resolve(t, spec("aws", "eu-north-1", ok), ""))); len(got) != 0 {
		t.Errorf("valid CIDR lists rejected: %v", got)
	}
	wide := strings.Replace(spec("aws", "eu-north-1", ""), "state:", "network: {cidr: 10.0.0.0/8}\nstate:", 1)
	if got := errs(aws.Validate(resolve(t, wide, ""))); len(got) != 1 || !strings.Contains(got[0], "/16") {
		t.Errorf("an AWS VPC wider than /16 must be rejected: %v", got)
	}
	exact := strings.Replace(spec("aws", "eu-north-1", ""), "state:", "network: {cidr: 10.0.0.0/16}\nstate:", 1)
	if got := errs(aws.Validate(resolve(t, exact, ""))); len(got) != 0 {
		t.Errorf("a /16 VPC must be accepted: %v", got)
	}
}
