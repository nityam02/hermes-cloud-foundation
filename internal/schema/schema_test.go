package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/nityam02/hermes-cloud-foundation/schemas"
)

func load(t *testing.T, src string) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	return normalize(v)
}

// normalize converts yaml.v3 generic output (map[string]interface{} already) and ints.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = normalize(x)
		}
	case []any:
		for i, x := range t {
			t[i] = normalize(x)
		}
	}
	return v
}

func TestPlatformSchemaFixtures(t *testing.T) {
	valid := map[string]string{
		"minimal": "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\n",
		"overlay": "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nenvironments:\n  gcpdev: {cloud: {provider: gcp, region: europe-north1}}\n",
		"service": "project: {name: abc}\ncloud: {provider: gcp, region: europe-north1}\nworkloads: {compute: {services: [{name: api, image: 'x/y:1', cpu: 1, memory_mb: 2048}]}}\n",
		"no dlq":  "project: {name: abc}\ncloud: {provider: gcp, region: europe-north1}\nworkloads: {messaging: {topics: [{name: t1, subscriptions: [{name: s1, max_delivery_attempts: 0}]}]}}\n",
		"nulls":   "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nsecurity: {customer_managed_keys: null}\nworkloads: {database: {deletion_protection: null}}\n",
	}
	invalid := map[string]string{
		"missing cloud":     "project: {name: abc}\n",
		"bad project name":  "project: {name: Bad_Name}\ncloud: {provider: aws, region: eu-north-1}\n",
		"bad cpu":           "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nworkloads: {compute: {services: [{name: api, image: x, cpu: 3}]}}\n",
		"missing image":     "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nworkloads: {compute: {services: [{name: api}]}}\n",
		"bad access mode":   "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nworkloads: {compute: {services: [{name: api, image: x, access: {buckets: {b1: write}}}]}}\n",
		"bad env key":       "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nworkloads: {compute: {services: [{name: api, image: x, env: {lower: x}}]}}\n",
		"overlay name":      "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nenvironments: {dev: {project: {name: other}}}\n",
		"bad env name":      "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nenvironments: {Dev: {}}\n",
		"unknown extension": "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nextensions: {azure: {}}\n",
		"zones 4":           "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nnetwork: {zones: 4}\n",
		"bad apiVersion":    "apiVersion: hermes/v1\nproject: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\n",
	}
	for name, src := range valid {
		ds, err := Validate(schemas.PlatformV1Alpha1, load(t, src))
		if err != nil || len(ds) != 0 {
			t.Errorf("%s: expected valid, got %v %v", name, err, ds)
		}
	}
	for name, src := range invalid {
		ds, err := Validate(schemas.PlatformV1Alpha1, load(t, src))
		if err != nil || len(ds) == 0 {
			t.Errorf("%s: expected invalid, got %v", name, err)
		}
	}
}

func TestMinimalExampleIsSchemaValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "basic", "hermes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ds, err := Validate(schemas.PlatformV1Alpha1, load(t, string(data)))
	if err != nil || len(ds) != 0 {
		t.Fatalf("minimal example must be schema-valid: %v %v", err, ds)
	}
}

func TestDottedPath(t *testing.T) {
	inst := load(t, "a: {b: [{c: 1}]}")
	if got := DottedPath(inst, []string{"a", "b", "0", "c"}); got != "a.b[0].c" {
		t.Errorf("got %s", got)
	}
	ds, _ := Validate(schemas.PlatformV1Alpha1, load(t, "project: {name: abc}\ncloud: {provider: aws, region: eu-north-1}\nworkloads: {compute: {services: [{name: api, image: x, cpu: 3}]}}\n"))
	if len(ds) == 0 || ds[0].Path != "workloads.compute.services[0].cpu" || !strings.Contains(ds[0].Message, "0.25") {
		t.Errorf("unexpected diagnostics %v", ds)
	}
}
