package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

// The JSON Schemas are validated here against fixtures, generated documents and the REAL
// maintained data files (migration/mappings, migration/workflows, migration/pricing).

const schemaBase = "https://hermes.example.com/schemas/"

func compileSchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	files := map[string]string{
		"inventory/v1alpha1.schema.json":   "inventory",
		"migration/catalog.schema.json":    "catalog",
		"migration/workflow.schema.json":   "workflow",
		"migration/pricing.schema.json":    "pricing",
		"migration/assessment.schema.json": "assessment",
		"migration/plan.schema.json":       "plan",
		"migration/validation.schema.json": "validation",
	}
	for f := range files {
		raw, err := os.ReadFile(filepath.Join(repoRoot(), "schemas", f))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if err := c.AddResource(schemaBase+f, doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	out := map[string]*jsonschema.Schema{}
	for f, name := range files {
		s, err := c.Compile(schemaBase + f)
		if err != nil {
			t.Fatalf("compile %s: %v", f, err)
		}
		out[name] = s
	}
	return out
}

func asJSONValue(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func yamlFile(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := yaml.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return asJSONValue(t, v)
}

func mustValid(t *testing.T, s *jsonschema.Schema, name string, doc any) {
	t.Helper()
	if err := s.Validate(doc); err != nil {
		t.Errorf("%s does not validate: %v", name, err)
	}
}

func TestSchemasRealDataFiles(t *testing.T) {
	s := compileSchemas(t)
	root := repoRoot()
	mustValid(t, s["catalog"], "migration/mappings/catalog.yaml", yamlFile(t, filepath.Join(root, "migration", "mappings", "catalog.yaml")))
	mustValid(t, s["pricing"], "migration/pricing/assumptions.yaml", yamlFile(t, filepath.Join(root, "migration", "pricing", "assumptions.yaml")))
	wfs, _ := filepath.Glob(filepath.Join(root, "migration", "workflows", "*.yaml"))
	if len(wfs) < 13 {
		t.Fatalf("expected 13 workflows, found %d", len(wfs))
	}
	for _, f := range wfs {
		mustValid(t, s["workflow"], f, yamlFile(t, f))
	}
}

func TestSchemasGeneratedDocuments(t *testing.T) {
	s := compileSchemas(t)
	inv := sampleInventory(t)
	mustValid(t, s["inventory"], "sample-inventory.json", asJSONValue(t, inv))
	imported, err := inventory.ImportAWSConfig([]byte(`[{"resourceId":"q","resourceType":"AWS::SQS::Queue","awsRegion":"eu-north-1","arn":"arn:aws:sqs:eu-north-1:123456789012:q"}]`))
	if err != nil {
		t.Fatal(err)
	}
	mustValid(t, s["inventory"], "aws-config import", asJSONValue(t, imported))
	for _, target := range Targets {
		_, a := assessSample(t, target)
		mustValid(t, s["assessment"], "assessment "+target, asJSONValue(t, a))
	}
	a, p := samplePlan(t)
	mustValid(t, s["plan"], "plan", asJSONValue(t, p))
	dir := t.TempDir()
	if _, err := Generate(a, filepath.Join(dir, "target"), "", ""); err != nil {
		t.Fatal(err)
	}
	checks := writeChecks(t, dir, "  - {id: c, type: spec-coverage, spec: target/hermes.yaml}\n")
	rep := (&Validator{Plan: p}).Run(context.Background(), checks)
	mustValid(t, s["validation"], "report", asJSONValue(t, rep))
	mustValid(t, s["validation"], "examples/migration/checks.yaml", yamlFile(t, filepath.Join(repoRoot(), "examples", "migration", "checks.yaml")))
}

func TestSchemasRejectUnsafeDocuments(t *testing.T) {
	s := compileSchemas(t)
	bad := map[string]struct {
		schema string
		doc    string
	}{
		"inventory not read-only":                {"inventory", `{"apiVersion":"hermes/v1alpha1","kind":"Inventory","metadata":{"provider":"aws","collector":"aws-api","account_id":"1","regions":[],"read_only":false},"resources":[],"relationships":[],"gaps":[],"summary":{"resource_count":0,"by_type":{},"gap_count":0}}`},
		"source-modifying step without approval": {"workflow", `{"apiVersion":"hermes/v1alpha1","kind":"MigrationWorkflow","metadata":{"id":"x","title":"x","applies_to":["other"]},"spec":{"steps":[{"id":"drain","phase":"synchronize","title":"t","description":"d","automation":"manual","gate":"none","reversible":true,"rollback":"r","risks":[],"validation":[],"source_impact":"modifies-source-data"}]}}`},
		"automated decommission":                 {"workflow", `{"apiVersion":"hermes/v1alpha1","kind":"MigrationWorkflow","metadata":{"id":"x","title":"x","applies_to":["other"]},"spec":{"steps":[{"id":"rm","phase":"decommission","title":"t","description":"d","automation":"hermes","gate":"approval","reversible":false,"rollback":"r","risks":[],"validation":[],"source_impact":"modifies-source-config"}]}}`},
		"plain http check":                       {"validation", `{"apiVersion":"hermes/v1alpha1","kind":"ValidationChecks","metadata":{"name":"x"},"checks":[{"id":"h","type":"http","url":"http://example.com/"}]}`},
		"shell string command":                   {"validation", `{"apiVersion":"hermes/v1alpha1","kind":"ValidationChecks","metadata":{"name":"x"},"checks":[{"id":"c","type":"command","argv":"rm -rf /"}]}`},
	}
	for name, tc := range bad {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.doc))
		if err != nil {
			t.Fatal(err)
		}
		if s[tc.schema].Validate(doc) == nil {
			t.Errorf("%s: schema accepted an unsafe document", name)
		}
	}
}
