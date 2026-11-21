package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
	"github.com/nityam02/hermes-cloud-foundation/internal/providers"
)

var update = flag.Bool("update", false, "rewrite golden files")

// testModules stands in for the embedded module tree so golden files do not change when
// the HCL modules change.
var testModules = fstest.MapFS{
	"contract/variables.tf":                         {Data: []byte("# contract\n")},
	"providers/aws/platform/main.tf":                {Data: []byte("# aws\n")},
	"providers/gcp/platform/main.tf":                {Data: []byte("# gcp\n")},
	"providers/alibaba/platform/main.tf":            {Data: []byte("# alibaba\n")},
	"bootstrap/aws/state-backend/main.tf":           {Data: []byte("# aws bootstrap\n")},
	"modules/aws/network/main.tf":                   {Data: []byte("# net\n")},
	"providers/aws/platform/.terraform/ignored.txt": {Data: []byte("never extracted\n")},
}

func examples(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join("..", "..", "examples", "*", "hermes.yaml"))
	if err != nil || len(dirs) < 5 {
		t.Fatalf("expected at least 5 examples, got %v %v", dirs, err)
	}
	return dirs
}

func resolveAll(t *testing.T, path string) map[string]*config.Platform {
	t.Helper()
	doc, ds, err := config.Load(path)
	if err != nil || ds.HasErrors() {
		t.Fatalf("%s: %v %v", path, err, ds)
	}
	out := map[string]*config.Platform{}
	for _, env := range doc.Environments() {
		p, ds, err := doc.Resolve(env)
		if err != nil || ds.HasErrors() {
			t.Fatalf("%s env %s: %v %v", path, env, err, ds)
		}
		a, _ := providers.Get(p.Cloud.Provider)
		if pds := a.Validate(p); pds.HasErrors() {
			t.Fatalf("%s env %s: provider errors: %v", path, env, pds)
		}
		p.Source = "hermes.yaml"
		out[env] = p
	}
	return out
}

func TestGoldenExamples(t *testing.T) {
	for _, path := range examples(t) {
		name := filepath.Base(filepath.Dir(path))
		for env, p := range resolveAll(t, path) {
			t.Run(name+"/"+env, func(t *testing.T) {
				hermes := t.TempDir()
				opts := Options{
					OutDir: filepath.Join(hermes, "build", env), HermesDir: hermes, Version: "test",
					ModulesFS: testModules, LocalBackend: p.State.Bucket == "",
				}
				res, err := Render(p, opts)
				if err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(res.RootFile)
				if err != nil {
					t.Fatal(err)
				}
				golden := filepath.Join("testdata", "golden", name+"-"+env+".tf.json")
				if *update {
					_ = os.MkdirAll(filepath.Dir(golden), 0o755)
					if err := os.WriteFile(golden, got, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("missing golden file (run go test ./internal/render -update): %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s differs from golden; run go test ./internal/render -update and review the diff", golden)
				}
				// Determinism: a second render in another directory produces the same digest.
				hermes2 := t.TempDir()
				opts.OutDir, opts.HermesDir = filepath.Join(hermes2, "build", env), hermes2
				res2, err := Build(p, opts)
				if err != nil {
					t.Fatal(err)
				}
				if res2.Digest != res.Digest || res2.ModulesDigest != res.ModulesDigest {
					t.Errorf("render is not deterministic")
				}
				checkRoot(t, p, got)
			})
		}
	}
}

// checkRoot asserts structural properties every rendered root must have.
func checkRoot(t *testing.T, p *config.Platform, data []byte) {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	tf := root["terraform"].(map[string]any)
	for name, v := range tf["required_providers"].(map[string]any) {
		ver := v.(map[string]any)["version"].(string)
		if strings.ContainsAny(ver, "~<>= ") {
			t.Errorf("provider %s pin must be exact: %s", name, ver)
		}
	}
	mod := root["module"].(map[string]any)["platform"].(map[string]any)
	for _, k := range []string{"source", "project", "environment", "region", "protected", "labels", "network", "security",
		"compute", "database", "storage", "messaging", "secrets", "observability", "extensions"} {
		if _, ok := mod[k]; !ok {
			t.Errorf("module argument %s missing", k)
		}
	}
	if len(mod) != 15 {
		t.Errorf("module must pass exactly the contract variables + source, got %d keys", len(mod))
	}
	if src := mod["source"].(string); !strings.HasPrefix(src, "../") || !strings.HasSuffix(src, "/providers/"+p.Cloud.Provider+"/platform") {
		t.Errorf("module source %s", src)
	}
	outs := root["output"].(map[string]any)
	for _, k := range []string{"bindings", "service_environment", "secret_environment"} {
		if _, ok := outs[k]; !ok {
			t.Errorf("output %s missing", k)
		}
	}
}

func TestBackends(t *testing.T) {
	base := "apiVersion: hermes/v1alpha1\nproject: {name: demo-app, environment: dev}\n"
	cases := []struct {
		name, spec, backend string
		want                map[string]any
	}{
		{"s3", "cloud: {provider: aws, region: eu-north-1}\nstate: {bucket: b-state}\n", "s3",
			map[string]any{"bucket": "b-state", "key": "hermes/demo-app/dev/aws/terraform.tfstate", "region": "eu-north-1", "encrypt": true, "use_lockfile": true}},
		{"s3 dynamodb", "cloud: {provider: aws, region: eu-north-1}\nstate: {bucket: b-state, lock_table: locks, prefix: p1, region: eu-west-1}\n", "s3",
			map[string]any{"bucket": "b-state", "key": "p1/demo-app/dev/aws/terraform.tfstate", "region": "eu-west-1", "encrypt": true, "dynamodb_table": "locks"}},
		{"gcs", "cloud: {provider: gcp, region: europe-north1, account: demo-project-1}\nstate: {bucket: b-state}\n", "gcs",
			map[string]any{"bucket": "b-state", "prefix": "hermes/demo-app/dev/gcp"}},
		{"oss", "cloud: {provider: alibaba, region: eu-central-1}\nstate: {bucket: b-state}\nextensions: {alibaba: {state_tablestore_endpoint: 'https://x.ots.example.com', state_tablestore_table: lock_t}}\n", "oss",
			map[string]any{"bucket": "b-state", "prefix": "hermes/demo-app/dev/alibaba", "key": "terraform.tfstate", "region": "eu-central-1", "encrypt": true,
				"tablestore_endpoint": "https://x.ots.example.com", "tablestore_table": "lock_t"}},
		{"local", "cloud: {provider: aws, region: eu-north-1}\nstate: {backend: local}\n", "local",
			map[string]any{"path": "../../state/hermes/demo-app/dev/aws/terraform.tfstate"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustResolve(t, base+tc.spec, "")
			hermes := t.TempDir()
			res, err := Build(p, Options{OutDir: filepath.Join(hermes, "build", "dev"), ModulesFS: testModules, Version: "t"})
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			_ = json.Unmarshal(res.Files[RootFile], &root)
			be := root["terraform"].(map[string]any)["backend"].(map[string]any)[tc.backend].(map[string]any)
			gotJSON, _ := json.Marshal(be)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("backend = %s\nwant      %s", gotJSON, wantJSON)
			}
		})
	}
}

