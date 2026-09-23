package main

import (
	"os"
	"path/filepath"
	"testing"
)

func installedExecutable(root, version, runtimeKey string) string {
	name := "plugin"
	if runtimeKey == "windows-amd64" {
		name += ".exe"
	}
	return filepath.Join(root, "installed", pluginID, version, "runtimes", runtimeKey, name)
}

func TestPoolPathStableAcrossUpgradesAndPlatforms(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "installed", pluginID, ".state", "routepool.json")
	for _, version := range []string{"0.5.0-abcdef123456-193832", "0.6.0-012345abcdef-57329"} {
		for _, platform := range []string{"windows-amd64", "linux-amd64", "linux-arm64"} {
			got, err := resolvePoolPath(installedExecutable(root, version, platform))
			if err != nil || got != want {
				t.Fatalf("%s/%s: %q %v", version, platform, got, err)
			}
		}
	}
}

func TestPoolPathRejectsUnexpectedLayouts(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "plugin.exe"),
		installedExecutable(root, "0.5.0", "windows-amd64"),
		installedExecutable(root, "0.5.0-abcdef123456-123", "darwin-arm64"),
		filepath.Join(root, "staging", pluginID, "0.5.0-abcdef123456-123", "runtimes", "linux-amd64", "plugin"),
		filepath.Join(root, "installed", "com.example.other", "0.5.0-abcdef123456-123", "runtimes", "linux-amd64", "plugin"),
		"runtimes/linux-amd64/plugin",
	} {
		if path, err := resolvePoolPath(path); err == nil {
			t.Errorf("accepted unexpected layout %s", path)
		}
	}
}

func TestPoolPathValidatesExistingManifest(t *testing.T) {
	executable := installedExecutable(t.TempDir(), "0.5.0-abcdef123456-123", "linux-amd64")
	versionDir := filepath.Dir(filepath.Dir(filepath.Dir(executable)))
	if err := os.MkdirAll(versionDir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(versionDir, "manifest.json")
	for _, contents := range []string{`{"id":"com.example.other","version":"0.5.0"}`, `{"id":"` + pluginID + `","version":"0.4.0"}`, `{`} {
		if err := os.WriteFile(manifest, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolvePoolPath(executable); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	if err := os.WriteFile(manifest, []byte(`{"id":"`+pluginID+`","version":"0.5.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePoolPath(executable); err != nil {
		t.Fatal(err)
	}
}

func TestPoolPathRejectsLinkedState(t *testing.T) {
	root := t.TempDir()
	executable := installedExecutable(root, "0.5.0-abcdef123456-123", "linux-amd64")
	idDir := filepath.Join(root, "installed", pluginID)
	if err := os.MkdirAll(idDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(idDir, ".state")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := resolvePoolPath(executable); err == nil {
		t.Fatal("accepted symlinked state directory")
	}
}
