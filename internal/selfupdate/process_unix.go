//go:build !windows

package selfupdate

import (
	"errors"
	"syscall"
)

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func terminateProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}
