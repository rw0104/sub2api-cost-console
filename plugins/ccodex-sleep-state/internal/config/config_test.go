package config

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestParseDefaultsAndStrictUnknownFields(t *testing.T) {
	c, err := Parse([]byte(`{"enabled":true,"inject_state":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxBodyBytes != DefaultMaxBodyBytes || c.StateTargetLength != DefaultStateTargetLength {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.SubscriptionRefreshSeconds != DefaultSubscriptionRefreshSeconds {
		t.Fatalf("unexpected subscription refresh default: %d", c.SubscriptionRefreshSeconds)
	}
	if _, err := Parse([]byte(`{"unknown":true}`)); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if _, err := Parse([]byte(`{} {}`)); err == nil {
		t.Fatal("trailing JSON must be rejected")
	}
}

func TestNormalizeRejectsStaleRevision(t *testing.T) {
	current, err := Parse([]byte(`{"revision":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize([]byte(`{"revision":2}`), current); err == nil {
		t.Fatal("stale revision must be rejected")
	}
}

func TestProxySchemes(t *testing.T) {
	if _, err := Parse([]byte(`{"proxy_url":"socks5h://127.0.0.1:1080"}`)); err != nil {
		t.Fatal(err)
	}
	c, err := Parse([]byte(`{"proxy_url":"http://user:pass@127.0.0.1:8080"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.RouteURLs(); len(got) != 1 || got[0] != "http://user:pass@127.0.0.1:8080" {
		t.Fatalf("authenticated HTTP proxy was not preserved: %#v", got)
	}
}

func TestProxyURLsMigrateLegacyAndDeduplicate(t *testing.T) {
	c, err := Parse([]byte(`{
		"proxy_url":"HTTP://127.0.0.1:10808/",
		"proxy_urls":["http://127.0.0.1:10808","socks5h://127.0.0.1:10809","socks5h://127.0.0.1:10809"]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	urls := c.RouteURLs()
	if len(urls) != 2 || urls[0] != "http://127.0.0.1:10808" || urls[1] != "socks5h://127.0.0.1:10809" {
		t.Fatalf("unexpected route URLs: %#v", urls)
	}
	if c.RouteSignature() == "" {
		t.Fatal("route signature must not be empty")
	}
	legacy, err := Parse([]byte(`{"proxy_url":"http://127.0.0.1:10808"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := legacy.RouteURLs(); len(got) != 1 || got[0] != "http://127.0.0.1:10808" {
		t.Fatalf("legacy route was not migrated: %#v", got)
	}
}

func TestAccountRouteOverrideAndDirect(t *testing.T) {
	c, err := Parse([]byte(`{
		"proxy_urls":["http://127.0.0.1:10808"],
		"accounts":{"7":{"proxy_urls":["socks5://127.0.0.1:10809"],"direct":true}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	effective := c.ForAccount(7)
	if !effective.Direct || len(effective.RouteURLs()) != 1 || effective.RouteURLs()[0] != "socks5://127.0.0.1:10809" {
		t.Fatalf("unexpected effective account routes: %+v", effective)
	}
	if c.ForAccount(8).Direct || c.ForAccount(8).RouteURLs()[0] != "http://127.0.0.1:10808" {
		t.Fatalf("global routes changed: %+v", c.ForAccount(8))
	}
}

func TestFixedRouteModeValidationAndAccountOverride(t *testing.T) {
	c, err := Parse([]byte(`{
		"proxy_urls":["http://127.0.0.1:18080"],
		"route_mode":"fixed",
		"fixed_route_id":"route-abc",
		"accounts":{"7":{"route_mode":"round_robin"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.RouteMode != "fixed" || c.FixedRouteID != "route-abc" {
		t.Fatalf("unexpected fixed route config: %+v", c)
	}
	if effective := c.ForAccount(7); effective.RouteMode != "round_robin" {
		t.Fatalf("account route mode override not applied: %+v", effective)
	}
	if _, err := Parse([]byte(`{"route_mode":"fixed"}`)); err == nil {
		t.Fatal("fixed mode without route ID must be rejected")
	}
	if _, err := Parse([]byte(`{"route_mode":"random"}`)); err == nil {
		t.Fatal("unknown route mode must be rejected")
	}
}

func TestTooManyProxyURLsRejected(t *testing.T) {
	values := make([]string, MaxRouteURLs+1)
	for i := range values {
		values[i] = "http://127.0.0.1:" + strconv.Itoa(10000+i)
	}
	raw, err := json.Marshal(map[string]any{"proxy_urls": values})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(raw); err == nil {
		t.Fatal("proxy_urls over the limit must be rejected")
	}
}

func TestSubscriptionSourceValidation(t *testing.T) {
	c, err := Parse([]byte(`{
		"proxy_envs":["CCODEX_PROXY"],
		"subscription_proxy_env":"CCODEX_SUBSCRIPTION_PROXY",
		"subscriptions":[{"url":"https://example.test/nodes","user_agent":"test"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Subscriptions) != 1 || c.Subscriptions[0].URL != "https://example.test/nodes" {
		t.Fatalf("unexpected sources: %+v", c)
	}
	if _, err := Parse([]byte(`{"subscriptions":[{"url":"http://example.test/nodes"}]}`)); err == nil {
		t.Fatal("non-loopback HTTP subscription must be rejected")
	}
	if _, err := Parse([]byte(`{"subscriptions":[{"url":"https://example.test/nodes","url_env":"NODES"}]}`)); err == nil {
		t.Fatal("subscription source with multiple selectors must be rejected")
	}
}

func TestAccountOverrideMergesAndValidates(t *testing.T) {
	c, err := Parse([]byte(`{
		"enabled":false,
		"inject_state":false,
		"proxy_url":"http://127.0.0.1:8080",
		"accounts":{"7":{"enabled":true,"inject_state":true,"proxy_url":"","refresh_before_seconds":0,"models":["gpt-6-astra"]}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	effective := c.ForAccount(7)
	if !effective.Enabled || !effective.InjectState || effective.ProxyURL != "" || effective.RefreshBeforeSeconds != 0 || len(effective.Models) != 1 {
		t.Fatalf("unexpected account override: %+v", effective)
	}
	other := c.ForAccount(8)
	if other.Enabled || other.ProxyURL != "http://127.0.0.1:8080" {
		t.Fatalf("global policy changed: %+v", other)
	}
	if _, err := Parse([]byte(`{"accounts":{"07":{"enabled":true}}}`)); err == nil {
		t.Fatal("non-canonical account ID must be rejected")
	}
}

func TestStateTargetLengthMatchesParserCapacity(t *testing.T) {
	if _, err := Parse([]byte(`{"state_target_length":2040}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]byte(`{"state_target_length":2060}`)); err == nil {
		t.Fatal("state length beyond parser capacity must be rejected")
	}
}

func TestAccountRouteInheritanceAndExplicitClearSurviveRoundTrip(t *testing.T) {
	c, err := Parse([]byte(`{"proxy_url":"http://127.0.0.1:8080","accounts":{"7":{"enabled":true},"8":{"proxy_urls":[]},"9":{"proxy_url":"socks5://127.0.0.1:1080"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		inherited := c.ForAccount(7).RouteURLs()
		if len(inherited) != 1 || inherited[0] != "http://127.0.0.1:8080" {
			t.Fatalf("round %d: unrelated override removed inherited route: %v", round, inherited)
		}
		if cleared := c.ForAccount(8).RouteURLs(); len(cleared) != 0 {
			t.Fatalf("round %d: explicit [] failed to clear routes: %v", round, cleared)
		}
		if c.Accounts["7"].ProxyURLs != nil || c.Accounts["8"].ProxyURLs == nil {
			t.Fatal("omitted and explicit empty route lists lost their distinction")
		}
		legacy := c.ForAccount(9).RouteURLs()
		if len(legacy) != 1 || legacy[0] != "socks5://127.0.0.1:1080" {
			t.Fatalf("round %d: legacy override lost: %v", round, legacy)
		}
		raw, marshalErr := json.Marshal(c)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		c, err = Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRouteSignatureDistinguishesClearedAndInheritedAccountSources(t *testing.T) {
	a, _ := Parse([]byte(`{"proxy_urls":["http://127.0.0.1:8080"],"accounts":{"7":{"enabled":true}}}`))
	b, _ := Parse([]byte(`{"proxy_urls":["http://127.0.0.1:8080"],"accounts":{"7":{"enabled":true,"proxy_urls":[]}}}`))
	if a.RouteSignature() == b.RouteSignature() {
		t.Fatal("explicit route clearing must invalidate account state")
	}
}

func TestSourceSignatureIgnoresSelectionAndPolicy(t *testing.T) {
	a, _ := Parse([]byte(`{"proxy_urls":["http://127.0.0.1:8080"]}`))
	b, _ := Parse([]byte(`{"proxy_urls":["http://127.0.0.1:8080"],"route_mode":"fixed","fixed_route_id":"route-abcd","cooldown_seconds":300}`))
	if a.SourceSignature() != b.SourceSignature() {
		t.Fatal("choosing a discovered node must preserve its report")
	}
}

func TestNormalizeDoesNotMutateCurrentLegacyConfig(t *testing.T) {
	current, err := Parse([]byte(`{"revision":3,"proxy_url":"HTTP://127.0.0.1:8080/","subscriptions":[{"url":"https://example.test/sub","include_protocols":["HTTP"]}],"accounts":{"7":{"enabled":true},"8":{"proxy_url":"socks5://127.0.0.1:1080"},"9":{"proxy_urls":[]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(current)
	for _, raw := range [][]byte{before, []byte(`{"revision":3,"proxy_url":"http://127.0.0.1:9000","accounts":{"7":{"proxy_urls":[]}}}`), []byte(`{"revision":2}`)} {
		_, _ = Normalize(raw, current)
	}
	after, _ := json.Marshal(current)
	if string(before) != string(after) {
		t.Fatal("Normalize mutated current nested config or pointer-backed legacy overrides")
	}
	normalized, err := Normalize(before, current)
	if err != nil {
		t.Fatal(err)
	}
	if string(normalized) != string(before) {
		t.Fatal("already normalized legacy configuration was not stable")
	}
}

func TestEmptyGlobalSourceListsHaveStableSignatures(t *testing.T) {
	var previous *Config
	for _, raw := range []string{
		`{"proxy_urls":["http://127.0.0.1:8080"]}`,
		`{"proxy_urls":["http://127.0.0.1:8080"],"proxy_envs":null,"subscriptions":null}`,
		`{"proxy_urls":["http://127.0.0.1:8080"],"proxy_envs":[],"subscriptions":[]}`,
	} {
		c, err := Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if previous != nil && (previous.SourceSignature() != c.SourceSignature() || previous.RouteSignature() != c.RouteSignature()) {
			t.Fatal("equivalent empty global source lists changed a signature")
		}
		previous = c
	}
	// Signatures must be stable for programmatically constructed configs too.
	withEmpty := *previous
	withEmpty.ProxyEnvs, withEmpty.Subscriptions = []string{}, []Subscription{}
	if previous.SourceSignature() != withEmpty.SourceSignature() || previous.RouteSignature() != withEmpty.RouteSignature() {
		t.Fatal("signature depends on nil versus empty optional lists")
	}
}
