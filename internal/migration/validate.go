package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/yamlsafe"
)

// Checks is a ValidationChecks document.
type Checks struct {
	APIVersion string         `yaml:"apiVersion" json:"apiVersion"`
	Kind       string         `yaml:"kind" json:"kind"`
	Metadata   ChecksMetadata `yaml:"metadata" json:"metadata"`
	Checks     []Check        `yaml:"checks" json:"checks"`

	dir    string
	digest string
}

// ChecksMetadata names the check set.
type ChecksMetadata struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Check is one validation check. Fields apply per type:
//
//	spec-coverage: spec
//	bindings:      file, expect
//	http:          url, expect_status, body_contains, allow_insecure, ca_file
//	tcp:           address
//	dns:           name, record_type (A|AAAA|CNAME), expect
//	command:       argv (never a shell string), dir
type Check struct {
	ID             string       `yaml:"id" json:"id"`
	Type           string       `yaml:"type" json:"type"`
	Description    string       `yaml:"description,omitempty" json:"description,omitempty"`
	Steps          []string     `yaml:"steps,omitempty" json:"steps,omitempty"` // plan steps this check validates
	TimeoutSeconds int          `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
	Spec           string       `yaml:"spec,omitempty" json:"spec,omitempty"`
	File           string       `yaml:"file,omitempty" json:"file,omitempty"`
	Expect         *Expectation `yaml:"expect,omitempty" json:"expect,omitempty"`
	URL            string       `yaml:"url,omitempty" json:"url,omitempty"`
	ExpectStatus   int          `yaml:"expect_status,omitempty" json:"expect_status,omitempty"`
	BodyContains   string       `yaml:"body_contains,omitempty" json:"body_contains,omitempty"`
	AllowInsecure  bool         `yaml:"allow_insecure,omitempty" json:"allow_insecure,omitempty"`
	CAFile         string       `yaml:"ca_file,omitempty" json:"ca_file,omitempty"`
	Address        string       `yaml:"address,omitempty" json:"address,omitempty"`
	Name           string       `yaml:"name,omitempty" json:"name,omitempty"`
	RecordType     string       `yaml:"record_type,omitempty" json:"record_type,omitempty"`
	Argv           []string     `yaml:"argv,omitempty" json:"argv,omitempty"`
	Dir            string       `yaml:"dir,omitempty" json:"dir,omitempty"`
}

// Expectation is used by bindings (names that must exist) and dns (values to resolve to).
type Expectation struct {
	Buckets       []string `yaml:"buckets,omitempty" json:"buckets,omitempty"`
	Topics        []string `yaml:"topics,omitempty" json:"topics,omitempty"`
	Subscriptions []string `yaml:"subscriptions,omitempty" json:"subscriptions,omitempty"`
	Secrets       []string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Services      []string `yaml:"services,omitempty" json:"services,omitempty"`
	Database      bool     `yaml:"database,omitempty" json:"database,omitempty"`
	Values        []string `yaml:"values,omitempty" json:"values,omitempty"`
}

// CheckTypes lists supported check types.
var CheckTypes = []string{"spec-coverage", "bindings", "http", "tcp", "dns", "command"}

// LoadChecks reads and validates a checks file.
func LoadChecks(path string) (*Checks, error) {
	data, err := yamlsafe.ReadFile(path, yamlsafe.MaxBytes)
	if err != nil {
		return nil, fmt.Errorf("checks: %w", err)
	}
	var c Checks
	if err := yamlsafe.DecodeStrict(data, yamlsafe.MaxBytes, &c); err != nil {
		return nil, fmt.Errorf("checks %s: %w", path, err)
	}
	c.dir = filepath.Dir(path)
	c.digest = digest(data)
	var errs []string
	if c.APIVersion != APIVersion || c.Kind != "ValidationChecks" {
		errs = append(errs, "apiVersion/kind must be "+APIVersion+"/ValidationChecks")
	}
	seen := map[string]bool{}
	for i, ch := range c.Checks {
		if ch.ID == "" || seen[ch.ID] {
			errs = append(errs, fmt.Sprintf("checks[%d]: id missing or duplicate", i))
		}
		seen[ch.ID] = true
		if !oneOf(ch.Type, CheckTypes) {
			errs = append(errs, fmt.Sprintf("%s: unknown type %q (want one of %s)", ch.ID, ch.Type, strings.Join(CheckTypes, ", ")))
		}
		switch ch.Type {
		case "spec-coverage":
			if ch.Spec == "" {
				errs = append(errs, ch.ID+": spec is required")
			}
		case "bindings":
			if ch.File == "" {
				errs = append(errs, ch.ID+": file is required")
			}
		case "http":
			u, err := url.Parse(ch.URL)
			if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
				errs = append(errs, ch.ID+": url must be an absolute http(s) URL")
			} else if u.Scheme == "http" && !ch.AllowInsecure {
				errs = append(errs, ch.ID+": plain http requires allow_insecure: true (TLS is required by default)")
			}
		case "tcp":
			if _, _, err := net.SplitHostPort(ch.Address); err != nil {
				errs = append(errs, ch.ID+": address must be host:port")
			}
		case "dns":
			if ch.Name == "" || ch.Expect == nil || len(ch.Expect.Values) == 0 {
				errs = append(errs, ch.ID+": name and expect.values are required")
			}
		case "command":
			if len(ch.Argv) == 0 {
				errs = append(errs, ch.ID+": argv is required (a list; commands never run through a shell)")
			}
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("checks %s:\n  %s", path, strings.Join(errs, "\n  "))
	}
	return &c, nil
}

// Report is a ValidationReport document.
type Report struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   ReportMetadata `json:"metadata"`
	Summary    ReportSummary  `json:"summary"`
	Passed     bool           `json:"passed"`
	Results    []CheckResult  `json:"results"`
}

// ReportMetadata ties the report to a plan.
type ReportMetadata struct {
	PlanID       string `json:"plan_id"`
	ChecksName   string `json:"checks_name"`
	ChecksDigest string `json:"checks_digest"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	Generator    string `json:"generator"`
}

