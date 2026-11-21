// Package diag defines the diagnostics model shared by validation, provider adapters,
// execution safety gates and the migration engine.
//
// Diagnostic codes are stable and documented in docs/reference/diagnostics.md:
//
//	HCF1xx  configuration and schema
//	HCF2xx  provider adapter / capability
//	HCF3xx  execution engine
//	HCF4xx  migration and discovery
//	HCF5xx  safety gates
package diag

import (
	"fmt"
	"sort"
	"strings"
)

// Severity of a diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Diagnostic is a single finding with a stable code and a spec path.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Path     string   `json:"path,omitempty"` // dotted spec path, e.g. workloads.database.engine
	File     string   `json:"file,omitempty"` // source file, when known
	Line     int      `json:"line,omitempty"` // 1-based line in File, when known
	Message  string   `json:"message"`
	Hint     string   `json:"hint,omitempty"`
}

func (d Diagnostic) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", d.Severity, d.Code)
	if d.Path != "" {
		fmt.Fprintf(&b, " at %s", d.Path)
	}
	if d.Line > 0 {
		if d.File != "" {
			fmt.Fprintf(&b, " (%s:%d)", d.File, d.Line)
		} else {
			fmt.Fprintf(&b, " (line %d)", d.Line)
		}
	}
	fmt.Fprintf(&b, ": %s", d.Message)
	if d.Hint != "" {
		fmt.Fprintf(&b, " (hint: %s)", d.Hint)
	}
	return b.String()
}

// Errorf builds an error diagnostic.
func Errorf(code, path, format string, args ...any) Diagnostic {
	return Diagnostic{Severity: SeverityError, Code: code, Path: path, Message: fmt.Sprintf(format, args...)}
}

// Warnf builds a warning diagnostic.
func Warnf(code, path, format string, args ...any) Diagnostic {
	return Diagnostic{Severity: SeverityWarning, Code: code, Path: path, Message: fmt.Sprintf(format, args...)}
}

// Infof builds an informational diagnostic.
func Infof(code, path, format string, args ...any) Diagnostic {
	return Diagnostic{Severity: SeverityInfo, Code: code, Path: path, Message: fmt.Sprintf(format, args...)}
}

// WithHint returns a copy of d with a remediation hint.
func (d Diagnostic) WithHint(format string, args ...any) Diagnostic {
	d.Hint = fmt.Sprintf(format, args...)
	return d
}

// Diagnostics is an ordered collection of findings.
type Diagnostics []Diagnostic

// HasErrors reports whether any diagnostic is an error.
func (ds Diagnostics) HasErrors() bool {
	for _, d := range ds {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Count returns the number of diagnostics with the given severity.
func (ds Diagnostics) Count(s Severity) int {
	n := 0
	for _, d := range ds {
		if d.Severity == s {
			n++
		}
	}
	return n
}

// Sorted returns diagnostics ordered by severity (errors first), then path, then code.
func (ds Diagnostics) Sorted() Diagnostics {
	rank := map[Severity]int{SeverityError: 0, SeverityWarning: 1, SeverityInfo: 2}
	out := make(Diagnostics, len(ds))
	copy(out, ds)
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// Err converts error diagnostics into a single error, or nil if there are none.
func (ds Diagnostics) Err() error {
	var msgs []string
	for _, d := range ds {
		if d.Severity == SeverityError {
			msgs = append(msgs, d.String())
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("%d error(s):\n  %s", len(msgs), strings.Join(msgs, "\n  "))
}
