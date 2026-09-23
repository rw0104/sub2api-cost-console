package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/upstream/proxyroute"
	"local.sub2api/ccodex-sleep-state/internal/upstream/settings"
	"local.sub2api/ccodex-sleep-state/internal/upstream/turnstate"
)

// Each route has a real localhost CONNECT proxy tunnelling to a separate TLS
// fixture. The URL and credentials remain identical across the route snapshot.
func verificationProxyRoute(t *testing.T, id string, handler http.Handler) proxyroute.Route {
	t.Helper()
	origin := httptest.NewTLSServer(handler)
	t.Cleanup(origin.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", 400)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("origin credentials exposed to CONNECT proxy")
		}
		remote, err := net.Dial("tcp", origin.Listener.Addr().String())
		if err != nil {
			http.Error(w, "fixture unavailable", 502)
			return
		}
		local, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			remote.Close()
			return
		}
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		go func() { defer local.Close(); defer remote.Close(); _, _ = io.Copy(remote, buffered) }()
		go func() { defer local.Close(); defer remote.Close(); _, _ = io.Copy(local, remote) }()
	}))
	t.Cleanup(proxy.Close)
	proxyURL, _ := url.Parse(proxy.URL)
	transport := origin.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	t.Cleanup(transport.CloseIdleConnections)
	return proxyroute.Route{ID: id, StableID: id, DisplayName: "fixture-" + id, Protocol: "http", Transport: transport}
}

func verificationEngine(t *testing.T, routes []proxyroute.Route) *Engine {
	t.Helper()
	c := settings.Default()
	c.Upstream = "https://127.0.0.1:443/backend-api/codex"
	c.InjectionDisabled, c.HarvestDisabled, c.PoolEnabled = true, true, true
	c.ProbeSeconds, c.MaxProbes, c.CooldownSeconds = 2, 10, 1
	e := New(c, routes, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(e.Close)
	return e
}

func verificationSession(t *testing.T, e *Engine) *session {
	t.Helper()
	s, err := e.borrow(http.Header{"Authorization": {"Bearer synthetic-verification-secret"}}, settings.Model)
	if err != nil {
		t.Fatal(err)
	}
	release(s)
	return s
}

func waitVerification(t *testing.T, e *Engine, predicate func(NodeVerificationReport) bool) NodeVerificationReport {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		reports := e.NodeVerifications()
		if len(reports) == 1 && predicate(reports[0]) {
			return reports[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("verification did not reach expected state: %+v", e.NodeVerifications())
	return NodeVerificationReport{}
}

func TestNodeVerificationDistinguishesHeaderQualificationThroughCONNECT(t *testing.T) {
	tokens := []string{"", "not-an-envelope", fakeToken(9, 2), fakeToken(10, 3), fakeToken(10, 4)}
	ids := []string{"missing", "invalid", "wrong-shape", "qualified", "incomplete"}
	var probes, ordinary atomic.Int32
	routes := make([]proxyroute.Route, 0, len(ids))
	for i, id := range ids {
		routes = append(routes, verificationProxyRoute(t, id, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer synthetic-verification-secret" {
				t.Error("RAM credential was not sent to origin")
			}
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "private prompt") {
				ordinary.Add(1)
			} else {
				probes.Add(1)
			}
			if r.Header.Get(turnstate.Header) != "" {
				t.Error("verification injected prior turn-state")
			}
			if i == 4 {
				w.Header().Set(turnstate.Header, tokens[i])
				fmt.Fprint(w, "data: {\"type\":\"response.created\"}\n\n")
				return
			}
			if i == 0 {
				fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
				return
			}
			complete(w, tokens[i])
		})))
	}
	e := verificationEngine(t, routes)
	// Supply the credential via a successful ordinary generation while both
	// injection and automatic collection are disabled.
	w := httptest.NewRecorder()
	e.ServeHTTP(w, request(generation, "synthetic-verification-secret"))
	if w.Code != 200 || ordinary.Load() != 1 || probes.Load() != 0 {
		t.Fatalf("passthrough status=%d ordinary=%d probes=%d", w.Code, ordinary.Load(), probes.Load())
	}
	var s *session
	for _, candidate := range e.sessions {
		s = candidate
	}
	if s == nil {
		t.Fatal("passthrough did not retain a RAM session")
	}
	activeToken, _ := turnstate.Parse(fakeToken(10, 99))
	if !s.state.Offer(activeToken, 0, time.Now()) {
		t.Fatal("could not establish existing active state")
	}
	if err := e.StartNodeVerification(s.id, nil); err != nil {
		t.Fatal(err)
	}
	report := waitVerification(t, e, func(r NodeVerificationReport) bool { return r.State == "completed" })
	wanted := []string{"missing_state_header", "invalid_state_envelope", "shape_mismatch", "accepted", "incomplete_response"}
	if report.Total != 5 || report.Completed != 5 || probes.Load() != 5 {
		t.Fatalf("unexpected report: %+v, probes=%d", report, probes.Load())
	}
	for i, row := range report.Rows {
		if row.Status != wanted[i] || row.HeaderPresent != (tokens[i] != "") || row.ObservedLength != len(tokens[i]) || row.ExpectedLength != 292 || row.HTTPStatus != 200 || row.CheckedAt.IsZero() {
			t.Errorf("incorrect safe result %s: %+v", ids[i], row)
		}
		if row.Qualified != (i == 3) || row.Parsed != (i >= 2) {
			t.Errorf("incorrect qualification %s: %+v", ids[i], row)
		}
	}
	if report.Rows[2].ObservedBlocks != 9 || report.Rows[3].ObservedBlocks != 10 {
		t.Fatal("wrong block counts")
	}
	if e.pool.Get("wrong-shape").State != "available" {
		t.Fatal("shape mismatch poisoned network pool")
	}
	if active, ok := s.state.Acquire(time.Now()); !ok || active.Route != 0 || active.Token.Value != activeToken.Value || s.state.Status(time.Now()).Ready {
		t.Fatal("diagnostic verification changed the active/standby state or selected route")
	}
	encoded, _ := json.Marshal(report)
	for _, secret := range []string{"synthetic-verification-secret", tokens[2], tokens[3], "private prompt", "127.0.0.1"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("sensitive data leaked into verification report: %q", secret)
		}
	}
}

