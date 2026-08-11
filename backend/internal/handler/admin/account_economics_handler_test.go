package admin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseEconomicsAccountIDsAcceptsStableProviderScope(t *testing.T) {
	ids, err := parseEconomicsAccountIDs("42, 7,42")
	require.NoError(t, err)
	require.Equal(t, []int64{42, 7}, ids)
}

func TestParseEconomicsAccountIDsRejectsInvalidValues(t *testing.T) {
	_, err := parseEconomicsAccountIDs("42,not-an-id")
	require.Error(t, err)
}
