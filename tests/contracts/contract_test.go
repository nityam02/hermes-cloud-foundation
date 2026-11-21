// Package contracts verifies that every provider composition implements the Hermes
// provider contract identically. These checks are static (no engine, no network);
// `make tf-test` runs the shared contract runs with mock providers.
package contracts

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Providers that must implement the contract.
var providers = []string{"aws", "gcp", "alibaba"}

// Outputs every composition must declare (infra/contract/outputs.md).
var wantOutputs = []string{"bindings", "secret_environment", "service_environment"}

const runsMarker = "# --- BEGIN HERMES CONTRACT RUNS"

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// normalize removes trailing whitespace per line and normalizes line endings so that
// formatting-only differences (editor newline at EOF) do not fail the contract.
func normalize(b []byte) []byte {
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n")
}

func fromMarker(t *testing.T, b []byte, path string) []byte {
	t.Helper()
	i := bytes.Index(b, []byte(runsMarker))
	if i < 0 {
		t.Fatalf("%s: canonical contract marker %q not found", path, runsMarker)
	}
	return normalize(b[i:])
}

func TestVariablesAreIdenticalToContract(t *testing.T) {
	root := repoRoot(t)
	want := normalize(read(t, filepath.Join(root, "infra", "contract", "variables.tf")))
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			path := filepath.Join(root, "infra", "providers", p, "platform", "variables.tf")
			got := normalize(read(t, path))
			if !bytes.Equal(got, want) {
				t.Errorf("%s differs from infra/contract/variables.tf; copy the canonical file verbatim", path)
			}
		})
	}
}

func TestContractRunsAreIdentical(t *testing.T) {
	root := repoRoot(t)
	canonicalPath := filepath.Join(root, "tests", "contracts", "contract-runs.hcl")
	want := fromMarker(t, read(t, canonicalPath), canonicalPath)
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			path := filepath.Join(root, "infra", "providers", p, "platform", "tests", "contract.tftest.hcl")
			got := fromMarker(t, read(t, path), path)
			if !bytes.Equal(got, want) {
				t.Errorf("%s: the section after %q must be a verbatim copy of tests/contracts/contract-runs.hcl", path, runsMarker)
			}
		})
	}
}

var outputRe = regexp.MustCompile(`(?m)^output\s+"([A-Za-z0-9_-]+)"`)

func TestOutputsMatchContract(t *testing.T) {
	root := repoRoot(t)
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			dir := filepath.Join(root, "infra", "providers", p, "platform")
			files, err := filepath.Glob(filepath.Join(dir, "*.tf"))
			if err != nil || len(files) == 0 {
				t.Fatalf("no .tf files in %s", dir)
			}
			seen := map[string]bool{}
			for _, f := range files {
				for _, m := range outputRe.FindAllSubmatch(read(t, f), -1) {
					seen[string(m[1])] = true
				}
			}
			var got []string
			for k := range seen {
				got = append(got, k)
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(wantOutputs, ",") {
				t.Errorf("outputs = %v, want exactly %v (infra/contract/outputs.md)", got, wantOutputs)
			}
		})
	}
}

// Outputs must never be marked sensitive: the contract forbids secret values in outputs,
// so needing `sensitive = true` indicates a contract violation.
func TestNoSensitiveOutputs(t *testing.T) {
	root := repoRoot(t)
	sensitiveRe := regexp.MustCompile(`(?s)output\s+"[^"]+"\s*\{[^}]*sensitive\s*=\s*true`)
	for _, p := range providers {
		dir := filepath.Join(root, "infra", "providers", p, "platform")
		files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
		for _, f := range files {
			if sensitiveRe.Match(read(t, f)) {
				t.Errorf("%s declares a sensitive output; outputs must not carry secrets", f)
			}
		}
	}
}
