package yamlsafe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// billionLaughs is the QA reproduction (F-01): 9 levels of 9 aliases, 9^9 leaves when expanded.
const billionLaughs = `a: &a ["lol","lol","lol","lol","lol","lol","lol","lol","lol"]
b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]
c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]
d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]
e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]
f: &f [*e,*e,*e,*e,*e,*e,*e,*e,*e]
g: &g [*f,*f,*f,*f,*f,*f,*f,*f,*f]
h: &h [*g,*g,*g,*g,*g,*g,*g,*g,*g]
i: &i [*h,*h,*h,*h,*h,*h,*h,*h,*h]
`

func TestRejectsAnchorsAndAliases(t *testing.T) {
	for name, doc := range map[string]string{
		"billion laughs":     billionLaughs,
		"alias":              "a: &x 1\nb: *x\n",
		"anchor only":        "a: &x {k: v}\n",
		"merge key":          "base: &b {k: v}\nother: {<<: *b}\n",
		"in second document": "a: 1\n---\nb: &x 2\nc: *x\n",
	} {
		start := time.Now()
		var out map[string]any
		err := Unmarshal([]byte(doc), 0, &out)
		var ae *AliasError
		if !errors.As(err, &ae) || ae.Line == 0 {
			t.Errorf("%s: want an AliasError with a line, got %v", name, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s: must fail fast, took %v", name, d)
		}
	}
}

func TestAcceptsPlainYAML(t *testing.T) {
	var out struct {
		A string   `yaml:"a"`
		B []string `yaml:"b"`
	}
	if err := DecodeStrict([]byte("a: '*not-an-alias* & text'\nb: [x, y]\n"), 0, &out); err != nil || out.A != "*not-an-alias* & text" || len(out.B) != 2 {
		t.Fatalf("plain YAML must decode: %v %+v", err, out)
	}
	if err := DecodeStrict([]byte("a: x\nunknown: 1\n"), 0, &out); err == nil {
		t.Error("DecodeStrict must reject unknown fields")
	}
	if err := Check([]byte("a: [unclosed\n"), 0); err != nil {
		t.Errorf("syntax errors are left to the decoder: %v", err)
	}
}

func TestSizeCap(t *testing.T) {
	big := []byte("a: " + strings.Repeat("x", MaxBytes) + "\n")
	if err := Check(big, 0); !errors.Is(err, ErrTooLarge) {
		t.Errorf("want ErrTooLarge, got %v", err)
	}
	if err := Check([]byte("a: 1\n"), 3); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an explicit cap applies: %v", err)
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(f, big, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(f, 0); !errors.Is(err, ErrTooLarge) {
		t.Errorf("ReadFile must refuse files over the cap: %v", err)
	}
	small := filepath.Join(dir, "small.yaml")
	_ = os.WriteFile(small, []byte("a: 1\n"), 0o600)
	if data, err := ReadFile(small, 0); err != nil || string(data) != "a: 1\n" {
		t.Errorf("ReadFile: %q %v", data, err)
	}
}
