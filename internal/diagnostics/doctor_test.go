package diagnostics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
)

func env(t *testing.T, engineVersion *execution.Version) Env {
	t.Helper()
	home := t.TempDir()
	work := t.TempDir()
	return Env{
		WorkDir: work, HomeDir: home,
		Getenv: func(k string) string { return map[string]string{"AWS_PROFILE": "demo-not-a-secret"}[k] },
		LookPath: func(n string) (string, error) {
			if n == "conftest" {
				return "/fake/conftest", nil
			}
			return "", errors.New("no")
		},
		Detect: func(context.Context, string) (*execution.Engine, error) {
			if engineVersion == nil {
				return nil, execution.ErrEngineNotFound
			}
			return execution.NewEngine("tofu", "/fake/tofu", *engineVersion, nil), nil
		},
		FreeBytes: func(string) (uint64, error) { return 1 << 30, nil },
	}
}

func find(r Report, cat, name string) Check {
	for _, c := range r.Checks {
		if c.Category == cat && c.Name == name {
			return c
		}
	}
	return Check{}
}

func TestDoctorMissingEngineFails(t *testing.T) {
	r := Run(context.Background(), env(t, nil))
	if !r.Failed || find(r, "engine", "opentofu/terraform").Status != StatusFail {
		t.Fatalf("missing engine must fail: %+v", r)
	}
}

func TestDoctorChecks(t *testing.T) {
	old := execution.Version{Major: 1, Minor: 10, Patch: 3}
	e := env(t, &old)
	_ = os.MkdirAll(filepath.Join(e.HomeDir, ".aws"), 0o700)
	_ = os.WriteFile(filepath.Join(e.HomeDir, ".aws", "credentials"), []byte("[default]\naws_access_key_id = demo-not-a-secret\n"), 0o600)
	_ = os.WriteFile(filepath.Join(e.WorkDir, ".gitignore"), []byte("bin/\n.hermes/\n"), 0o644)
	r := Run(context.Background(), e)
	if r.Failed {
		t.Fatalf("unexpected failure: %+v", r)
	}
	if c := find(r, "engine", "tofu"); c.Status != StatusWarn || !strings.Contains(c.Detail, "1.12") {
		t.Errorf("old tofu must warn: %+v", c)
	}
	if c := find(r, "tool", "conftest"); c.Status != StatusOK {
		t.Errorf("conftest: %+v", c)
	}
	aws := find(r, "credentials", "aws")
	if !strings.Contains(aws.Detail, "$AWS_PROFILE") || !strings.Contains(aws.Detail, "~/.aws/credentials") {
		t.Errorf("aws credential sources: %+v", aws)
	}
	for _, c := range r.Checks {
		if strings.Contains(c.Detail, "demo-not-a-secret") {
			t.Fatalf("doctor must never print credential values: %+v", c)
		}
	}
	if c := find(r, "workspace", ".gitignore"); c.Status != StatusOK {
		t.Errorf("gitignore: %+v", c)
	}
	if c := find(r, "workspace", "disk"); c.Status != StatusWarn {
		t.Errorf("low disk must warn: %+v", c)
	}
}

func TestDoctorConfig(t *testing.T) {
	v := execution.Version{Major: 1, Minor: 13}
	e := env(t, &v)
	e.ConfigPath = filepath.Join(e.WorkDir, "hermes.yaml")
	_ = os.WriteFile(e.ConfigPath, []byte("project: {name: abc, environment: dev}\ncloud: {provider: aws, region: eu-north-1}\nworkloads: {compute: {type: kubernetes}}\n"), 0o600)
	r := Run(context.Background(), e)
	if !r.Failed || find(r, "config", "spec").Status != StatusFail {
		t.Fatalf("invalid spec must fail: %+v", find(r, "config", "spec"))
	}
	if find(r, "workspace", ".gitignore").Status != StatusWarn {
		t.Error("missing .gitignore must warn")
	}
}
