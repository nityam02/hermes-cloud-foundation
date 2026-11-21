package infra

import (
	"io/fs"
	"strings"
	"testing"
)

func TestEmbedExcludesDotFiles(t *testing.T) {
	n := 0
	err := fs.WalkDir(FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, seg := range strings.Split(path, "/") {
			if seg != "." && (strings.HasPrefix(seg, ".") || strings.HasPrefix(seg, "_")) {
				t.Errorf("embedded path %q contains a dot/underscore segment", path)
			}
		}
		if strings.HasSuffix(path, ".tfstate") || strings.Contains(path, ".tfstate.") {
			t.Errorf("state file embedded: %s", path)
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no files embedded")
	}
	if _, err := fs.Stat(FS, "contract/variables.tf"); err != nil {
		t.Errorf("contract/variables.tf must be embedded: %v", err)
	}
}
