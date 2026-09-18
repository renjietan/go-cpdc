//go:build !linux

package fsutil

// SyncFilesystems is unnecessary on the non-Linux CPDC targets.
func SyncFilesystems() {}
