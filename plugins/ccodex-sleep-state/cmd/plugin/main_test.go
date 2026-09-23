package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	v2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
)

func TestPluginInfoIsValid(t *testing.T) {
	info, err := newPlugin().GetInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := info.Validate(); err != nil {
		t.Fatal(err)
	}
	if info.PluginID != pluginID || info.PluginVersion != pluginVersion {
		t.Fatalf("unexpected plugin info: %+v", info)
	}
}

func TestPluginInfoMatchesSourceManifest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "manifest.source.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ID           string `json:"id"`
		Version      string `json:"version"`
		Capabilities []struct {
			ID string `json:"id"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ID != pluginID || manifest.Version != pluginVersion || len(manifest.Capabilities) != 1 || manifest.Capabilities[0].ID != v2.CapabilityProtectionTransport {
		t.Fatalf("source manifest is out of sync: %+v", manifest)
	}
}
