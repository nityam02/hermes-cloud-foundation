package diag

import (
	"strings"
	"testing"
)

func TestDiagnostics(t *testing.T) {
	ds := Diagnostics{
		Warnf("HCF102", "apiVersion", "missing"),
		Errorf("HCF101", "b.c", "bad %d", 1).WithHint("fix it"),
		Infof("HCF210", "", "note"),
		Errorf("HCF101", "a", "worse"),
	}
	if !ds.HasErrors() || ds.Count(SeverityError) != 2 || ds.Count(SeverityWarning) != 1 {
		t.Fatalf("counts wrong: %v", ds)
	}
	sorted := ds.Sorted()
	if sorted[0].Path != "a" || sorted[1].Path != "b.c" || sorted[2].Severity != SeverityWarning || sorted[3].Severity != SeverityInfo {
		t.Errorf("Sorted: %v", sorted)
	}
	if err := ds.Err(); err == nil || !strings.Contains(err.Error(), "2 error(s)") {
		t.Errorf("Err: %v", err)
	}
	if (Diagnostics{Warnf("X", "", "w")}).Err() != nil {
		t.Error("warnings only must not produce an error")
	}
	d := Errorf("HCF101", "x.y", "m").WithHint("h")
	if got := d.String(); got != "error HCF101 at x.y: m (hint: h)" {
		t.Errorf("String() = %q", got)
	}
	d.File, d.Line = "hermes.yaml", 7
	if got := d.String(); got != "error HCF101 at x.y (hermes.yaml:7): m (hint: h)" {
		t.Errorf("String() with line = %q", got)
	}
}
