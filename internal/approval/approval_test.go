package approval

import (
	"errors"
	"testing"
)

func TestCheck(t *testing.T) {
	const id = "3f2a9c1b77d04e5f8a6b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f"
	for approve, want := range map[string]error{
		"3f2a9c1b":     nil,
		"3F2A9C1B77":   nil, // case-insensitive
		"  3f2a9c1b  ": nil, // trimmed
		id:             nil,
		"":             ErrMissing,
		"   ":          ErrMissing,
		"3f2a9c1":      ErrTooShort,
		"3f2a9c1z":     ErrNotHex,
		"deadbeef":     ErrMismatch,
		id + "0":       ErrMismatch,
	} {
		err := Check(id, approve)
		if want == nil && err != nil || want != nil && !errors.Is(err, want) {
			t.Errorf("Check(%q) = %v, want %v", approve, err, want)
		}
	}
}
