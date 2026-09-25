//go:build !windows

package pluginruntime

import "os/exec"

func hideCommandWindow(*exec.Cmd) {}

func HideCommandWindow(cmd *exec.Cmd) {
	hideCommandWindow(cmd)
}
