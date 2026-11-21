// Package schema validates documents against the embedded JSON Schemas (draft 2020-12)
// using santhosh-tekuri/jsonschema/v6, and converts violations into diagnostics with
// dotted spec paths.
package schema

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
	"github.com/nityam02/hermes-cloud-foundation/schemas"
)

// CodeSchema is the diagnostic code for JSON Schema violations.
const CodeSchema = "HCF101"

var (
	cacheMu sync.Mutex
	cache   = map[string]*jsonschema.Schema{}
	printer = message.NewPrinter(language.English)
)

// Compile returns the compiled schema at path inside the embedded schemas FS
// (e.g. schemas.PlatformV1Alpha1). Compiled schemas are cached.
func Compile(path string) (*jsonschema.Schema, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if s, ok := cache[path]; ok {
		return s, nil
	}
	data, err := schemas.FS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("schema: read embedded %s: %w", path, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("schema: parse %s: %w", path, err)
	}
	url := "mem:///" + path
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("schema: add %s: %w", path, err)
	}
	s, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("schema: compile %s: %w", path, err)
	}
	cache[path] = s
	return s, nil
}

// Validate validates instance (JSON-compatible values: map[string]any, []any, string,
// int/float64, bool, nil) against the embedded schema at path. It returns one error
// diagnostic per leaf violation, with Path set to the dotted instance path.
func Validate(path string, instance any) (diag.Diagnostics, error) {
	s, err := Compile(path)
	if err != nil {
		return nil, err
	}
	err = s.Validate(instance)
	if err == nil {
		return nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil, fmt.Errorf("schema: validate: %w", err)
	}
	var ds diag.Diagnostics
	seen := map[string]bool{}
	for _, leaf := range leaves(ve) {
		loc := leaf.InstanceLocation
		if ap, ok := leaf.ErrorKind.(*kind.AdditionalProperties); ok && len(ap.Properties) > 0 {
			// Point at the (first) unknown key itself, so the diagnostic carries its line.
			loc = append(append([]string{}, loc...), ap.Properties[0])
		}
		p := DottedPath(instance, loc)
		msg := leaf.ErrorKind.LocalizedString(printer)
		key := p + "\x00" + msg
		if seen[key] {
			continue
		}
		seen[key] = true
		ds = append(ds, diag.Errorf(CodeSchema, p, "%s", msg))
	}
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].Path < ds[j].Path })
	return ds, nil
}

// leaves returns the most specific violations. For anyOf/oneOf groups the library nests
// alternatives as causes; reporting the leaves gives the user every concrete reason.
func leaves(ve *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(ve.Causes) == 0 {
		return []*jsonschema.ValidationError{ve}
	}
	var out []*jsonschema.ValidationError
	for _, c := range ve.Causes {
		out = append(out, leaves(c)...)
	}
	return out
}

// DottedPath converts a JSON pointer token list into the dotted path used in diagnostics,
// e.g. ["workloads","compute","services","0","cpu"] -> "workloads.compute.services[0].cpu".
// The instance is walked so that array indexes are rendered as [i].
func DottedPath(instance any, tokens []string) string {
	var b strings.Builder
	cur := instance
	for _, t := range tokens {
		switch v := cur.(type) {
		case []any:
			b.WriteString("[" + t + "]")
			if i, err := strconv.Atoi(t); err == nil && i >= 0 && i < len(v) {
				cur = v[i]
			} else {
				cur = nil
			}
		case map[string]any:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(t)
			cur = v[t]
		default:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(t)
			cur = nil
		}
	}
	return b.String()
}
