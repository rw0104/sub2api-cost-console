package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionInfoReportsExtensionCapabilities(t *testing.T) {
	info := versionInfo(
		"0.1.173",
		"29009f0b2ea1",
		"2026-08-09T16:09:17+08:00",
		"1.0.0",
		"account_cost_loss_ledger.v1",
	)

	require.Equal(t,
		"Sub2API 0.1.173 (commit: 29009f0b2ea1, built: 2026-08-09T16:09:17+08:00, extension: 1.0.0, capabilities: account_cost_loss_ledger.v1)",
		info,
	)
}
