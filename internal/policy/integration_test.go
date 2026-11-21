//go:build integration

// Opt-in: runs the real conftest binary through Check, with the embedded policy bundle,
// against the security workstream's synthetic plan fixtures.
//
//	go test -tags integration ./internal/policy -run TestIntegration -v
package policy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIntegrationConftest(t *testing.T) {
	if _, err := exec.LookPath("conftest"); err != nil {
		t.Skip("conftest not on PATH")
	}
	fixtures := filepath.Join("..", "..", "policies", "tests", "fixtures")
	for _, tc := range []struct {
		fixture string
		pass    bool
	}{{"aws_compliant", true}, {"gcp_compliant", true}, {"alibaba_compliant", true},
		{"aws_violations", false}, {"gcp_violations", false}, {"alibaba_violations", false}} {
		t.Run(tc.fixture, func(t *testing.T) {
			dir := t.TempDir()
			data, err := os.ReadFile(filepath.Join(fixtures, tc.fixture, "plan.json"))
			if err != nil {
				t.Skipf("fixture missing: %v", err)
			}
			plan := filepath.Join(dir, "plan.json")
			if err := os.WriteFile(plan, data, 0o600); err != nil {
				t.Fatal(err)
			}
			rec, ds, err := Check(context.Background(), plan, Options{
				HermesDir: dir, Version: "it", Protected: true,
				Data: map[string]any{"hermes": map[string]any{"protected_environments": []string{"production"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !rec.Ran || rec.Passed != tc.pass {
				t.Fatalf("ran=%v passed=%v (want %v) failures=%v diags=%v", rec.Ran, rec.Passed, tc.pass, rec.Failures, ds)
			}
			t.Logf("failures=%d warnings=%d", len(rec.Failures), len(rec.Warnings))
		})
	}
}
