package service

import (
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseHeaderProbeStateReportsSafeShapeOnly(t *testing.T) {
	raw := make([]byte, 57+16*10)
	raw[0] = 0x80
	binary.BigEndian.PutUint64(raw[1:9], uint64(time.Now().Unix()))
	value := base64.RawURLEncoding.EncodeToString(raw)

	parsed, blocks := parseHeaderProbeState(value)
	require.True(t, parsed)
	require.Equal(t, 10, blocks)
	parsed, blocks = parseHeaderProbeState("not-a-state")
	require.False(t, parsed)
	require.Zero(t, blocks)
}

func TestHeaderProbeSignalsNeverIncludeRawState(t *testing.T) {
	secret := "opaque-state-value"
	signals := headerProbeSignals(http.Header{"X-Codex-Turn-State": []string{secret}})
	require.Equal(t, "true", signals["x-sub2api-probe-state-present"][0])
	require.NotContains(t, signals, secret)
	require.NotContains(t, signals, "X-Codex-Turn-State")
}
