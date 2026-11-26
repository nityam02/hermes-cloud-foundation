package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The UI has no authentication, so a non-loopback address must be refused before anything is loaded.
func TestUIRefusesNonLoopbackAddress(t *testing.T) {
	var out, errb bytes.Buffer
	app := &App{}
	cmd := newUICmd(app)
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs([]string{"--addr", "0.0.0.0:0"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected a loopback refusal, got %v", err)
	}
}
