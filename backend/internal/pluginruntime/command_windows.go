//go:build windows

package pluginruntime

import (
	"os/exec"
	"syscall"
)

func hideCommandWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

// HideCommandWindow applies the Windows background-process flags to a command
// launched by a host service. It is also used for legacy plugin binaries that
// were compiled with the console subsystem.
func HideCommandWindow(cmd *exec.Cmd) {
	hideCommandWindow(cmd)
}
