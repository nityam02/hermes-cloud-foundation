//go:build !unix

package diagnostics

import "errors"

func freeBytes(string) (uint64, error) {
	return 0, errors.New("free disk space is not measured on this OS")
}
