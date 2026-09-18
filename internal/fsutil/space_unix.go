//go:build !windows

package fsutil

import (
	"math"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func AvailableSpace(path string) (uint64, error) {
	for {
		var stat unix.Statfs_t
		err := unix.Statfs(path, &stat)
		if err == nil {
			blockSize := uint64(stat.Bsize)
			if uint64(stat.Bavail) > math.MaxUint64/blockSize {
				return math.MaxUint64, nil
			}
			return uint64(stat.Bavail) * blockSize, nil
		}
		if !os.IsNotExist(err) {
			return 0, err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return 0, err
		}
		path = parent
	}
}
