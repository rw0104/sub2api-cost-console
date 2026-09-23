package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveSubscriptionConfigTestPublishesAvailability(t *testing.T) {
	subscriptionURL := os.Getenv("CCODEX_TEST_SUBSCRIPTION_URL")
	if subscriptionURL == "" {
		t.Skip("set CCODEX_TEST_SUBSCRIPTION_URL for live subscription integration")
	}
	raw, err := json.Marshal(map[string]any{"subscriptions": []map[string]string{{"url": subscriptionURL}}})
	if err != nil {
		t.Fatal(err)
	}
	p := newPlugin()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := p.TestConfig(ctx, raw); err != nil {
		t.Fatal(err)
	}
	health, err := p.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(health.Message, "route connectivity:") || !strings.Contains(health.Message, "/") {
		t.Fatalf("availability summary missing: %q", health.Message)
	}
	t.Log(health.Message)
}