func TestNodeVerificationStopsOnAuthenticationAndQuotaDecisions(t *testing.T) {
	for _, status := range []int{401, 403, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			routes := []proxyroute.Route{}
			for i := 0; i < 3; i++ {
				routes = append(routes, verificationProxyRoute(t, fmt.Sprint(i), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(status) })))
			}
			e := verificationEngine(t, routes)
			s := verificationSession(t, e)
			if err := e.StartNodeVerification(s.id, nil); err != nil {
				t.Fatal(err)
			}
			report := waitVerification(t, e, func(r NodeVerificationReport) bool { return r.State == "paused" })
			if calls.Load() != 1 || report.Completed != 1 || report.Rows[0].HTTPStatus != status || report.Rows[1].Status != "pending" {
				t.Fatalf("upstream decision rotated exits: %+v calls=%d", report, calls.Load())
			}
			if err := e.StartNodeVerification(s.id, nil); err == nil {
				t.Fatal("manual restart bypassed upstream limit")
			}
			if e.pool.Get("0").State != "available" {
				t.Fatal("upstream refusal became network failure")
			}
		})
	}
}

func TestNodeVerificationRespectsRoundsAndCancellation(t *testing.T) {
	var calls atomic.Int32
	routes := []proxyroute.Route{}
	for i := 0; i < 4; i++ {
		routes = append(routes, verificationProxyRoute(t, fmt.Sprint(i), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); complete(w, "") })))
	}
	e := verificationEngine(t, routes)
	e.config.MaxProbes = 2
	s := verificationSession(t, e)
	started := time.Now()
	if err := e.StartNodeVerification(s.id, nil); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("background scheduling waited for subscription scan")
	}
	report := waitVerification(t, e, func(r NodeVerificationReport) bool { return r.State == "cooling_down" })
	if calls.Load() != 2 || report.Completed != 2 || !report.NextRunAt.After(time.Now()) {
		t.Fatalf("round budget not respected: %+v calls=%d", report, calls.Load())
	}
	if err := e.CancelNodeVerification(s.id); err != nil {
		t.Fatal(err)
	}
	<-e.verifications[s.id].done
	report = waitVerification(t, e, func(r NodeVerificationReport) bool { return r.State == "cancelled" })
	if calls.Load() != 2 || report.Rows[2].Status != "cancelled" {
		t.Fatal("cancellation did not preserve finished rows and cancel remaining rows")
	}
	// Restart resumes only remaining nodes and still observes the existing
	// cooldown instead of letting repeated button presses create a burst.
	if err := e.StartNodeVerification(s.id, nil); err != nil {
		t.Fatal(err)
	}
	report = waitVerification(t, e, func(r NodeVerificationReport) bool { return r.State == "completed" })
	if calls.Load() != 4 || report.Completed != 4 {
		t.Fatalf("restart repeated completed rows: %+v calls=%d", report, calls.Load())
	}
}

