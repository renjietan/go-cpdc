//go:build windows

package selfupdate

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// newDetachedCommand builds a replacement process that is detached from the
// current console and process group so it survives the parent exit.
func newDetachedCommand(name string, args []string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	return cmd
}
