package pluginv2

import (
	"strings"
	"testing"
)

func TestNormalizeOpenAIPlanType(t *testing.T) {
	for raw, want := range map[string]string{
		"Free": "free", "chatgpt_plus": "plus", "ChatGPT Pro": "pro", "pro_20x": "pro", "prolite": "pro_lite", "Pro 5x": "pro_lite",
		"TEAM": "team", "Business Standard": "team", "business": "business", "self_serve_business_prolite": "business_premium", "business_premium": "business_premium",
		"self_serve_business_usage_based": "business_usage_based", "Enterprise": "enterprise", "ChatGPT Edu": "edu", "K-12": "k12", "chatgpt_for_teachers": "k12", "ChatGPT Go": "go",
		"": "unknown", "some-secret-token": "unknown", "prosumer": "unknown", strings.Repeat("x", 129): "unknown",
	} {
		if got := NormalizeOpenAIPlanType(raw); got != want {
			t.Errorf("normalization of %q = %q, want %q", raw, got, want)
		}
		if got := NormalizeOpenAIPlanType(want); got != want {
			t.Errorf("canonical type changed: %q -> %q", want, got)
		}
	}
}
