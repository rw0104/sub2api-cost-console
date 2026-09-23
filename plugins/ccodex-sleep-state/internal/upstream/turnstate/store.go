package turnstate

import (
	"sync"
	"time"
)

type Snapshot struct {
	Token   Token
	Route   int
	Version uint64
}

// Store has one publisher. Response observations cannot mutate active state.
// Snapshot values are immutable; a request never rereads them halfway through.
type Store struct {
	holdActive    bool
	mu            sync.Mutex
	policy        Policy
	active, ready Snapshot
	version       uint64
	strikes       int
	candidates    uint64
}

func New(p Policy) *Store { return &Store{policy: p} }
func (s *Store) Acquire(now time.Time) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promote(now)
	return s.active, s.policy.Accept(s.active.Token, now)
}
func (s *Store) promote(now time.Time) {
	a, r := s.active, s.ready
	if s.policy.Accept(r.Token, now) && r.Token.Fingerprint != a.Token.Fingerprint &&
		(!s.policy.Accept(a.Token, now) || s.strikes >= 2 || (!s.holdActive && now.Add(s.policy.Refresh).After(a.Token.Issued.Add(s.policy.TTL)) && r.Token.Issued.After(a.Token.Issued))) {
		s.version++
		r.Version = s.version
		s.active = r
		s.ready = Snapshot{}
		s.strikes = 0
	}
}
func (s *Store) Offer(t Token, route int, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.candidates++
	if !s.policy.Accept(t, now) {
		return false
	}
	if s.active.Token.Fingerprint == t.Fingerprint {
		return true
	}
	if !s.policy.Accept(s.active.Token, now) {
		s.version++
		s.active = Snapshot{t, route, s.version}
		s.strikes = 0
		return true
	}
	if s.ready.Token.Value == "" || !t.Issued.Before(s.ready.Token.Issued) {
		s.ready = Snapshot{Token: t, Route: route}
	}
	s.promote(now)
	return true
}
func (s *Store) Observe(value string, used Snapshot, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value == "" {
		return false
	}
	s.candidates++
	t, err := Parse(value)
	suspect := err != nil || !s.policy.Accept(t, now)
	if used.Version == s.active.Version && used.Token.Fingerprint == s.active.Token.Fingerprint {
		if suspect {
			s.strikes++
		} else {
			s.strikes = 0
		}
	}
	return suspect
}
func (s *Store) NeedsRefresh(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promote(now)
	return !s.policy.Accept(s.active.Token, now) || s.strikes >= 2 || now.Add(s.policy.Refresh).After(s.active.Token.Issued.Add(s.policy.TTL))
}

type Status struct {
	Usable           bool   `json:"usable"`
	Version          uint64 `json:"version"`
	RemainingSeconds int    `json:"remaining_seconds"`
	Ready            bool   `json:"ready"`
	Strikes          int    `json:"strikes"`
	Candidates       uint64 `json:"observations"`
}

func (s *Store) Status(now time.Time) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	left := int(s.active.Token.Issued.Add(s.policy.TTL).Sub(now).Seconds())
	if left < 0 {
		left = 0
	}
	return Status{s.policy.Accept(s.active.Token, now), s.active.Version, left, s.policy.Accept(s.ready.Token, now), s.strikes, s.candidates}
}

// RejectAndPromote changes only future admissions. In-flight snapshots remain
// immutable and a stale response cannot invalidate a newer active state.
func (s *Store) RejectAndPromote(used Snapshot, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if used.Version != s.active.Version || used.Token.Fingerprint != s.active.Token.Fingerprint {
		return false
	}
	s.active = Snapshot{}
	s.strikes = 0
	s.promote(now)
	return s.policy.Accept(s.active.Token, now)
}

func (s *Store) HoldActive(hold bool) { s.mu.Lock(); defer s.mu.Unlock(); s.holdActive = hold }
