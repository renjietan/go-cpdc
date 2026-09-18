//go:build !windows

package fsutil

import "os"

func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
