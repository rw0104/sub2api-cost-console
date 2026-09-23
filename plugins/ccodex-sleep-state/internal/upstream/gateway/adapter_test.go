package gateway

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/upstream/settings"
)

func sharedEngine(limits *CredentialLimits) *Engine {
	e := New(settings.Default(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.ShareCredentialLimits(limits)
	return e
}

func borrowShared(t *testing.T, e *Engine, credential string) *session {
	t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+credential)
	s, err := e.borrow(h)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSharedLimitsHealthyCredentialRotationDoesNotExhaustLifetimeCapacity(t *testing.T) {
	limits := NewCredentialLimits()
	for i := 0; i < 4200; i++ {
		e := sharedEngine(limits)
		s := borrowShared(t, e, fmt.Sprintf("healthy-oauth-refresh-%d", i))
		release(s)
		e.Close()
		e.Close()
		if len(limits.entries) != 0 || len(limits.references) != 0 {
			t.Fatalf("rotation %d leaked retired guards", i)
		}
	}
}

func TestSharedLimitsRetainOldActiveEngineGuardUntilLateRejection(t *testing.T) {
	limits := NewCredentialLimits()
	old := sharedEngine(limits)
	current := sharedEngine(limits)
	oldSession := borrowShared(t, old, "same-active-credential")
	currentSession := borrowShared(t, current, "same-active-credential")
	if oldSession.limit != currentSession.limit {
		t.Fatal("engines did not share guard")
	}
	release(currentSession)
	current.Close()
	// Retire while a formal request still owns the old session, then create
	// a replacement after retirement. Neither operation can discard its guard.
	old.Close()
	if len(limits.entries) != 1 || limits.references[oldSession.credentialKey] != 1 {
		t.Fatal("draining engine lost its reference")
	}
	replacement := sharedEngine(limits)
	defer replacement.Close()
	replacementSession := borrowShared(t, replacement, "same-active-credential")
	if replacementSession.limit != oldSession.limit {
		t.Fatal("replacement detached draining guard")
	}
	old.reject(oldSession, 403, 0, 0)
	release(oldSession)
	if status, _ := replacementSession.rejection(); status != 403 {
		t.Fatal("late old-engine rejection did not block replacement")
	}
	release(replacementSession)
	replacement.Close()
	if len(limits.entries) != 1 || len(limits.references) != 0 {
		t.Fatal("blocked guard was discarded or reference leaked")
	}
	future := sharedEngine(limits)
	defer future.Close()
	s := borrowShared(t, future, "same-active-credential")
	defer release(s)
	if status, _ := s.rejection(); status != 403 {
		t.Fatal("blocked guard disappeared after engine close")
	}
}

func TestSharedLimitsExpiryDropsOnlyUnreferencedUnblockedGuards(t *testing.T) {
	limits := NewCredentialLimits()
	e := sharedEngine(limits)
	s := borrowShared(t, e, "rate-limited-credential")
	e.reject(s, 429, time.Minute, 0)
	release(s)
	e.Close()
	if len(limits.entries) != 1 {
		t.Fatal("active cooldown was discarded")
	}
	limits.mu.Lock()
	limits.collectLocked(time.Now().Add(time.Hour))
	limits.mu.Unlock()
	if len(limits.entries) != 0 {
		t.Fatal("expired unreferenced cooldown retained")
	}
	e = sharedEngine(limits)
	defer e.Close()
	s = borrowShared(t, e, "active-healthy-credential")
	limits.mu.Lock()
	limits.collectLocked(time.Now().Add(time.Hour))
	limits.mu.Unlock()
	if len(limits.entries) != 1 {
		t.Fatal("live healthy session guard discarded")
	}
	release(s)
	s.mu.Lock()
	s.lastUsed = time.Now().Add(-idleLifetime - time.Second)
	s.mu.Unlock()
	e.backgroundWork(time.Now())
	if len(limits.entries) != 0 || len(limits.references) != 0 {
		t.Fatal("idle expired session guard not collected")
	}
}
