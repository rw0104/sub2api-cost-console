package service

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCostAccountingSharedFrontendBackendCases(t *testing.T) {
	data, err := os.ReadFile("testdata/cost_accounting_parity.json")
	require.NoError(t, err)
	var cases []struct {
		Name      string         `json:"name"`
		Type      string         `json:"type"`
		Plan      string         `json:"plan"`
		CreatedAt time.Time      `json:"created_at"`
		Now       time.Time      `json:"now"`
		Profile   map[string]any `json:"profile"`
		Currency  string         `json:"expected_currency"`
		Cost      float64        `json:"expected_cost"`
		Hourly    float64        `json:"expected_hourly"`
	}
	require.NoError(t, json.Unmarshal(data, &cases))
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			account := Account{ID: 1, Type: tc.Type, Platform: PlatformOpenAI, CreatedAt: tc.CreatedAt, Extra: map[string]any{"plan_type": tc.Plan}}
			if tc.Profile != nil {
				account.Extra["cost_profile"] = tc.Profile
			}
			profile, err := resolveAccountCostProfileSnapshot(&account)
			require.NoError(t, err)
			require.Equal(t, tc.Currency, profile.Currency)
			cost, hourly := accruedCostAt(profile, tc.Now)
			require.InDelta(t, tc.Cost, cost, 1e-8)
			require.InDelta(t, tc.Hourly, hourly, 1e-8)
			cny, _, cnyHourly, invalid := summarizeProcurementEconomics([]Account{account}, nil, "openai", 7, tc.Now, false)
			require.Zero(t, invalid)
			factor := 1.0
			if tc.Currency == "USD" {
				factor = 7
			}
			require.InDelta(t, tc.Cost*factor, cny, 1e-8)
			require.InDelta(t, tc.Hourly*factor, cnyHourly, 1e-8)
		})
	}
}