// Recomputation is the outcome of a report derived from its per-check results only.
type Recomputation struct {
	Passed  bool
	Summary ReportSummary
}

// Recomputed derives pass/fail and the counts from Results, ignoring the report's own
// passed and summary fields (which an edited report could set freely). A report passes only
// when it has at least one result and every result has status "pass".
func (r *Report) Recomputed() Recomputation {
	var sum ReportSummary
	for _, res := range r.Results {
		sum.Total++
		switch res.Status {
		case "pass":
			sum.Passed++
		case "fail":
			sum.Failed++
		default:
			sum.Errors++ // "error" and any unknown status
		}
	}
	return Recomputation{Passed: sum.Total > 0 && sum.Passed == sum.Total, Summary: sum}
}

// ReportSummary counts results.
type ReportSummary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
	Errors int `json:"errors"`
}

// CheckResult is one check outcome with evidence.
type CheckResult struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Status     string         `json:"status"` // pass | fail | error
	Steps      []string       `json:"steps,omitempty"`
	StartedAt  string         `json:"started_at"`
	DurationMS int64          `json:"duration_ms"`
	Message    string         `json:"message"`
	Evidence   map[string]any `json:"evidence"`
}

// Validator runs checks. Resolver and HTTPClient are replaceable in tests.
type Validator struct {
	Plan      *Plan
	Generator string
	Now       func() time.Time
	Resolver  *net.Resolver
	// Output receives the output of `command` checks (streamed, never stored); nil discards it.
	Output io.Writer
}

