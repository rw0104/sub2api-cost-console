package config

import (
	"encoding/json"
	"testing"
)

func TestCoreDefaultsAndLegacyPolicyMigration(t *testing.T) {
	fresh, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.StateRefreshMode != "on_demand" || fresh.AccountMode != "auto" || fresh.MaxProbesPerRound != 6 || fresh.MaxBodyBytes != 64<<20 {
		t.Fatalf("unexpected new policy: %+v", fresh.CoreOptions)
	}
	legacy, err := Parse([]byte(`{"state_ttl_seconds":3600,"state_target_length":332,"max_body_bytes":4194304}`))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.StateRefreshMode != "standby" || legacy.AccountMode != "custom" || legacy.MaxBodyBytes != 4<<20 {
		t.Fatal("legacy policy was silently changed")
	}
	core := legacy.CoreSettings("https://example.test/backend-api/codex")
	if core.BaselineBlocks != 12 || core.RequestLimitMiB != 4 || !core.HarvestDisabled || !core.InjectionDisabled {
		t.Fatal("legacy core translation mismatch")
	}
	encoded, _ := json.Marshal(fresh)
	roundtrip, err := Parse(encoded)
	if err != nil || roundtrip.StateRefreshMode != "on_demand" {
		t.Fatal("normalized new policy changed on reopen")
	}
}

func TestCoreAccountOverridesAndIndependentEgress(t *testing.T) {
	c, err := Parse([]byte(`{"enabled":true,"inject_state":true,"harvest_on_demand":true,"max_probes_per_round":4,"route_mode":"fixed","fixed_route_id":"route-aaaa","egress_mode":"fixed","egress_route":"route-bbbb","accounts":{"7":{"account_mode":"team","state_refresh_mode":"on_demand","max_probes_per_round":2,"pool_enabled":true,"egress_mode":"random"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	global := c.CoreSettings("https://example.test/backend-api/codex")
	if global.PinnedRoute != "route-aaaa" || global.EgressRoute != "route-bbbb" || global.EgressMode != "fixed" {
		t.Fatal("collection and generation exits were collapsed")
	}
	account := c.ForAccount(7).CoreSettings("https://example.test/backend-api/codex")
	if account.AccountMode != "team" || account.MaxProbes != 2 || !account.PoolEnabled || account.EgressMode != "random" || account.InjectionDisabled || account.HarvestDisabled {
		t.Fatal("account core policy did not inherit and override correctly")
	}
	if c.CoreSettings("https://example.test").MaxProbes != 4 {
		t.Fatal("account override mutated global policy")
	}
}

func TestCoreRejectsUnboundedPoliciesAndCommands(t *testing.T) {
	for _, raw := range []string{`{"max_probes_per_round":21}`, `{"max_probes_per_round":-1}`, `{"state_refresh_mode":"always"}`, `{"account_mode":"invented"}`, `{"egress_mode":"fixed"}`, `{"zstd_window_mib":129}`, `{"compact_limit_mib":1}`, `{"max_body_bytes":134217729}`, `{"core_request":{"operation":"retry"}}`, `{"core_request":{"operation":"pool","action":"delete","route_ids":["route-aaaa"]}}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("accepted invalid policy %s", raw)
		}
	}
}

func TestExplicitZeroRefreshIsPreserved(t *testing.T) {
	c, err := Parse([]byte(`{"refresh_before_seconds":0}`))
	if err != nil || c.RefreshBeforeSeconds != 0 {
		t.Fatal("explicit zero refresh changed")
	}
}

func TestExplicitCustom292KeepsItsRuleOverAutomaticAccountHints(t *testing.T) {
	c, err := Parse([]byte(`{"account_mode":"custom","state_target_length":292}`))
	if err != nil {
		t.Fatal(err)
	}
	core := c.CoreSettings("https://example.test/backend-api/codex")
	if core.AccountMode != "personal" || core.BaselineBlocks != 10 {
		t.Fatal("explicit custom 292 would be overridden by an automatic Team hint")
	}
}
