//go:build linux

package fsutil

import "golang.org/x/sys/unix"

// SyncFilesystems flushes pending filesystem data and metadata to stable storage.
func SyncFilesystems() {
	unix.Sync()
}
