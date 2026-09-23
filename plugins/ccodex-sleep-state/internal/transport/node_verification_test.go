package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoreNodeVerificationUsesPassthroughSessionAndSafeStatus(t *testing.T) {
	var probes, formal atomic.Int32
	runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("Reply with OK.")) {
			probes.Add(1)
			if r.Header.Get("X-Codex-Turn-State") != "" || r.Header.Get("Session_id") != "" {
				t.Error("manual validation inherited conversation state")
			}
		} else {
			formal.Add(1)
		}
		w.Header().Set("X-Codex-Turn-State", coreToken(7))
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	})
	cfg.InjectState, cfg.HarvestOnDemand = false, false
	status, _, _ := coreResult(t, runtime, cfg, coreStream(origin+"/responses", []byte(coreGeneration)))
	if status != 200 || probes.Load() != 0 || formal.Load() != 1 {
		t.Fatal("plain forwarding did not establish the expected session")
	}
	sessionID := runtime.Status()["sessions"].([]map[string]any)[0]["id"].(string)
	ctx, cancel := context.WithCancel(context.Background())
	if err := runtime.VerifyNodes(ctx, sessionID, nil); err != nil {
		t.Fatal(err)
	}
	cancel() // Ending config.save must not cancel its background verification.
	deadline := time.Now().Add(3 * time.Second)
	var report map[string]any
	for time.Now().Before(deadline) {
		reports := runtime.Status()["node_verifications"].([]map[string]any)
		if len(reports) == 1 && reports[0]["state"] == "completed" {
			report = reports[0]
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if report == nil || report["account_id"] != int64(41) || probes.Load() != 1 {
		t.Fatalf("runtime did not expose completed account-scoped validation: %+v, probes=%d", report, probes.Load())
	}
	row := report["rows"].([]any)[0].(map[string]any)
	if row["qualified"] != true || row["header_present"] != true || row["observed_length"] != float64(292) || row["http_status"] != float64(200) {
		t.Fatalf("header result was lost in runtime status: %+v", row)
	}
	raw, _ := json.Marshal(runtime.Status())
	for _, secret := range []string{coreToken(7), "fixture-credential-12345", "private original prompt", "private-session"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("status contains sensitive values")
		}
	}
}

func TestCoreNodeVerificationInvalidationCancelsBackgroundWork(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("Reply with OK.")) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			close(stopped)
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	})
	cfg.InjectState, cfg.HarvestOnDemand = false, false
	coreResult(t, runtime, cfg, coreStream(origin+"/responses", []byte(coreGeneration)))
	sessionID := runtime.Status()["sessions"].([]map[string]any)[0]["id"].(string)
	if err := runtime.VerifyNodes(context.Background(), sessionID, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("verification did not start")
	}
	runtime.Invalidate()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime invalidation left a live verification request")
	}
	if len(runtime.Status()["node_verifications"].([]map[string]any)) != 0 {
		t.Fatal("retired verification remained attached to new runtime")
	}
	if err := runtime.VerifyNodes(context.Background(), sessionID, nil); err == nil {
		t.Fatal("retired session accepted a verification")
	}
}
