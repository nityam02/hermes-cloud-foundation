// Package approval is the one rule every Hermes approval follows (F-20): `hermes apply` and
// `hermes destroy --approve`, `hermes migrate cutover --approve` and `--approve-hooks`, and
// `hermes adopt apply --approve` all take at least MinPrefix characters of the id being
// approved (a sha256 in hex), compared case-insensitively after trimming surrounding spaces.
package approval

import (
	"errors"
	"strings"
)

// MinPrefix is the minimum number of id characters an approval must give.
const MinPrefix = 8

// Errors returned by Check.
var (
	ErrMissing  = errors.New("approval missing")
	ErrTooShort = errors.New("approval prefix too short")
	ErrNotHex   = errors.New("approval prefix is not hexadecimal")
	ErrMismatch = errors.New("approval prefix does not match")
)

// Normalize trims spaces and lower-cases an approval value.
func Normalize(approve string) string { return strings.ToLower(strings.TrimSpace(approve)) }

// Check returns nil when approve is a valid approval of id: at least MinPrefix hexadecimal
// characters (any case) that are a prefix of id.
func Check(id, approve string) error {
	a := Normalize(approve)
	switch {
	case a == "":
		return ErrMissing
	case len(a) < MinPrefix:
		return ErrTooShort
	case !isHex(a):
		return ErrNotHex
	case !strings.HasPrefix(strings.ToLower(id), a):
		return ErrMismatch
	}
	return nil
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