// Run executes every check (sequentially, each with its own timeout) and returns the report.
func (v *Validator) Run(ctx context.Context, c *Checks) *Report {
	now := v.Now
	if now == nil {
		now = time.Now
	}
	rep := &Report{APIVersion: APIVersion, Kind: "ValidationReport", Results: []CheckResult{}}
	rep.Metadata = ReportMetadata{PlanID: v.Plan.Metadata.PlanID, ChecksName: c.Metadata.Name, ChecksDigest: c.digest,
		StartedAt: now().UTC().Format(time.RFC3339), Generator: v.Generator}
	for _, ch := range c.Checks {
		start := now()
		timeout := time.Duration(ch.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		status, msg, ev := v.run(cctx, c, ch)
		cancel()
		if ev == nil {
			ev = map[string]any{}
		}
		res := CheckResult{ID: ch.ID, Type: ch.Type, Status: status, Steps: ch.Steps, StartedAt: start.UTC().Format(time.RFC3339),
			DurationMS: now().Sub(start).Milliseconds(), Message: msg, Evidence: ev}
		rep.Results = append(rep.Results, res)
		rep.Summary.Total++
		switch status {
		case "pass":
			rep.Summary.Passed++
		case "fail":
			rep.Summary.Failed++
		default:
			rep.Summary.Errors++
		}
	}
	rep.Passed = rep.Summary.Total > 0 && rep.Summary.Passed == rep.Summary.Total
	rep.Metadata.FinishedAt = now().UTC().Format(time.RFC3339)
	return rep
}

func (v *Validator) path(c *Checks, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

func (v *Validator) run(ctx context.Context, c *Checks, ch Check) (string, string, map[string]any) {
	switch ch.Type {
	case "spec-coverage":
		return specCoverage(v.Plan, v.path(c, ch.Spec))
	case "bindings":
		return bindingsCheck(v.path(c, ch.File), ch.Expect)
	case "http":
		return httpCheck(ctx, ch, v.path(c, ch.CAFile))
	case "tcp":
		d := net.Dialer{}
		conn, err := d.DialContext(ctx, "tcp", ch.Address)
		if err != nil {
			return "fail", "connection failed: " + err.Error(), map[string]any{"address": ch.Address}
		}
		_ = conn.Close()
		return "pass", "connected", map[string]any{"address": ch.Address, "remote": conn.RemoteAddr().String()}
	case "dns":
		return dnsCheck(ctx, v.Resolver, ch)
	case "command":
		return commandCheck(ctx, ch, v.path(c, ch.Dir), c.dir, v.Output)
	}
	return "error", "unknown check type", nil
}

// specCoverage verifies offline that the target spec contains an element for every source
// resource within Hermes scope (plan coverage).
func specCoverage(p *Plan, specPath string) (string, string, map[string]any) {
	data, err := yamlsafe.ReadFile(specPath, yamlsafe.MaxBytes)
	if err != nil {
		return "error", err.Error(), nil
	}
	var spec map[string]any
	if err := yamlsafe.Unmarshal(data, yamlsafe.MaxBytes, &spec); err != nil {
		return "error", "spec: " + err.Error(), nil
	}
	have := map[string]map[string]bool{"bucket": {}, "topic": {}, "subscription": {}, "secret": {}, "service": {}}
	w, _ := spec["workloads"].(map[string]any)
	sub := func(m map[string]any, k string) map[string]any { x, _ := m[k].(map[string]any); return x }
	list := func(m map[string]any, k string) []any { x, _ := m[k].([]any); return x }
	name := func(x any) string { m, _ := x.(map[string]any); s, _ := m["name"].(string); return s }
	enabled := func(m map[string]any) bool {
		if m == nil {
			return false
		}
		e, ok := m["enabled"].(bool)
		return !ok || e
	}
	if st := sub(w, "storage"); enabled(st) {
		for _, b := range list(st, "buckets") {
			have["bucket"][name(b)] = true
		}
	}
	if m := sub(w, "messaging"); enabled(m) {
		for _, t := range list(m, "topics") {
			have["topic"][name(t)] = true
			tm, _ := t.(map[string]any)
			for _, s := range list(tm, "subscriptions") {
				have["subscription"][name(s)] = true
			}
		}
	}
	if s := sub(w, "secrets"); enabled(s) {
		for _, n := range list(s, "names") {
			if str, ok := n.(string); ok {
				have["secret"][str] = true
			}
		}
	}
	if c := sub(w, "compute"); c != nil {
		for _, s := range list(c, "services") {
			have["service"][name(s)] = true
		}
	}
	db := sub(w, "database")
	dbOK := enabled(db) && (db["engine"] == nil || db["engine"] == "postgresql")
	blockOK := func(path string) bool {
		switch path {
		case "network", "per-service identities", "workloads":
			return true
		}
		parts := strings.Split(strings.TrimPrefix(path, "workloads."), ".")
		blk := sub(w, parts[0])
		if parts[0] == "compute" {
			return blk != nil
		}
		return enabled(blk)
	}
	var missing []string
	covered := 0
	for _, it := range p.Coverage {
		ok := false
		switch it.Kind {
		case "network":
			ok = true // the network block always exists (defaulted)
		case "database":
			ok = dbOK
		case "block":
			ok = blockOK(it.Name)
		default:
			ok = have[it.Kind][it.Name]
		}
		if ok {
			covered++
		} else {
			missing = append(missing, fmt.Sprintf("%s %q (from %s)", it.Kind, it.Name, it.SourceID))
		}
	}
	ev := map[string]any{"spec": specPath, "in_scope": len(p.Coverage), "covered": covered, "missing": nonNil(missing)}
	if len(missing) > 0 {
		return "fail", fmt.Sprintf("%d of %d in-scope source resources are not covered by the target spec", len(missing), len(p.Coverage)), ev
	}
	return "pass", fmt.Sprintf("all %d in-scope source resources are covered", len(p.Coverage)), ev
}

// bindingsCheck reads `hermes bindings -o json` output (the bindings object, optionally
// wrapped as {"bindings": ...} or engine output {"bindings": {"value": ...}}).
func bindingsCheck(file string, exp *Expectation) (string, string, map[string]any) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "error", err.Error(), nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return "error", "bindings: " + err.Error(), nil
	}
	b := raw
	if inner, ok := raw["bindings"].(map[string]any); ok {
		b = inner
		if v, ok := inner["value"].(map[string]any); ok {
			b = v
		}
	}
	if exp == nil {
		exp = &Expectation{}
	}
	var missing []string
	keys := func(k string) map[string]any { m, _ := b[k].(map[string]any); return m }
	need := func(kind string, m map[string]any, names []string) {
		for _, n := range names {
			if _, ok := m[n]; !ok {
				missing = append(missing, kind+" "+n)
			}
		}
	}
	need("bucket", keys("buckets"), exp.Buckets)
	need("topic", keys("topics"), exp.Topics)
	need("secret", keys("secrets"), exp.Secrets)
	need("service", keys("services"), exp.Services)
	subs := map[string]any{}
	for _, t := range keys("topics") {
		if tm, ok := t.(map[string]any); ok {
			if sm, ok := tm["subscriptions"].(map[string]any); ok {
				for k, v := range sm {
					subs[k] = v
				}
			}
		}
	}
	need("subscription", subs, exp.Subscriptions)
	if exp.Database {
		if dbm, ok := b["database"].(map[string]any); !ok || dbm["endpoint"] == "" || dbm["endpoint"] == nil {
			missing = append(missing, "database endpoint")
		}
	}
	ev := map[string]any{"file": file, "provider": b["provider"], "missing": nonNil(missing)}
	if len(missing) > 0 {
		return "fail", "bindings missing: " + strings.Join(missing, ", "), ev
	}
	return "pass", "all expected bindings present", ev
}

