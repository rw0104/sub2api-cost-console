package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration196CreatesAppendOnlyAccountCostLossLedger(t *testing.T) {
	content, err := FS.ReadFile("196_account_cost_loss_events.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS account_cost_loss_events")
	require.Contains(t, sql, "account_id_snapshot BIGINT NOT NULL")
	require.Contains(t, sql, "REFERENCES accounts(id) ON DELETE SET NULL")
	require.Contains(t, sql, "idempotency_key VARCHAR(255) NOT NULL UNIQUE")
	require.Contains(t, sql, "source_event_id BIGINT REFERENCES account_cost_loss_events(id)")
	require.Contains(t, sql, "event_type IN ('terminal_loss', 'refund', 'reversal')")
	require.NotContains(t, strings.ToUpper(sql), "ON DELETE CASCADE")
}
