package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMapUpstreamStatusNeverReturnsAccountAuthAsPanelUnauthorized(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   int
	}{
		{name: "unauthorized account", status: http.StatusUnauthorized, want: http.StatusBadGateway},
		{name: "forbidden account", status: http.StatusForbidden, want: http.StatusBadGateway},
		{name: "rate limited account", status: http.StatusTooManyRequests, want: http.StatusTooManyRequests},
		{name: "other client error", status: http.StatusBadRequest, want: http.StatusBadGateway},
		{name: "upstream server error", status: http.StatusBadGateway, want: http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, mapUpstreamStatus(tt.status))
		})
	}
}
