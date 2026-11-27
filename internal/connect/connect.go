// Package connect checks that Hermes can reach a cloud account, and says how to fix it when it cannot.
// Every check is read-only. Results are lists of steps with a status, what was found and a fix, so a
// person who does not know the cloud's IAM model can act on them.
package connect

// Status of one step.
type Status string

const (
	Pass Status = "pass"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip"
)

// Step is one check in a connection test.
type Step struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// Result is a whole connection test. OK is true when no step failed (warnings are allowed).
type Result struct {
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
	Steps   []Step `json:"steps"`
	// Identity is who the check ran as (an ARN, or an account for GCP), when known.
	Identity string `json:"identity,omitempty"`
}

func finish(r *Result, okText, failText string) {
	r.OK = true
	for _, s := range r.Steps {
		if s.Status == Fail {
			r.OK = false
		}
	}
	if r.OK {
		r.Summary = okText
	} else {
		r.Summary = failText
	}
}

// skipRest marks the remaining planned steps as skipped after a failure they depend on.
func skipRest(r *Result, ids [][2]string, because string) {
	for _, p := range ids {
		r.Steps = append(r.Steps, Step{ID: p[0], Title: p[1], Status: Skip, Detail: because})
	}
}
