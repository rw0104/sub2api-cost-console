package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"local.sub2api/ccodex-sleep-state/internal/config"
	"local.sub2api/ccodex-sleep-state/internal/upstream/routepool"
)

func TestCoreManagementLifecyclePersistsWithoutReplayingCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routepool.json")
	newTestPlugin := func() *plugin {
		p := newPlugin()
		p.openPool = func() (*routepool.Store, error) { return routepool.Open(path) }
		t.Cleanup(func() {
			if p.core != nil {
				p.core.Close()
			}
		})
		return p
	}
	p := newTestPlugin()
	raw, err := p.ValidateConfig(context.Background(), []byte(`{"proxy_urls":["http://127.0.0.1:19890"],"route_request":{"operation":"discover"}}`))
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse(raw)
	if err != nil || c.RouteReport == nil || len(c.RouteReport.Nodes) != 1 {
		t.Fatal("missing discovered route")
	}
	id := c.RouteReport.Nodes[0].ID
	c.CoreRequest = &config.CoreRequest{Operation: "pool", Action: "disabled", RouteIDs: []string{id}}
	raw, _ = json.Marshal(c)
	saved, err := p.ValidateConfig(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	c, err = config.Parse(saved)
	if err != nil || c.CoreRequest != nil || c.CoreReport == nil || len(c.CoreReport.Pool) != 1 || c.CoreReport.Pool[0].State != "disabled" {
		t.Fatalf("invalid lifecycle report: %v", err)
	}
	if len(c.CoreReport.Sessions) != 0 || c.CoreReport.Engines != 0 {
		t.Fatal("disabled temporary process invented a live state")
	}
	restored := newTestPlugin()
	if err := restored.ApplyConfig(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	if restored.pool != nil || restored.core != nil {
		t.Fatal("ApplyConfig replayed pool command or created a runtime")
	}
	c.CoreRequest = &config.CoreRequest{Operation: "status"}
	raw, _ = json.Marshal(c)
	status, err := restored.ValidateConfig(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Parse(status)
	if err != nil || loaded.CoreReport.Pool[0].State != "disabled" {
		t.Fatal("node lifecycle was lost across process restart")
	}
	loaded.CoreRequest = &config.CoreRequest{Operation: "pool", Action: "available", RouteIDs: []string{id}}
	raw, _ = json.Marshal(loaded)
	recycled, err := restored.ValidateConfig(context.Background(), raw)
	if err != nil || !strings.Contains(string(recycled), `"state":"available"`) {
		t.Fatal("manual recycle failed")
	}
}

func TestCoreStorageErrorIsVisibleAndNoCommandPersists(t *testing.T) {
	p := newPlugin()
	p.openPool = func() (*routepool.Store, error) { return nil, errors.New("test private path must not be displayed") }
	raw, err := p.ValidateConfig(context.Background(), []byte(`{"core_request":{"operation":"status"}}`))
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse(raw)
	if err != nil || c.CoreReport == nil || c.CoreReport.ErrorCode != "CORE_STORAGE_UNAVAILABLE" || c.CoreRequest != nil {
		t.Fatal("missing safe storage diagnostic")
	}
	if strings.Contains(string(raw), "private path") {
		t.Fatal("raw storage error leaked")
	}
}
