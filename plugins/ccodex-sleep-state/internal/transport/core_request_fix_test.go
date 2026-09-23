package transport

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"local.sub2api/ccodex-sleep-state/internal/config"
)

func corePolicyFailure(t *testing.T, runtime *CoreRuntime, cfg *config.Config, s *fakeStream, messageCode string) *v1.ForwardResponseError {
	t.Helper()
	if err := runtime.Forward(s, cfg); err != nil {
		t.Fatal(err)
	}
	if len(s.responses) != 1 || s.responses[0].GetError() == nil {
		t.Fatalf("expected one local policy Error, got %v", s.responses)
	}
	f := s.responses[0].GetError()
	if f.Code != "PROTECTION_BUSY" || f.RequestSent || !strings.Contains(f.Message, messageCode) {
		t.Fatalf("wrong policy boundary: code=%s sent=%v message=%s", f.Code, f.RequestSent, f.Message)
	}
	return f
}

func TestCoreRequestFixShapeMismatchDoesNotPoisonProxyPool(t *testing.T) {
	makeToken := func(blocks int, issued time.Time) string {
		raw := make([]byte, 57+16*blocks)
		raw[0] = 0x80
		binary.BigEndian.PutUint64(raw[1:9], uint64(issued.Unix()))
		return base64.URLEncoding.EncodeToString(raw)
	}
	for _, token := range []struct{ name, value string }{
		{"shape_mismatch", makeToken(11, time.Now())},
		{"missing_state_header", ""},
		{"invalid_state_envelope", "not-a-state"},
		{"state_time_rejected", makeToken(10, time.Now().Add(-24*time.Hour))},
	} {
		for _, strict := range []bool{false, true} {
			t.Run(token.name+"/"+fmt.Sprint(strict), func(t *testing.T) {
				var probes, formal atomic.Int32
				runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					if bytes.Contains(body, []byte("Reply with OK.")) {
						probes.Add(1)
					} else {
						formal.Add(1)
					}
					if token.value != "" {
						w.Header().Set("X-Codex-Turn-State", token.value)
					}
					fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
				})
				cfg.PoolEnabled = true
				cfg.FailClosed = strict
				s := coreStream(origin+"/responses", []byte(coreGeneration))
				if strict {
					corePolicyFailure(t, runtime, cfg, s, "state_unavailable")
				} else {
					status, _, _ := coreResult(t, runtime, cfg, s)
					if status != 200 {
						t.Fatalf("fallback=%d", status)
					}
				}
				if probes.Load() != 1 || formal.Load() != map[bool]int32{true: 0, false: 1}[strict] {
					t.Fatalf("probes/formal=%d/%d", probes.Load(), formal.Load())
				}
				entry := runtime.pool.Get("route-fixture")
				if entry.State == "failed" || (strict && (entry.State != "available" || entry.Reason != token.name)) {
					t.Fatalf("header qualification failure poisoned the usable proxy: %+v", entry)
				}
			})
		}
	}
}

func TestCoreRequestFixExplicitModelAllowlistAcceptsNewModel(t *testing.T) {
	var calls atomic.Int32
	runtime, cfg, origin := coreFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, "ok") })
	cfg.Models = []string{"new-codex-model"}
	cfg.InjectState = false
	status, _, _ := coreResult(t, runtime, cfg, coreStream(origin+"/responses", []byte(strings.ReplaceAll(coreGeneration, "gpt-6-astra", "new-codex-model"))))
	if status != 200 || calls.Load() != 1 {
		t.Fatal("configured model did not reach upstream")
	}
}