func mustResolve(t *testing.T, src, env string) *config.Platform {
	t.Helper()
	doc, ds, err := config.Parse([]byte(src), "hermes.yaml")
	if err != nil || ds.HasErrors() {
		t.Fatalf("%v %v", err, ds)
	}
	p, ds, err := doc.Resolve(env)
	if err != nil || ds.HasErrors() {
		t.Fatalf("%v %v", err, ds)
	}
	return p
}

func TestEncryptionBlock(t *testing.T) {
	spec := "apiVersion: hermes/v1alpha1\nproject: {name: demo-app, environment: production}\ncloud: {provider: aws, region: eu-north-1, account: '123456789012'}\n" +
		"state: {bucket: b-state, encryption: {client_side: true, kms_key: 'arn:aws:kms:eu-north-1:123456789012:key/abc'}}\n"
	p := mustResolve(t, spec, "")
	for _, engine := range []string{"tofu", "terraform"} {
		res, err := Build(p, Options{OutDir: filepath.Join(t.TempDir(), "build", "production"), ModulesFS: testModules, Engine: engine})
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		_ = json.Unmarshal(res.Files[RootFile], &root)
		enc, has := root["terraform"].(map[string]any)["encryption"].(map[string]any)
		if engine == "terraform" {
			if has {
				t.Error("terraform roots must not contain the OpenTofu encryption block")
			}
			continue
		}
		if !has {
			t.Fatal("tofu root must contain the encryption block")
		}
		st := enc["state"].(map[string]any)
		if st["method"] != "method.aes_gcm.hermes" || st["enforced"] != true {
			t.Errorf("state encryption: %v (protected env must enforce; bare traversal required)", st)
		}
		if enc["method"].(map[string]any)["aes_gcm"].(map[string]any)["hermes"].(map[string]any)["keys"] != "key_provider.aws_kms.hermes" {
			t.Errorf("keys reference: %v", enc["method"])
		}
	}
}

func TestTemplateEscaping(t *testing.T) {
	spec := "apiVersion: hermes/v1alpha1\nproject: {name: demo-app, environment: dev}\ncloud: {provider: aws, region: eu-north-1}\nstate: {bucket: b-state}\n" +
		"workloads:\n  compute:\n    services:\n      - {name: api, image: 'x/y:1', env: {GREETING: '${file(\"/etc/passwd\")} %{if true}x%{endif}'}}\n"
	p := mustResolve(t, spec, "")
	res, err := Build(p, Options{OutDir: filepath.Join(t.TempDir(), "build", "dev"), ModulesFS: testModules})
	if err != nil {
		t.Fatal(err)
	}
	s := string(res.Files[RootFile])
	if !strings.Contains(s, `$${file(`) || !strings.Contains(s, `%%{if true}`) || strings.Contains(s, `"${file`) {
		t.Errorf("user values must be escaped against template evaluation:\n%s", s)
	}
}

