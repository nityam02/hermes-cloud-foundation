// Package config loads, validates and normalizes the Platform spec (hermes.yaml).
//
// Pipeline (data-contracts §1 "Normalization"): parse YAML (yaml.v3, keeping line numbers)
// → JSON-Schema validation against the embedded schema → deep-merge environments.<env>
// (maps merge, lists replace) → defaults and protected-environment rules → semantic
// validation → normalized *Platform.
package config

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/internal/schema"
	"github.com/nityam02/hermes-cloud-foundation/internal/yamlsafe"
	"github.com/nityam02/hermes-cloud-foundation/schemas"
)

// Diagnostic codes owned by this package (documented in docs/reference/diagnostics.md).
const (
	CodeYAML            = "HCF100"
	CodeSchema          = schema.CodeSchema // HCF101
	CodeAPIVersion      = "HCF102"
	CodeEnvironment     = "HCF103"
	CodeDuplicate       = "HCF104"
	CodeSecretRef       = "HCF105"
	CodeBucketRef       = "HCF106"
	CodeTopicRef        = "HCF107"
	CodeSubscriptionRef = "HCF108"
	CodeDatabaseRef     = "HCF109"
	CodeReservedEnv     = "HCF110"
	CodeNotImplemented  = "HCF111"
	CodeProtected       = "HCF112"
	CodeAccount         = "HCF113"
	CodeImageTag        = "HCF114"
	CodeRange           = "HCF115"
	CodeStateBucket     = "HCF116"
	CodeStateConfig     = "HCF117"
	CodeNetwork         = "HCF118"
	CodeIgnored         = "HCF119"
	CodeDBVersion       = "HCF120"
	CodeProtectedName   = "HCF121"
	CodeUnprotectedProd = "HCF122"
)

// Document is a parsed, schema-validated spec file (before overlays and defaults).
type Document struct {
	// Filename is the path the document was read from (used in diagnostics).
	Filename string
	raw      map[string]any
	lines    map[string]int // dotted path -> 1-based line
}

