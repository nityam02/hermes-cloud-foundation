// Package enginetest provides a fake OpenTofu/Terraform engine for tests: it records
// invocations, writes plan files and returns canned `show -json` / `output -json`
// documents. It never executes a binary.
package enginetest

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
)

// Fake is a scripted engine.
type Fake struct {
	mu sync.Mutex
	// PlanJSON is returned by `show -json <planfile>`.
	PlanJSON string
	// StateJSON is returned by `show -json` without a plan file.
	StateJSON string
	// OutputJSON is returned by `output -json`.
	OutputJSON string
	// Drift makes `plan -refresh-only -detailed-exitcode` exit 2.
	Drift bool
	// Fail maps a sub-command (init, plan, show, apply, output) to an error.
	Fail map[string]error
	// Calls records every invocation as "subcommand arg1 arg2 ...".
	Calls []string
	// planCounter makes each plan file unique (like real plans, which embed timestamps).
	planCounter int
}

// Engine returns an *execution.Engine backed by the fake.
func (f *Fake) Engine(name string) *execution.Engine {
	return execution.NewEngine(name, "/fake/bin/"+name, execution.Version{Major: 1, Minor: 13, Patch: 0}, f.run)
}

// Called reports whether a call starting with prefix was made.
func (f *Fake) Called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *Fake) run(_ context.Context, _ string, _ []string, stdout, _ io.Writer, _ string, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, strings.Join(args, " "))
	if len(args) == 0 {
		return fmt.Errorf("fake: no arguments")
	}
	sub := args[0]
	if err := f.Fail[sub]; err != nil {
		return err
	}
	switch sub {
	case "version":
		_, err := io.WriteString(stdout, `{"terraform_version":"1.13.0"}`)
		return err
	case "init", "validate", "apply":
		return nil
	case "plan":
		if contains(args, "-refresh-only") {
			if f.Drift {
				return &execution.ExitStatusError{Code: 2}
			}
			return nil
		}
		for _, a := range args {
			if out, ok := strings.CutPrefix(a, "-out="); ok {
				f.planCounter++
				if err := os.WriteFile(out, fmt.Appendf(nil, "fake-plan-%d %s", f.planCounter, strings.Join(args, " ")), 0o600); err != nil {
					return err
				}
			}
		}
		return &execution.ExitStatusError{Code: 2} // changes present
	case "show":
		doc := f.StateJSON
		if len(args) > 3 && strings.HasSuffix(args[len(args)-1], ".tfplan") {
			doc = f.PlanJSON
		}
		_, err := io.WriteString(stdout, doc)
		return err
	case "output":
		_, err := io.WriteString(stdout, f.OutputJSON)
		return err
	}
	return fmt.Errorf("fake: unsupported sub-command %q", sub)
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// PlanJSON builds a minimal plan document with the given address→actions changes.
func PlanJSON(changes map[string][]string) string {
	var b strings.Builder
	b.WriteString(`{"format_version":"1.2","terraform_version":"1.13.0","resource_changes":[`)
	first := true
	for addr, actions := range changes {
		if !first {
			b.WriteString(",")
		}
		first = false
		quoted := make([]string, len(actions))
		for i, a := range actions {
			quoted[i] = `"` + a + `"`
		}
		fmt.Fprintf(&b, `{"address":%q,"mode":"managed","type":"test_resource","name":"x","change":{"actions":[%s]}}`, addr, strings.Join(quoted, ","))
	}
	b.WriteString(`]}`)
	return b.String()
}