func TestProtectedRejectsLocalBackend(t *testing.T) {
	p := mustResolve(t, "apiVersion: hermes/v1alpha1\nproject: {name: demo-app, environment: dev}\ncloud: {provider: aws, region: eu-north-1}\nstate: {bucket: b-state}\n", "")
	p.Protected = true
	p.State.Backend = config.BackendLocal
	if _, err := Build(p, Options{OutDir: filepath.Join(t.TempDir(), "build", "dev"), ModulesFS: testModules}); err == nil {
		t.Fatal("local backend in a protected environment must be rejected")
	}
	p.State.Backend = config.BackendS3
	p.State.Bucket = ""
	if _, err := Build(p, Options{OutDir: filepath.Join(t.TempDir(), "build", "dev"), ModulesFS: testModules}); err == nil {
		t.Fatal("missing bucket must be rejected")
	}
}

func TestModulesExtraction(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "modules", "v1")
	d1, err := ExtractModules(testModules, dir, "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "providers/aws/platform/.terraform")); !os.IsNotExist(err) {
		t.Error("dot directories must not be extracted")
	}
	if d2, err := ExtractModules(testModules, dir, "v1", false); err != nil || d2 != d1 {
		t.Fatalf("re-extract of identical modules must be a no-op: %v", err)
	}
	// A locally modified tree is never overwritten silently.
	if err := os.WriteFile(filepath.Join(dir, "providers/aws/platform/main.tf"), []byte("# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractModules(testModules, dir, "v1", false); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("expected modified error, got %v", err)
	}
	// A different module set for the same version is refused too.
	other := fstest.MapFS{"providers/aws/platform/main.tf": {Data: []byte("# other\n")}}
	if _, err := ExtractModules(other, dir, "v1", false); err == nil {
		t.Fatal("expected refusal for a different module set")
	}
	if _, err := ExtractModules(other, dir, "v1", true); err != nil {
		t.Fatalf("replace must succeed: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "providers/aws/platform/main.tf")); string(data) != "# other\n" {
		t.Errorf("replace did not install the new modules")
	}
}

func TestBootstrapRender(t *testing.T) {
	p := mustResolve(t, "apiVersion: hermes/v1alpha1\nproject: {name: demo-app, environment: dev}\ncloud: {provider: aws, region: eu-north-1, account: '123456789012'}\nstate: {bucket: b-state}\n", "")
	hermes := t.TempDir()
	res, err := RenderBootstrap(p, Options{OutDir: filepath.Join(hermes, "build", BootstrapEnvironment("aws")), ModulesFS: testModules})
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal(res.Files[RootFile], &root)
	mod := root["module"].(map[string]any)["state_backend"].(map[string]any)
	if mod["bucket_name"] != "b-state" || !strings.HasSuffix(mod["source"].(string), "/bootstrap/aws/state-backend") {
		t.Errorf("bootstrap module: %v", mod)
	}
	if _, ok := root["terraform"].(map[string]any)["backend"].(map[string]any)["local"]; !ok {
		t.Error("bootstrap must use the local backend")
	}
}

// F-03 (QA reproduction): a symlinked *.tf file added to the extracted modules is loaded by the
// engine but ignored by the regular-file digest; verification must treat it as a modification.
// A dot-named symlink, a fifo and a dest that is itself a symlink are refused too.
func TestModulesExtractionRefusesSymlinksAndSpecialFiles(t *testing.T) {
	base := t.TempDir()
	evil := filepath.Join(base, "evil.tf")
	if err := os.WriteFile(evil, []byte("# planted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := func(t *testing.T) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "modules", "v1")
		if _, err := ExtractModules(testModules, dir, "v1", false); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	for name, plant := range map[string]func(dir string) error{
		"symlinked tf file": func(dir string) error {
			return os.Symlink(evil, filepath.Join(dir, "providers/aws/platform/zz_evil.tf"))
		},
		"dot-named symlink": func(dir string) error {
			return os.Symlink(evil, filepath.Join(dir, "providers/aws/platform/.zz_evil.tf"))
		},
		"symlinked directory": func(dir string) error {
			return os.Symlink(base, filepath.Join(dir, "providers/aws/linked"))
		},
		"fifo": func(dir string) error {
			return syscall.Mkfifo(filepath.Join(dir, "providers/aws/platform/pipe"), 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := fresh(t)
			if err := plant(dir); err != nil {
				t.Skipf("cannot create the fixture: %v", err)
			}
			_, err := ExtractModules(testModules, dir, "v1", false)
			if !errors.Is(err, ErrModulesChanged) || !strings.Contains(err.Error(), "symbolic link or special file") {
				t.Fatalf("want a modified-modules refusal, got %v", err)
			}
		})
	}
	t.Run("dest is a symlink", func(t *testing.T) {
		real := fresh(t)
		link := filepath.Join(t.TempDir(), "v1")
		if err := os.Symlink(real, link); err != nil {
			t.Skip(err)
		}
		if _, err := ExtractModules(testModules, link, "v1", false); !errors.Is(err, ErrModulesChanged) || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("want refusal for a symlinked modules directory, got %v", err)
		}
	})
}
