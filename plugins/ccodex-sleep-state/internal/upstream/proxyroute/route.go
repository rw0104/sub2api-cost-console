// Package proxyroute bridges the vendored gateway to plugin-owned routes.
package proxyroute

import "net/http"

// Route matches upstream's immutable route value. The plugin Registry owns
// adapter lifetime; engines may only close idle HTTP connections.
type Route struct {
	ID, StableID, DisplayName, Protocol string
	Transport                           *http.Transport
}

func (r Route) Close() {
	if r.Transport != nil {
		r.Transport.CloseIdleConnections()
	}
}
