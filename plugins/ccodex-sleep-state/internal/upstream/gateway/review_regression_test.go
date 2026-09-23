package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/upstream/proxyroute"
	"local.sub2api/ccodex-sleep-state/internal/upstream/routepool"
	"local.sub2api/ccodex-sleep-state/internal/upstream/turnstate"
)

func TestReviewProbeRejectionSurvivesPoolWriteFailure(t *testing.T) {
	for _, status := range []int{401, 403, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pool.json")
			var calls atomic.Int32
			e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				// Claim has already persisted. Preserve that file but replace its location
				// with a directory so the post-response write fails deterministically.
				if err := os.Rename(path, path+".backup"); err != nil {
					t.Error(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Error(err)
				}
				w.WriteHeader(status)
			}))
			var err error
			e.pool, err = routepool.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			e.config.PoolEnabled = true
			r := request(generation, "review-fake-rejected-key")
			s, err := e.borrow(r.Header)
			if err != nil {
				t.Fatal(err)
			}
			defer release(s)
			e.refresh(context.Background(), s, true)
			if got, _ := s.rejection(); got != status {
				t.Fatalf("lost upstream rejection: got %d want %d", got, status)
			}
			if e.pool.Err() == nil {
				t.Fatal("expected pool persistence error")
			}
			w := httptest.NewRecorder()
			e.ServeHTTP(w, request(generation, "review-fake-rejected-key"))
			if w.Code != status || calls.Load() != 1 {
				t.Fatalf("rejection bypass: response=%d calls=%d", w.Code, calls.Load())
			}
		})
	}
}

func TestReviewAutomaticProbeAndGenerationClaimOnce(t *testing.T) {
	// Separate engines sharing one store exercise the reservation boundary used
	// by background probes and user generations, without contacting real hosts.
	for i := 0; i < 20; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var calls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); complete(w, fakeToken(10, byte(i+1))) })
			probe, _ := testEngine(t, handler)
			generationEngine, _ := testEngine(t, handler)
			shared, _ := routepool.Open("")
			probe.pool, generationEngine.pool = shared, shared
			probe.config.PoolEnabled = true
			generationEngine.config.PoolEnabled = true
			generationEngine.config.EgressMode = "random"
			generationEngine.SetInjection(false)
			s, err := probe.borrow(request(generation, "review-fake-probe-key").Header)
			if err != nil {
				t.Fatal(err)
			}
			defer release(s)
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); <-start; probe.refresh(context.Background(), s, true) }()
			go func() {
				defer wg.Done()
				<-start
				generationEngine.ServeHTTP(httptest.NewRecorder(), request(generation, "review-fake-generation-key"))
			}()
			close(start)
			wg.Wait()
			if calls.Load() != 1 {
				t.Fatalf("same node dispatched %d times", calls.Load())
			}
			if got := shared.Get("test-route").Attempts; got != 1 {
				t.Fatalf("same node claimed %d times", got)
			}
		})
	}
}

func TestReviewOnDemandBackgroundReacquiresOnlyOne(t *testing.T) {
	var calls atomic.Int32
	e, _ := testEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		complete(w, fakeToken(10, byte(n+10)))
	}))
	e.routes = append(e.routes, proxyroute.Route{ID: "second-route", Transport: &http.Transport{Proxy: nil}})
	e.SetRefreshMode("on_demand")
	s, err := e.borrow(request(generation, "review-fake-hold-key").Header)
	if err != nil {
		t.Fatal(err)
	}
	defer release(s)
	token, _ := turnstate.Parse(fakeToken(10, 1))
	s.state.Offer(token, 0, time.Now())
	active, _ := s.state.Acquire(time.Now())
	s.state.RejectAndPromote(active, time.Now())
	// Run calls refresh with bootstrap=false after expiry or mismatch. It must
	// stop once a new active is acquired, not spend a second request on standby.
	e.refresh(context.Background(), s, false)
	status := s.state.Status(time.Now())
	if calls.Load() != 1 || !status.Usable || status.Ready {
		t.Fatalf("calls=%d usable=%v ready=%v", calls.Load(), status.Usable, status.Ready)
	}
}
