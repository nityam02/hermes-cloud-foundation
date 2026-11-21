package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F-01: every YAML input hermes reads (validation checks, cutover hooks, catalog and pricing
// overrides) rejects anchors/aliases and files over 1 MiB before decoding.
func TestYAMLInputsRejectAliasesAndHugeFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		f := filepath.Join(dir, name)
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	alias := "apiVersion: &v hermes/v1alpha1\nkind: X\nother: *v\n"
	huge := "apiVersion: hermes/v1alpha1\n# " + strings.Repeat("x", 1<<20) + "\n"
	loaders := map[string]func(string) error{
		"checks":  func(f string) error { _, err := LoadChecks(f); return err },
		"hooks":   func(f string) error { _, err := LoadHooks(f, nil); return err },
		"catalog": func(f string) error { _, err := LoadCatalog(f); return err },
		"pricing": func(f string) error { _, err := LoadPricing(f); return err },
	}
	for name, load := range loaders {
		if err := load(write(name+"-alias.yaml", alias)); err == nil || !strings.Contains(err.Error(), "anchors and aliases") {
			t.Errorf("%s: anchors/aliases must be refused: %v", name, err)
		}
		if err := load(write(name+"-huge.yaml", huge)); err == nil || !strings.Contains(err.Error(), "too large") {
			t.Errorf("%s: files over 1 MiB must be refused: %v", name, err)
		}
	}
}

// F-11: the output of `command` checks may contain secrets. It is streamed to the terminal and
// never stored: the report keeps exit code, byte count and sha256 only (like cutover hooks).
func TestCommandCheckOutputIsNeverPersisted(t *testing.T) {
	dir := t.TempDir()
	const canary = "CHECK-CANARY-demo-not-a-secret"
	// The secret comes from a file, as a real check would print it; it is not part of argv.
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte(canary+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := writeChecks(t, dir, `  - {id: echo, type: command, argv: ["sh", "-c", "cat secret.txt; cat secret.txt >&2"]}
  - {id: echo-fail, type: command, argv: ["sh", "-c", "cat secret.txt; exit 3"]}
`)
	var term bytes.Buffer
	rep := (&Validator{Plan: &Plan{Metadata: PlanMetadata{PlanID: "abc"}}, Output: &term}).Run(context.Background(), c)
	raw, _ := json.Marshal(rep)
	if strings.Contains(string(raw), canary) || strings.Contains(string(raw), "_tail") {
		t.Fatalf("command output reached the report: %s", raw)
	}
	if strings.Count(term.String(), canary) != 3 {
		t.Errorf("output must be streamed to the terminal: %q", term.String())
	}
	want := map[string]int{"echo": 0, "echo-fail": 3}
	line := canary + "\n"
	for _, r := range rep.Results {
		ev := r.Evidence
		if ev["exit_code"] != want[r.ID] {
			t.Errorf("%s: exit_code %v", r.ID, ev["exit_code"])
		}
		n := map[string]int{"echo": 2, "echo-fail": 1}[r.ID] * len(line)
		sum := sha256.Sum256([]byte(strings.Repeat(line, n/len(line))))
		if ev["output_bytes"] != int64(n) || ev["output_sha256"] != hex.EncodeToString(sum[:]) {
			t.Errorf("%s: evidence %v (want %d bytes)", r.ID, ev, n)
		}
	}
}
