package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
)

func TestConfirm(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "YES\n": true, "n\n": false, "\n": false, "": false, "sure\n": false} {
		var out bytes.Buffer
		got, err := Confirm(strings.NewReader(in), &out, "Apply?")
		if err != nil || got != want {
			t.Errorf("Confirm(%q) = %v %v", in, got, err)
		}
		if !strings.Contains(out.String(), "[y/N]") {
			t.Errorf("prompt: %q", out.String())
		}
	}
}

func TestTableAndDiagnostics(t *testing.T) {
	var b bytes.Buffer
	Table(&b, [][]string{{"A", "B"}, {"long-value", "x"}})
	if !strings.Contains(b.String(), "A           B") {
		t.Errorf("table: %q", b.String())
	}
	b.Reset()
	Diagnostics(&b, diag.Diagnostics{diag.Warnf("W1", "", "w"), diag.Errorf("E1", "", "e")})
	if !strings.HasPrefix(b.String(), "error E1") || !strings.Contains(b.String(), "1 error(s), 1 warning(s)") {
		t.Errorf("diagnostics: %q", b.String())
	}
	if IsTerminal(&b) {
		t.Error("a buffer is not a terminal")
	}
}
