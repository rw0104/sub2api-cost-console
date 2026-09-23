package main

import (
	"context"
	"testing"

	"local.sub2api/ccodex-sleep-state/internal/config"
)

func TestLivePolicyChangesPreserveRouteGeneration(t *testing.T) {
	p := newPlugin()
	base := []byte(`{"enabled":true,"inject_state":true,"harvest_on_demand":true,"state_refresh_mode":"standby","proxy_urls":["http://127.0.0.1:19001"]}`)
	if err := p.ApplyConfig(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	before := p.config.Load()
	first, err := p.registryFor(context.Background(), before, "")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	updated := []byte(`{"enabled":true,"inject_state":false,"harvest_on_demand":true,"state_refresh_mode":"on_demand","proxy_urls":["http://127.0.0.1:19001"]}`)
	if err := p.ApplyConfig(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	second, err := p.registryFor(context.Background(), p.config.Load(), "")
	if err != nil || first != second {
		t.Fatal("live mode toggle replaced active route generation")
	}
	if before.InjectState != true || before.StateRefreshMode != "standby" {
		t.Fatal("comparison mutated stored config")
	}
	changed, _ := config.Parse([]byte(`{"cooldown_seconds":600}`))
	if sameExceptLivePolicies(before, changed) {
		t.Fatal("probe budget change was treated as live atomic toggle")
	}
}
