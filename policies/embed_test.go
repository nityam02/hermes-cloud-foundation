package policies

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedPolicies(t *testing.T) {
	entries, err := fs.ReadDir(FS, Dir)
	if err != nil {
		t.Fatalf("read embedded dir: %v", err)
	}
	if len(entries) < 9 {
		t.Fatalf("expected at least 9 policy files, got %d", len(entries))
	}
	ruleID := regexp.MustCompile(`HCF-POL-\d{3}`)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".rego") || strings.HasSuffix(name, "_test.rego") {
			t.Errorf("unexpected embedded file %q: only non-test .rego files may be shipped", name)
			continue
		}
		b, err := fs.ReadFile(FS, Dir+"/"+name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(b)
		if !strings.Contains(src, "\npackage main\n") {
			t.Errorf("%s: must declare package main (conftest's default namespace)", name)
		}
		if !ruleID.MatchString(src) {
			t.Errorf("%s: no HCF-POL-nnn rule id found", name)
		}
	}
}
