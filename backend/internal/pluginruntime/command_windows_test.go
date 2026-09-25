//go:build windows

package pluginruntime

import (
	"os/exec"
	"testing"
)

func TestHideCommandWindowSetsBackgroundProcessFlags(t *testing.T) {
	cmd := exec.Command("plugin.exe")
	hideCommandWindow(cmd)
	attrs := cmd.SysProcAttr
	if attrs == nil {
		t.Fatal("HideCommandWindow did not configure Windows process attributes")
	}
	if !attrs.HideWindow {
		t.Fatal("HideWindow was not enabled")
	}
	if attrs.CreationFlags&0x08000000 == 0 {
		t.Fatalf("CREATE_NO_WINDOW flag missing: %#x", attrs.CreationFlags)
	}
}
