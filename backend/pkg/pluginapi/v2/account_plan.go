package pluginv2

import "strings"

// AccountSubscription describes only the current host account, never a
// credential. WorkspaceID binds the hint to the selected ChatGPT account and
// must not appear in public diagnostics.
type AccountSubscription struct {
	PlanType    string `json:"plan_type"`
	Source      string `json:"source"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// NormalizeOpenAIPlanType keeps subscription identity separate from a plugin's
// state-length heuristic. Unknown values are never echoed verbatim.
func NormalizeOpenAIPlanType(raw string) string {
	if len(raw) > 128 {
		return "unknown"
	}
	value := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(raw)))
	switch value {
	case "free", "chatgptfree":
		return "free"
	case "plus", "chatgptplus":
		return "plus"
	case "pro", "chatgptpro", "pro20x", "chatgptpro20x":
		return "pro"
	case "prolite", "chatgptprolite", "pro5x", "chatgptpro5x":
		return "pro_lite"
	case "team", "chatgptteam", "businessstandard":
		return "team"
	case "business", "chatgptbusiness":
		return "business"
	case "selfservebusinessprolite", "businesspremium":
		return "business_premium"
	case "selfservebusinessusagebased", "businessusagebased":
		return "business_usage_based"
	case "enterprise", "chatgptenterprise":
		return "enterprise"
	case "edu", "education", "chatgptedu":
		return "edu"
	case "k12", "chatgptforteachers":
		return "k12"
	case "go", "chatgptgo":
		return "go"
	default:
		return "unknown"
	}
}