func TestNodeVerificationRuntimeRetirementCancelsInflightRequest(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	route := verificationProxyRoute(t, "slow", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(turnstate.Header, fakeToken(10, 8))
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	}))
	e := verificationEngine(t, []proxyroute.Route{route})
	s := verificationSession(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.Run(ctx) }()
	if err := e.StartNodeVerification(s.id, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime retirement did not stop verification")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request remained active")
	}
	report := e.NodeVerifications()[0]
	if report.State != "cancelled" || report.Reason != "runtime_stopped" || report.Rows[0].Qualified {
		t.Fatalf("retired report: %+v", report)
	}
	if err := e.StartNodeVerification(s.id, nil); err == nil {
		t.Fatal("retired runtime accepted new verification")
	}
}

func TestNodeVerificationUsesSharedProbeSlotAndCurrentMembership(t *testing.T) {
	var calls atomic.Int32
	route := verificationProxyRoute(t, "existing", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); complete(w, fakeToken(10, 9)) }))
	e := verificationEngine(t, []proxyroute.Route{route})
	limits := NewCredentialLimits()
	e.ShareCredentialLimits(limits)
	s := verificationSession(t, e)
	if err := e.StartNodeVerification(s.id, []string{"forged-report-node"}); err == nil {
		t.Fatal("accepted node outside current route snapshot")
	}
	limits.probeSlot <- struct{}{}
	if err := e.StartNodeVerification(s.id, []string{"existing"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("manual scan bypassed process-global probe slot")
	}
	<-limits.probeSlot
	report := waitVerification(t, e, func(r NodeVerificationReport) bool { return r.State == "completed" })
	if calls.Load() != 1 || !report.Rows[0].Qualified {
		t.Fatal("node did not qualify after slot was released")
	}
}

func TestNodeVerificationNetworkFailureIsNotMissingHeader(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, _ := url.Parse("http://" + listener.Addr().String())
	listener.Close()
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	route := proxyroute.Route{ID: "offline", Transport: transport}
	e := verificationEngine(t, []proxyroute.Route{route})
	s := verificationSession(t, e)
	if err := e.StartNodeVerification(s.id, nil); err != nil {
		t.Fatal(err)
	}
	report := waitVerification(t, e, func(r NodeVerificationReport) bool { return r.State == "completed" })
	row := report.Rows[0]
	if row.Status != "network_failed" || row.HTTPStatus != 0 || row.HeaderPresent || row.Parsed || row.Qualified || row.ObservedLength != 0 {
		t.Fatalf("transport failure was confused with successful header acquisition: %+v", row)
	}
	if e.pool.Get("offline").State != "available" {
		t.Fatal("diagnostic test mutated the user's routing pool")
	}
}
