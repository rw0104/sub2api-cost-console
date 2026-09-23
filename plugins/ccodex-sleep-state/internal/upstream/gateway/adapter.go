package gateway

import (
	"context"
	"sync"
	"time"
)

type dispatchObserverKey struct{}

// WithDispatchObserver identifies the one formal dispatch separately from
// synthetic probes. The host must not retry a generation after this point.
func WithDispatchObserver(ctx context.Context, observe func()) context.Context {
	return context.WithValue(ctx, dispatchObserverKey{}, observe)
}

func observeDispatch(ctx context.Context) {
	if observe, ok := ctx.Value(dispatchObserverKey{}).(func()); ok {
		observe()
	}
}

// CredentialLimits is process-local bookkeeping shared by successive immutable
// engine generations. Configuration changes and subscription refreshes must not
// clear an upstream authentication rejection or Retry-After pause.
// Only credential/workspace digests and rejection metadata are retained here.
type CredentialLimits struct {
	mu         sync.Mutex
	entries    map[string]*credentialLimit
	references map[string]int
	probeSlot  chan struct{}
}

func NewCredentialLimits() *CredentialLimits {
	return &CredentialLimits{entries: make(map[string]*credentialLimit), references: make(map[string]int), probeSlot: make(chan struct{}, 1)}
}

// Caller holds mu. Never discard a guard while an old immutable engine can
// still receive an upstream rejection for it.
func (l *CredentialLimits) releaseLocked(key string, now time.Time) {
	if l.references[key] > 1 {
		l.references[key]--
		return
	}
	delete(l.references, key)
	l.collectKeyLocked(key, now)
}

func (l *CredentialLimits) collectKeyLocked(key string, now time.Time) {
	if l.references[key] > 0 {
		return
	}
	limit := l.entries[key]
	if limit == nil {
		return
	}
	limit.mu.Lock()
	removable := !limit.blocked && !now.Before(limit.retryUntil)
	limit.mu.Unlock()
	if removable {
		delete(l.entries, key)
	}
}

func (l *CredentialLimits) collectLocked(now time.Time) {
	for key := range l.entries {
		l.collectKeyLocked(key, now)
	}
}

// retireSessionLocked runs with both engine.mu and sharedLimits.mu held.
// Active requests keep the reference until release, even after Engine.Close.
func (e *Engine) retireSessionLocked(key string, s *session) {
	s.mu.Lock()
	s.retired = true
	if s.sharedLimits != nil && s.busy == 0 && s.probing == nil && !s.referenceReleased {
		s.referenceReleased = true
		s.sharedLimits.releaseLocked(s.credentialKey, time.Now())
	}
	s.mu.Unlock()
	delete(e.sessions, key)
}

// ShareCredentialLimits must be called before serving or starting Run.
func (e *Engine) ShareCredentialLimits(limits *CredentialLimits) {
	if limits != nil {
		e.sharedLimits = limits
		e.limits = limits.entries
		e.probeSlot = limits.probeSlot
	}
}