func httpCheck(ctx context.Context, ch Check, caFile string) (string, string, map[string]any) {
	u, _ := url.Parse(ch.URL)
	if u.Scheme != "https" && !ch.AllowInsecure {
		return "error", "TLS required (set allow_insecure: true for plain http)", nil
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return "error", err.Error(), nil
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return "error", "ca_file contains no PEM certificates", nil
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ch.URL, nil)
	if err != nil {
		return "error", err.Error(), nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return "fail", "request failed: " + err.Error(), map[string]any{"url": ch.URL}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	want := ch.ExpectStatus
	if want == 0 {
		want = 200
	}
	ev := map[string]any{"url": ch.URL, "status": resp.StatusCode, "expected_status": want}
	if resp.TLS != nil {
		ev["tls_version"] = tls.VersionName(resp.TLS.Version)
	}
	if resp.StatusCode != want {
		return "fail", fmt.Sprintf("status %d, want %d", resp.StatusCode, want), ev
	}
	if ch.BodyContains != "" {
		found := bytes.Contains(body, []byte(ch.BodyContains))
		ev["body_contains"] = found
		if !found {
			return "fail", "response body does not contain the expected text", ev
		}
	}
	return "pass", fmt.Sprintf("status %d", resp.StatusCode), ev
}

func dnsCheck(ctx context.Context, r *net.Resolver, ch Check) (string, string, map[string]any) {
	if r == nil {
		r = net.DefaultResolver
	}
	var got []string
	var err error
	switch strings.ToUpper(ch.RecordType) {
	case "CNAME":
		var c string
		c, err = r.LookupCNAME(ctx, ch.Name)
		got = []string{strings.TrimSuffix(c, ".")}
	default:
		got, err = r.LookupHost(ctx, ch.Name)
	}
	ev := map[string]any{"name": ch.Name, "expected": ch.Expect.Values}
	if err != nil {
		return "fail", "lookup failed: " + err.Error(), ev
	}
	sort.Strings(got)
	ev["resolved"] = got
	set := map[string]bool{}
	for _, g := range got {
		set[strings.TrimSuffix(strings.ToLower(g), ".")] = true
	}
	var missing []string
	for _, w := range ch.Expect.Values {
		if !set[strings.TrimSuffix(strings.ToLower(w), ".")] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		return "fail", "does not resolve to " + strings.Join(missing, ", "), ev
	}
	return "pass", "resolves to the expected values", ev
}

// commandCheck runs a user-declared argv (no shell) with a timeout; exit 0 passes. Output may
// contain secrets, so like cutover hooks (F-11) it is streamed to term only; the evidence keeps
// the exit code, the output byte count and its sha256 (stdout and stderr combined), never the
// output itself. The duration is recorded on the result.
func commandCheck(ctx context.Context, ch Check, dir, base string, term io.Writer) (string, string, map[string]any) {
	if dir == "" {
		dir = base
	}
	if term == nil {
		term = io.Discard
	}
	cmd := exec.CommandContext(ctx, ch.Argv[0], ch.Argv[1:]...)
	cmd.Dir = dir
	digest := &countingHash{h: sha256.New()}
	out := io.MultiWriter(term, digest)
	cmd.Stdout, cmd.Stderr = out, out // the same comparable writer: exec serialises the writes
	err := cmd.Run()
	ev := map[string]any{"argv": ch.Argv}
	record := func() {
		ev["output_bytes"] = digest.n
		ev["output_sha256"] = hex.EncodeToString(digest.h.Sum(nil))
	}
	record()
	if ctx.Err() != nil {
		ev["exit_code"] = -1
		return "fail", "timed out", ev
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		ev["exit_code"] = ee.ExitCode()
		return "fail", fmt.Sprintf("exit code %d", ee.ExitCode()), ev
	}
	if err != nil {
		ev["exit_code"] = -1
		return "error", err.Error(), ev
	}
	ev["exit_code"] = 0
	return "pass", "exit code 0", ev
}

// LoadReport reads a validation report.
func LoadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("report: %w", err)
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("report %s: %w", path, err)
	}
	if r.APIVersion != APIVersion || r.Kind != "ValidationReport" {
		return nil, fmt.Errorf("report %s: not a %s ValidationReport", path, APIVersion)
	}
	return &r, nil
}
