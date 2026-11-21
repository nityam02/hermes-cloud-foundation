//go:build unix

package diagnostics

import (
	"math"
	"math/bits"
	"syscall"
)

func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	// The field types differ per OS (Bsize is int64 on Linux and uint32 on macOS), so convert
	// through a helper that rejects negative values instead of letting them wrap around.
	return availableBytes(st.Bavail, st.Bsize), nil
}

type integer interface {
	~int32 | ~int64 | ~uint32 | ~uint64
}

// availableBytes multiplies the free block count by the block size. A negative value (never expected
// from statfs) counts as zero, and an overflowing product saturates at the maximum.
func availableBytes[A, B integer](blocks A, blockSize B) uint64 {
	if blocks < 0 || blockSize < 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(blocks), uint64(blockSize))
	if hi != 0 {
		return math.MaxUint64
	}
	return lo
}
