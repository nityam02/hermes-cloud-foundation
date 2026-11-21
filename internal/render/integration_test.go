//go:build integration

// Opt-in integration test: renders every example environment with the local backend and
// the repository's infra/ as the modules directory, then runs `init -backend=false` and
// `validate` with the real engine. It needs the provider plugins (use a shared
// TF_PLUGIN_CACHE_DIR) but no credentials, and it never plans or applies.
//
//	HERMES_TF_INTEGRATION=1 TF_PLUGIN_CACHE_DIR=$HOME/.cache/hermes-tf-plugins \
//	  go test -tags integration ./internal/render -run TestIntegration -v
//
// HERMES_TF_ENGINE=terraform runs it with Terraform instead of OpenTofu.
package render

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntegrationInitValidate(t *testing.T) {
	if os.Getenv("HERMES_TF_INTEGRATION") != "1" {
		t.Skip("set HERMES_TF_INTEGRATION=1 to run")
	}
	engine := os.Getenv("HERMES_TF_ENGINE")
	if engine == "" {
		engine = "tofu"
	}
	bin, err := exec.LookPath(engine)
	if err != nil {
		t.Skipf("%s not on PATH", engine)
	}
	if os.Getenv("TF_PLUGIN_CACHE_DIR") == "" {
		t.Fatal("set TF_PLUGIN_CACHE_DIR to a shared plugin cache (provider plugins are large)")
	}
	infraDir, err := filepath.Abs(filepath.Join("..", "..", "infra"))
	if err != nil {
		t.Fatal(err)
	}
	// Credential isolation: validate needs no credentials, and must never find real ones.
	env := []string{"AWS_CONFIG_FILE=/dev/null", "AWS_SHARED_CREDENTIALS_FILE=/dev/null", "AWS_PROFILE=",
		"AWS_EC2_METADATA_DISABLED=true", "GOOGLE_APPLICATION_CREDENTIALS=/dev/null", "ALIBABA_CLOUD_CREDENTIALS_FILE=/dev/null",
		"TF_IN_AUTOMATION=1", "TF_INPUT=0"}
	var base []string
	for _, kv := range os.Environ() {
		k := kv[:strings.IndexByte(kv, '=')]
		if strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "GOOGLE_") || strings.HasPrefix(k, "ALIBABA_") ||
			strings.HasPrefix(k, "ALICLOUD_") || strings.HasPrefix(k, "CLOUDSDK_") {
			continue
		}
		base = append(base, kv)
	}
	env = append(base, env...)

	for _, path := range examples(t) {
		name := filepath.Base(filepath.Dir(path))
		for envName, p := range resolveAll(t, path) {
			t.Run(name+"/"+envName, func(t *testing.T) {
				hermes := t.TempDir()
				res, err := Render(p, Options{
					OutDir: filepath.Join(hermes, "build", envName), HermesDir: hermes, ModulesDir: infraDir,
					LocalBackend: true, Engine: engine,
				})
				if err != nil {
					t.Fatal(err)
				}
				for _, args := range [][]string{{"init", "-backend=false", "-input=false", "-no-color"}, {"validate", "-no-color"}} {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					cmd := exec.CommandContext(ctx, bin, args...)
					cmd.Dir, cmd.Env = res.Dir, env
					var out bytes.Buffer
					cmd.Stdout, cmd.Stderr = &out, &out
					err := cmd.Run()
					cancel()
					if err != nil {
						t.Fatalf("%s %s: %v\n%s", engine, strings.Join(args, " "), err, out.String())
					}
				}
			})
		}
	}
}
