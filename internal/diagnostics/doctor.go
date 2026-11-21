// Package diagnostics implements `hermes doctor`: local environment checks that never
// call a cloud API and never read credential values (only environment variable NAMES and
// whether well-known credential files exist).
package diagnostics

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/providers"
)

// Check statuses.
const (
	StatusOK   = "ok"
	StatusInfo = "info"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// RecommendedOpenTofu is the oldest OpenTofu line still receiving security fixes
// (open-source-landscape §10 C1: 1.8 and 1.9 lost support; 1.12+ supported).
var RecommendedOpenTofu = execution.Version{Major: 1, Minor: 12}

// LowDiskBytes triggers the free-disk warning (provider plugins are hundreds of MB each).
const LowDiskBytes = 5 << 30

// Check is one doctor finding.
type Check struct {
	Category string `json:"category"` // engine | tool | credentials | config | workspace
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
}

// Report is the doctor result.
type Report struct {
	Checks []Check `json:"checks"`
	// Failed is true when a required tool is missing or the spec is invalid (exit 2).
	Failed bool `json:"failed"`
}

// Env holds the doctor's inputs; every field has a real default.
type Env struct {
	WorkDir    string
	ConfigPath string // spec to validate ("" or missing file = skip)
	HomeDir    string
	Getenv     func(string) string
	LookPath   func(string) (string, error)
	Detect     func(ctx context.Context, preference string) (*execution.Engine, error)
	FreeBytes  func(path string) (uint64, error)
}

// OptionalTools are reported by presence only.
var OptionalTools = []struct{ Name, Purpose string }{
	{"conftest", "plan policy checks (required for protected environments unless --skip-policy)"},
	{"checkov", "IaC security scanning"},
	{"tflint", "HCL linting"},
	{"trivy", "IaC misconfiguration scanning"},
	{"infracost", "`hermes cost` estimates (AWS and GCP only)"},
	{"aws", "AWS CLI (not used by hermes)"},
	{"gcloud", "Google Cloud CLI (not used by hermes)"},
	{"aliyun", "Alibaba Cloud CLI (not used by hermes)"},
}

type credSource struct {
	provider string
	envNames []string
	files    []string // relative to the home directory
}

var credSources = []credSource{
	{"aws", []string{"AWS_ACCESS_KEY_ID", "AWS_PROFILE", "AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI"},
		[]string{".aws/credentials", ".aws/config"}},
	{"gcp", []string{"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CREDENTIALS", "GOOGLE_OAUTH_ACCESS_TOKEN", "GOOGLE_CLOUD_PROJECT"},
		[]string{".config/gcloud/application_default_credentials.json"}},
	{"alibaba", []string{"ALIBABA_CLOUD_ACCESS_KEY_ID", "ALICLOUD_ACCESS_KEY", "ALIBABA_CLOUD_CREDENTIALS_FILE", "ALIBABA_CLOUD_ROLE_ARN"},
		[]string{".aliyun/config.json", ".alibabacloud/credentials"}},
}

// Run executes every check.
func Run(ctx context.Context, e Env) Report {
	e = e.withDefaults()
	var r Report
	add := func(c Check) {
		r.Checks = append(r.Checks, c)
		if c.Status == StatusFail {
			r.Failed = true
		}
	}

	// Engine (required).
	eng, err := e.Detect(ctx, "auto")
	switch {
	case err != nil:
		add(Check{"engine", "opentofu/terraform", StatusFail, err.Error()})
	case eng.IsOpenTofu() && eng.Version.Less(RecommendedOpenTofu):
		add(Check{"engine", eng.Name, StatusWarn, fmt.Sprintf("OpenTofu %s at %s: older than %d.%d, the oldest line still receiving security fixes; upgrade", eng.Version, eng.Path, RecommendedOpenTofu.Major, RecommendedOpenTofu.Minor)})
	default:
		add(Check{"engine", eng.Name, StatusOK, fmt.Sprintf("%s %s at %s", eng.Name, eng.Version, eng.Path)})
	}

	for _, t := range OptionalTools {
		if p, err := e.LookPath(t.Name); err == nil {
			add(Check{"tool", t.Name, StatusOK, "found at " + p})
		} else {
			add(Check{"tool", t.Name, StatusInfo, "not found (optional: " + t.Purpose + ")"})
		}
	}

	for _, cs := range credSources {
		var found []string
		for _, n := range cs.envNames {
			if e.Getenv(n) != "" {
				found = append(found, "$"+n)
			}
		}
		for _, f := range cs.files {
			if e.HomeDir != "" {
				if _, err := os.Stat(filepath.Join(e.HomeDir, f)); err == nil {
					found = append(found, "~/"+f)
				}
			}
		}
		if len(found) > 0 {
			add(Check{"credentials", cs.provider, StatusInfo, "credential sources present (values not read): " + strings.Join(found, ", ")})
		} else {
			add(Check{"credentials", cs.provider, StatusInfo, "no credential sources detected"})
		}
	}

	add(configCheck(e.ConfigPath))
	add(gitignoreCheck(e.WorkDir))
	if free, err := e.FreeBytes(e.WorkDir); err != nil {
		add(Check{"workspace", "disk", StatusInfo, "free space unknown: " + err.Error()})
	} else if free < LowDiskBytes {
		add(Check{"workspace", "disk", StatusWarn, fmt.Sprintf("%.1f GiB free; provider plugins need several hundred MB each (use a shared TF_PLUGIN_CACHE_DIR)", float64(free)/(1<<30))})
	} else {
		add(Check{"workspace", "disk", StatusOK, fmt.Sprintf("%.1f GiB free", float64(free)/(1<<30))})
	}
	return r
}

func (e Env) withDefaults() Env {
	if e.WorkDir == "" {
		e.WorkDir, _ = os.Getwd()
	}
	if e.HomeDir == "" {
		e.HomeDir, _ = os.UserHomeDir()
	}
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.LookPath == nil {
		e.LookPath = execLookPath
	}
	if e.Detect == nil {
		e.Detect = execution.Detect
	}
	if e.FreeBytes == nil {
		e.FreeBytes = freeBytes
	}
	return e
}

func configCheck(path string) Check {
	if path == "" {
		return Check{"config", "spec", StatusInfo, "no spec given"}
	}
	if _, err := os.Stat(path); err != nil {
		return Check{"config", "spec", StatusInfo, path + " not found (run `hermes init`)"}
	}
	doc, ds, err := config.Load(path)
	if err != nil {
		return Check{"config", "spec", StatusFail, err.Error()}
	}
	if doc != nil && !ds.HasErrors() {
		envs := doc.Environments()
		if len(envs) == 0 {
			envs = []string{""} // reports "no environment selected"
		}
		for _, env := range envs {
			p, rds, err := doc.Resolve(env)
			if err != nil {
				return Check{"config", "spec", StatusFail, err.Error()}
			}
			ds = append(ds, rds...)
			if p != nil {
				if a, ok := providers.Get(p.Cloud.Provider); ok {
					ds = append(ds, a.Validate(p)...)
				}
			}
		}
	}
	errs, warns := ds.Count(diag.SeverityError), ds.Count(diag.SeverityWarning)
	switch {
	case errs > 0:
		return Check{"config", "spec", StatusFail, fmt.Sprintf("%s: %d error(s), %d warning(s); run `hermes validate`", path, errs, warns)}
	case warns > 0:
		return Check{"config", "spec", StatusWarn, fmt.Sprintf("%s: valid with %d warning(s); run `hermes validate`", path, warns)}
	}
	return Check{"config", "spec", StatusOK, path + ": valid"}
}

// GitignoreCovers reports whether a .gitignore in dir ignores the .hermes/ directory.
func GitignoreCovers(dir string) bool {
	f, err := os.Open(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		switch strings.TrimSpace(sc.Text()) {
		case ".hermes", ".hermes/", "/.hermes", "/.hermes/", ".hermes/*", "/.hermes/*", "**/.hermes/":
			return true
		}
	}
	return false
}

func gitignoreCheck(dir string) Check {
	if GitignoreCovers(dir) {
		return Check{"workspace", ".gitignore", StatusOK, ".hermes/ is ignored"}
	}
	return Check{"workspace", ".gitignore", StatusWarn, ".hermes/ is not in .gitignore: saved plans and state paths must not be committed (`hermes init` adds it)"}
}
