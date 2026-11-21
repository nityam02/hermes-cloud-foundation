//go:build unix

package diagnostics

import (
	"math"
	"testing"
)

func TestAvailableBytes(t *testing.T) {
	if got := availableBytes(uint64(10), int64(4096)); got != 40960 {
		t.Errorf("linux-style types: got %d", got)
	}
	if got := availableBytes(uint64(10), uint32(4096)); got != 40960 {
		t.Errorf("darwin-style types: got %d", got)
	}
	if got := availableBytes(uint64(10), int64(-1)); got != 0 {
		t.Errorf("negative block size must count as zero, got %d", got)
	}
	if got := availableBytes(int64(-5), int64(4096)); got != 0 {
		t.Errorf("negative block count must count as zero, got %d", got)
	}
	if got := availableBytes(uint64(math.MaxUint64), int64(2)); got != math.MaxUint64 {
		t.Errorf("overflow must saturate, got %d", got)
	}
}

func TestFreeBytesOnTempDir(t *testing.T) {
	free, err := freeBytes(t.TempDir())
	if err != nil || free == 0 {
		t.Fatalf("freeBytes(temp dir) = %d, %v", free, err)
	}
}