// Load reads and parses a spec file. The returned error is non-nil only for I/O problems;
// syntax and schema problems are reported as error diagnostics (the Document is nil when
// the file is not a parseable YAML mapping).
func Load(path string) (*Document, diag.Diagnostics, error) {
	data, err := yamlsafe.ReadFile(path, yamlsafe.MaxBytes)
	if errors.Is(err, yamlsafe.ErrTooLarge) {
		d := diag.Errorf(CodeYAML, "", "the spec is larger than %d bytes", yamlsafe.MaxBytes).
			WithHint("split large specs with environment overlays; specs this large are not supported")
		d.File = path
		return nil, diag.Diagnostics{d}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Parse(data, path)
}

// Parse parses spec bytes. See Load for the error contract.
func Parse(data []byte, filename string) (*Document, diag.Diagnostics, error) {
	if len(data) > yamlsafe.MaxBytes {
		d := diag.Errorf(CodeYAML, "", "the spec is larger than %d bytes", yamlsafe.MaxBytes)
		d.File = filename
		return nil, diag.Diagnostics{d}, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		d := diag.Errorf(CodeYAML, "", "invalid YAML: %v", err)
		d.File = filename
		d.Line = yamlErrorLine(err)
		return nil, diag.Diagnostics{d}, nil
	}
	if len(root.Content) == 0 {
		d := diag.Errorf(CodeYAML, "", "the spec is empty")
		d.File = filename
		return nil, diag.Diagnostics{d}, nil
	}
	lines := map[string]int{}
	v, err := toValue(root.Content[0], "", lines)
	if err != nil {
		d := diag.Errorf(CodeYAML, "", "%v", err)
		d.File = filename
		var le *lineError
		if errors.As(err, &le) {
			d.Line = le.line
			d.Path = le.path
		}
		return nil, diag.Diagnostics{d}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		d := diag.Errorf(CodeYAML, "", "the spec must be a YAML mapping (got %s)", typeName(v))
		d.File = filename
		d.Line = root.Content[0].Line
		return nil, diag.Diagnostics{d}, nil
	}
	doc := &Document{Filename: filename, raw: m, lines: lines}

	var ds diag.Diagnostics
	if _, ok := m["apiVersion"]; !ok {
		ds = append(ds, diag.Warnf(CodeAPIVersion, "apiVersion", "apiVersion is missing; assuming %s", APIVersion).
			WithHint("add `apiVersion: %s` as the first line", APIVersion))
	}
	sds, err := schema.Validate(schemas.PlatformV1Alpha1, m)
	if err != nil {
		return nil, nil, fmt.Errorf("config: %w", err)
	}
	for _, d := range sds {
		ds = append(ds, withSchemaHint(d))
	}
	ds = append(ds, doc.checkProtectedNames()...)
	return doc, doc.locate(ds, ""), nil
}

// prodLikeEnvRe matches environment names that conventionally denote production.
var prodLikeEnvRe = regexp.MustCompile(`^(prod|prd|production|live|prod[0-9a-z]*)$`)

// checkProtectedNames guards the protection model against name mismatches (F-02): protection is
// membership of project.protected_environments, so a typo there protects nothing and silently
// leaves the intended environment without its gates.
//   - error: an explicitly listed name is not a declared environment (keys of environments and
//     project.environment). The implicit default [production] is exempt.
//   - warning: a declared environment whose name looks like production is not protected.
func (d *Document) checkProtectedNames() diag.Diagnostics {
	declared := d.Environments()
	var ds diag.Diagnostics
	if list, ok := getPath(d.raw, "project", "protected_environments").([]any); ok {
		for i, x := range list {
			name, isString := x.(string)
			if !isString || slices.Contains(declared, name) {
				continue // non-strings are schema errors
			}
			ds = append(ds, diag.Errorf(CodeProtectedName, fmt.Sprintf("project.protected_environments[%d]", i),
				"protected environment %q is not a declared environment (declared: %v), so it protects nothing", name, declared).
				WithHint("fix the name, or declare the environment under environments: (an empty overlay `%s: {}` is enough)", name))
		}
	}
	protected := d.protectedEnvironments()
	for _, env := range declared {
		if !prodLikeEnvRe.MatchString(env) || slices.Contains(protected, env) {
			continue
		}
		path := "environments." + env
		if _, inOverlays := d.lines[path]; !inOverlays {
			path = "project.environment"
		}
		ds = append(ds, diag.Warnf(CodeUnprotectedProd, path,
			"environment %q looks like production but is not in project.protected_environments: plan-id approval, required policies, remote state and pinned images are not enforced for it", env).
			WithHint("add %q to project.protected_environments (or rename the environment if it is not production)", env))
	}
	return ds
}

// Environments returns the declared environments, sorted: project.environment and the
// keys of environments. Every name in an explicit project.protected_environments must be one
// of them (HCF121, checked in Parse).
func (d *Document) Environments() []string {
	set := map[string]bool{}
	if e, ok := getPath(d.raw, "project", "environment").(string); ok && e != "" {
		set[e] = true
	}
	if envs, ok := d.raw["environments"].(map[string]any); ok {
		for k := range envs {
			set[k] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DefaultEnvironment returns project.environment ("" when unset).
func (d *Document) DefaultEnvironment() string {
	e, _ := getPath(d.raw, "project", "environment").(string)
	return e
}

func (d *Document) protectedEnvironments() []string {
	v, ok := getPath(d.raw, "project", "protected_environments").([]any)
	if !ok {
		if proj, isMap := d.raw["project"].(map[string]any); isMap {
			if _, present := proj["protected_environments"]; present {
				return nil // explicitly empty or null
			}
		}
		return []string{DefaultProtectedEnv}
	}
	var out []string
	for _, x := range v {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Raw returns a deep copy of the document as parsed (no overlays, no defaults).
func (d *Document) Raw() map[string]any { return deepCopy(d.raw).(map[string]any) }

// Line returns the 1-based line of the value at the dotted path, or of its closest
// ancestor below the root; 0 when unknown.
func (d *Document) Line(path string) int {
	for p := path; p != ""; p = parentPath(p) {
		if l, ok := d.lines[p]; ok {
			return l
		}
	}
	return 0 // the root mapping's line carries no information
}

// locate sets File and Line on diagnostics. For a resolved environment, values that come
// from the overlay are located in environments.<env> first.
func (d *Document) locate(ds diag.Diagnostics, env string) diag.Diagnostics {
	for i := range ds {
		if ds[i].Line != 0 {
			continue
		}
		ds[i].File = d.Filename
		if env != "" && ds[i].Path != "" {
			if l, ok := d.lines["environments."+env+"."+ds[i].Path]; ok {
				ds[i].Line = l
				continue
			}
		}
		ds[i].Line = d.Line(ds[i].Path)
	}
	return ds
}

func parentPath(p string) string {
	if strings.HasSuffix(p, "]") {
		if i := strings.LastIndex(p, "["); i >= 0 {
			return p[:i]
		}
	}
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[:i]
	}
	return ""
}

type lineError struct {
	line int
	path string
	msg  string
}

func (e *lineError) Error() string { return e.msg }

// toValue converts a yaml.Node into JSON-compatible Go values and records line numbers.
func toValue(n *yaml.Node, path string, lines map[string]int) (any, error) {
	lines[path] = n.Line
	// Anchors and aliases are rejected before anything is expanded (F-01: a 1 KB nested-alias
	// document expands to gigabytes). Environment overlays are the supported reuse mechanism.
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return nil, &lineError{n.Line, path, fmt.Sprintf("line %d: YAML anchors and aliases (&name, *name) are not supported; use environment overlays", n.Line)}
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return toValue(n.Content[0], path, lines)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, vn := n.Content[i], n.Content[i+1]
			if k.Kind != yaml.ScalarNode {
				return nil, &lineError{k.Line, path, fmt.Sprintf("line %d: mapping keys must be scalars", k.Line)}
			}
			if k.Tag == "!!merge" || k.Value == "<<" {
				return nil, &lineError{k.Line, path, fmt.Sprintf("line %d: YAML merge keys (<<) are not supported; use environment overlays", k.Line)}
			}
			if _, dup := m[k.Value]; dup {
				return nil, &lineError{k.Line, join(path, k.Value), fmt.Sprintf("line %d: duplicate key %q", k.Line, k.Value)}
			}
			cp := join(path, k.Value)
			v, err := toValue(vn, cp, lines)
			if err != nil {
				return nil, err
			}
			lines[cp] = k.Line
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for i, c := range n.Content {
			v, err := toValue(c, fmt.Sprintf("%s[%d]", path, i), lines)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, &lineError{n.Line, path, fmt.Sprintf("line %d: %v", n.Line, err)}
			}
			return b, nil
		case "!!int":
			var i int64
			if err := n.Decode(&i); err != nil {
				return nil, &lineError{n.Line, path, fmt.Sprintf("line %d: %v", n.Line, err)}
			}
			return int(i), nil
		case "!!float":
			var f float64
			if err := n.Decode(&f); err != nil {
				return nil, &lineError{n.Line, path, fmt.Sprintf("line %d: %v", n.Line, err)}
			}
			if math.IsInf(f, 0) || math.IsNaN(f) {
				return nil, &lineError{n.Line, path, fmt.Sprintf("line %d: non-finite numbers are not allowed", n.Line)}
			}
			if f == math.Trunc(f) && math.Abs(f) < 1<<53 {
				return int(f), nil
			}
			return f, nil
		default:
			return n.Value, nil
		}
	}
	return nil, &lineError{n.Line, path, fmt.Sprintf("line %d: unsupported YAML node", n.Line)}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func yamlErrorLine(err error) int {
	// yaml.v3 messages look like "yaml: line 7: mapping values are not allowed in this context".
	s := err.Error()
	if i := strings.Index(s, "line "); i >= 0 {
		rest := s[i+5:]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if n, e := strconv.Atoi(rest[:j]); e == nil {
			return n
		}
	}
	return 0
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case []any:
		return "a list"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case int, float64:
		return "a number"
	}
	return fmt.Sprintf("%T", v)
}

func withSchemaHint(d diag.Diagnostic) diag.Diagnostic {
	switch {
	case strings.HasPrefix(d.Path, "environments.") && strings.Contains(d.Message, "additional properties") &&
		strings.Count(d.Path, ".") == 3 && strings.Contains(d.Path, ".project."):
		return d.WithHint("overlays may only change project.owner and project.labels")
	case strings.Contains(d.Message, "additional properties"):
		return d.WithHint("check the field name against docs/reference/configuration.md (unknown fields are errors)")
	case strings.HasSuffix(d.Path, "database.version"):
		return d.WithHint(`quote the major version, e.g. version: "16"`)
	}
	return d
}

func getPath(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = deepCopy(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = deepCopy(x)
		}
		return out
	}
	return v
}
