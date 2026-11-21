// Package yamlsafe bounds the YAML documents hermes reads from files (specs, validation checks,
// cutover hooks, catalog/mapping/pricing overrides, policy data). Two rules, both enforced
// before any decoding into Go values:
//
//   - size: at most MaxBytes (1 MiB) by default;
//   - no anchors (&name) or aliases (*name): a 1 KB "billion laughs" document expands to
//     gigabytes when aliases are resolved (F-01). Hermes formats offer other reuse mechanisms
//     (environment overlays in specs), and hermes never writes anchors itself.
//
// Parsing into a yaml.Node does not expand aliases, so the check itself is linear in the input.
package yamlsafe

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// MaxBytes is the default size cap for YAML inputs.
const MaxBytes = 1 << 20

// ErrTooLarge is returned (wrapped) when an input exceeds its size cap.
var ErrTooLarge = errors.New("file too large")

// AliasError reports an anchor or alias.
type AliasError struct {
	Line int
}

func (e *AliasError) Error() string {
	return fmt.Sprintf("line %d: YAML anchors and aliases (&name, *name) are not supported", e.Line)
}

// ReadFile reads at most max bytes of path (max <= 0 means MaxBytes) and fails with
// ErrTooLarge when the file is larger, without reading the rest of it.
func ReadFile(path string, max int) ([]byte, error) {
	if max <= 0 {
		max = MaxBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > max {
		return nil, fmt.Errorf("%s: %w (limit %d bytes)", path, ErrTooLarge, max)
	}
	return data, nil
}

// Check returns an error when data is larger than max (max <= 0 means MaxBytes) or when any of
// its documents contains an anchor or an alias. Syntax errors are left to the decoder.
func Check(data []byte, max int) error {
	if max <= 0 {
		max = MaxBytes
	}
	if len(data) > max {
		return fmt.Errorf("%w: %d bytes (limit %d bytes)", ErrTooLarge, len(data), max)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return nil // a syntax error: reported by the real decoder with its own message
		}
		if err := checkNode(&n); err != nil {
			return err
		}
	}
}

func checkNode(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return &AliasError{Line: n.Line}
	}
	for _, c := range n.Content {
		if err := checkNode(c); err != nil {
			return err
		}
	}
	return nil
}

// DecodeStrict checks data (see Check) and decodes its first document into out, rejecting
// unknown fields.
func DecodeStrict(data []byte, max int, out any) error {
	if err := Check(data, max); err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(out)
}

// Unmarshal checks data (see Check) and decodes it into out (unknown fields allowed).
func Unmarshal(data []byte, max int, out any) error {
	if err := Check(data, max); err != nil {
		return err
	}
	return yaml.Unmarshal(data, out)
}
