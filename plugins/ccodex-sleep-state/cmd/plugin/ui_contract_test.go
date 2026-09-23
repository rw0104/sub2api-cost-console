package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIBridgeSecurityContract(t *testing.T) {
	root := filepath.Join("..", "..", "ui")
	appRaw, err := os.ReadFile(filepath.Join(root, "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	indexRaw, err := os.ReadFile(filepath.Join(root, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	styleRaw, err := os.ReadFile(filepath.Join(root, "style.css"))
	if err != nil {
		t.Fatal(err)
	}
	all := string(appRaw) + "\n" + string(indexRaw) + "\n" + string(styleRaw)
	for _, forbidden := range []string{"localStorage", "sessionStorage", "document.cookie", "XMLHttpRequest", "fetch("} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("UI contains forbidden capability %q", forbidden)
		}
	}
	index := strings.ToLower(string(indexRaw))
	for _, remoteAsset := range []string{"src=\"http://", "src=\"https://", "href=\"http://", "href=\"https://"} {
		if strings.Contains(index, remoteAsset) {
			t.Fatalf("UI loads a remote asset %q", remoteAsset)
		}
	}
	app := string(appRaw)
	for _, required := range []string{"event.source !== parent", "bridge_token", "request_id", "sub2api-plugin-ui", "sub2api-plugin-host", "pagehide", "config.load", "config.save", "route_request", "route_report"} {
		if !strings.Contains(app, required) {
			t.Fatalf("UI Bridge check is missing %q", required)
		}
	}
	for _, unsupported := range []string{"send('config.test'", "send('plugin.status'", `send("config.test"`, `send("plugin.status"`} {
		if strings.Contains(app, unsupported) {
			t.Fatalf("node management uses unsupported v2 host response path %q", unsupported)
		}
	}
}
