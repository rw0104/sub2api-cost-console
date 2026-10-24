package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration221CreatesObservationOnlyEconomicsSamples(t *testing.T) {
	content, err := FS.ReadFile("221_account_economics_samples.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS account_economics_samples")
	require.Contains(t, sql, "UNIQUE (scope_key, sample_bucket)")
	require.Contains(t, sql, "billed_usd_total NUMERIC")
	require.Contains(t, sql, "account_cost_usd_total NUMERIC")
	require.NotContains(t, sql, "procurement_cost")
	require.NotContains(t, sql, "FOREIGN KEY")
}
