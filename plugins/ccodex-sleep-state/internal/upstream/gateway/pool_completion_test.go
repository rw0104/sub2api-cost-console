package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"local.sub2api/ccodex-sleep-state/internal/upstream/routepool"
)

// The upstream handler acts at the exact gap between durable reservation and
// completion, through a separate store as a temporary UI process would.
func TestInFlightCompletionPreservesManualPoolActions(t *testing.T) {
	for _, kind := range []string{"generation", "probe"} {
		for _, action := range []string{"disabled", "available"} {
			for _, failUpstream := range []bool{false, true} {
				name := kind + "/" + action
				if failUpstream {
					name += "/failure"
				}
				t.Run(name, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "pool.json")
					manager, err := routepool.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					calls := 0
					e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if got := manager.Get("test-route"); got.State != "used" || got.Attempts != 1 {
							t.Errorf("not reserved: %#v", got)
						}
						if err := manager.Change([]string{"test-route"}, action, "manual", false); err != nil {
							t.Error(err)
						}
						if failUpstream {
							w.WriteHeader(http.StatusBadGateway)
							return
						}
						complete(w, fakeToken(10, 1))
					}))
					e.pool, err = routepool.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					e.config.PoolEnabled = true
					if kind == "generation" {
						e.SetInjection(false)
						e.ServeHTTP(httptest.NewRecorder(), request(generation, "fixture-manual-completion-key"))
					} else {
						s, err := e.borrow(request(generation, "fixture-manual-completion-key").Header)
						if err != nil {
							t.Fatal(err)
						}
						defer release(s)
						e.refresh(context.Background(), s, true)
					}
					if calls != 1 {
						t.Fatalf("upstream called %d times", calls)
					}
					if got := manager.Get("test-route"); got.State != action || got.Reason != "manual" || got.Attempts != 1 {
						t.Fatalf("manual action overwritten by %s completion: %#v", kind, got)
					}
				})
			}
		}
	}
}
