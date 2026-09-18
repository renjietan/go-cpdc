//go:build windows

package fsutil

import "golang.org/x/sys/windows"

func AvailableSpace(path string) (uint64, error) {
	root, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(root, &available, nil, nil); err != nil {
		return 0, err
	}
	return available, nil
}
