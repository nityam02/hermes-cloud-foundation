package execution_test

import (
	"bytes"
	"context"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
)

// F-05: TF_CLI_ARGS and TF_CLI_ARGS_<command> never reach the engine (they would inject
// arguments Hermes neither sees nor records); a warning names them once.
func TestEngineEnvironmentStripsTFCLIArgs(t *testing.T) {
	t.Setenv("TF_CLI_ARGS", "-no-color")
	t.Setenv("TF_CLI_ARGS_plan", "-target=module.platform.aws_db_instance.this")
	t.Setenv("TF_CLI_ARGS_init", "-backend-config=bucket=elsewhere")
	t.Setenv("TF_CLI_ARGS_apply", "-auto-approve")
	t.Setenv("TF_LOG", "WARN") // other TF_* variables pass through
	var envs [][]string
	run := func(_ context.Context, _ string, env []string, _, _ io.Writer, _ string, _ ...string) error {
		envs = append(envs, env)
		return nil
	}
	e := execution.NewEngine("tofu", "/fake/bin/tofu", execution.Version{Major: 1, Minor: 13}, run)
	var stderr bytes.Buffer
	e.Stderr = &stderr
	for i := 0; i < 2; i++ {
		if err := e.Init(context.Background(), t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	for _, env := range envs {
		joined := strings.Join(env, "\n")
		if strings.Contains(joined, "TF_CLI_ARGS") {
			t.Fatalf("TF_CLI_ARGS* reached the engine:\n%s", joined)
		}
		if !strings.Contains(joined, "TF_LOG=WARN") || !strings.Contains(joined, "TF_IN_AUTOMATION=1") || !strings.Contains(joined, "TF_INPUT=0") {
			t.Errorf("expected pass-through and automation variables:\n%s", joined)
		}
	}
	if got := strings.Count(stderr.String(), "warning: ignoring"); got != 1 {
		t.Errorf("want exactly one warning, got %d: %q", got, stderr.String())
	}
	list, _, _ := strings.Cut(strings.TrimPrefix(stderr.String(), "warning: ignoring "), ":")
	names := strings.Split(list, ", ")
	sort.Strings(names)
	if strings.Join(names, ",") != "TF_CLI_ARGS,TF_CLI_ARGS_apply,TF_CLI_ARGS_init,TF_CLI_ARGS_plan" {
		t.Errorf("the warning must name every stripped variable: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "elsewhere") || strings.Contains(stderr.String(), "-target") {
		t.Errorf("the warning must not echo the values: %q", stderr.String())
	}
}
