// Package ui holds small output helpers shared by CLI commands: JSON encoding, aligned
// tables, diagnostics rendering, terminal detection and y/N confirmation.
package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/nityam02/hermes-cloud-foundation/internal/diag"
)

// JSON writes v as indented JSON followed by a newline.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Table writes rows as aligned columns; the first row is the header.
func Table(w io.Writer, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	_ = tw.Flush()
}

// Diagnostics writes diagnostics (errors first) and a one-line summary.
func Diagnostics(w io.Writer, ds diag.Diagnostics) {
	for _, d := range ds.Sorted() {
		fmt.Fprintln(w, d.String())
	}
	if len(ds) > 0 {
		fmt.Fprintf(w, "%d error(s), %d warning(s)\n", ds.Count(diag.SeverityError), ds.Count(diag.SeverityWarning))
	}
}

// IsTerminal reports whether f is a character device (an interactive terminal).
func IsTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// Confirm asks a y/N question; anything but y/yes is "no".
func Confirm(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprintf(out, "%s [y/N]: ", prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
