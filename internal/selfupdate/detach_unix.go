//go:build !windows

package selfupdate

import (
	"os/exec"
	"syscall"
)

// newDetachedCommand builds a replacement process that starts a new session so
// it keeps running after the current process exits.
func newDetachedCommand(name string, args []string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
