package turnstate

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

type Snapshot struct {
	Token   Token
	Route   int
	Version uint64
}

type Status struct {
	Usable           bool   `json:"usable"`
	Version          uint64 `json:"version"`
	RemainingSeconds int    `json:"remaining_seconds"`
	Ready            bool   `json:"ready"`
	Strikes          int    `json:"strikes"`
	Candidates       uint64 `json:"observations"`
}

type stateMachine struct {
	mu            sync.Mutex
	policy        Policy
	active, ready Snapshot
	version       uint64
	strikes       int
	candidates    uint64
}

func (m *stateMachine) setPolicy(policy Policy) {
	m.mu.Lock()
	m.policy = policy
	m.mu.Unlock()
}

func (m *stateMachine) acquire(now time.Time) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promote(now)
	return m.active, m.policy.Accept(m.active.Token, now)
}

func (m *stateMachine) promote(now time.Time) {
	active, ready := m.active, m.ready
	if m.policy.Accept(ready.Token, now) && ready.Token.Fingerprint != active.Token.Fingerprint &&
		(!m.policy.Accept(active.Token, now) || m.strikes >= 2 || (now.Add(m.policy.Refresh).After(active.Token.Issued.Add(m.policy.TTL)) && ready.Token.Issued.After(active.Token.Issued))) {
		m.version++
		ready.Version = m.version
		m.active = ready
		m.ready = Snapshot{}
		m.strikes = 0
	}
}

func (m *stateMachine) offer(token Token, route int, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.candidates++
	if !m.policy.Accept(token, now) {
		return false
	}
	if m.active.Token.Fingerprint == token.Fingerprint {
		return true
	}
	if !m.policy.Accept(m.active.Token, now) {
		m.version++
		m.active = Snapshot{Token: token, Route: route, Version: m.version}
		m.strikes = 0
		return true
	}
	if m.ready.Token.Value == "" || !token.Issued.Before(m.ready.Token.Issued) {
		m.ready = Snapshot{Token: token, Route: route}
	}
	m.promote(now)
	return true
}

func (m *stateMachine) observe(value string, used Snapshot, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if value == "" {
		return false
	}
	m.candidates++
	token, err := Parse(value)
	suspect := err != nil || !m.policy.Accept(token, now)
	if used.Version == m.active.Version && used.Token.Fingerprint == m.active.Token.Fingerprint {
		if suspect {
			m.strikes++
		} else {
			m.strikes = 0
		}
	}
	return suspect
}

func (m *stateMachine) needsRefresh(now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promote(now)
	return !m.policy.Accept(m.active.Token, now) || m.strikes >= 2 || now.Add(m.policy.Refresh).After(m.active.Token.Issued.Add(m.policy.TTL))
}

func (m *stateMachine) status(now time.Time) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	remaining := int(m.active.Token.Issued.Add(m.policy.TTL).Sub(now).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	return Status{Usable: m.policy.Accept(m.active.Token, now), Version: m.active.Version, RemainingSeconds: remaining,
		Ready: m.policy.Accept(m.ready.Token, now), Strikes: m.strikes, Candidates: m.candidates}
}

// Store isolates one target state machine per account and outbound model.
type Store struct {
	mu       sync.Mutex
	machines map[string]*stateMachine
}

func New() *Store { return &Store{machines: make(map[string]*stateMachine)} }

func (s *Store) machine(accountID int64, model string, policy Policy) *stateMachine {
	key := machineKey(accountID, model)
	s.mu.Lock()
	machine := s.machines[key]
	if machine == nil {
		machine = &stateMachine{policy: policy}
		s.machines[key] = machine
	}
	s.mu.Unlock()
	machine.setPolicy(policy)
	return machine
}

func (s *Store) Acquire(accountID int64, model string, policy Policy, now time.Time) (Snapshot, bool) {
	if accountID <= 0 || strings.TrimSpace(model) == "" {
		return Snapshot{}, false
	}
	return s.machine(accountID, model, policy).acquire(now)
}

func (s *Store) Offer(accountID int64, model, value string, route int, policy Policy, now time.Time) bool {
	if accountID <= 0 || strings.TrimSpace(model) == "" {
		return false
	}
	token, err := Parse(value)
	if err != nil {
		return false
	}
	return s.machine(accountID, model, policy).offer(token, route, now)
}

func (s *Store) Observe(accountID int64, model, value string, used Snapshot, policy Policy, now time.Time) bool {
	if accountID <= 0 || strings.TrimSpace(model) == "" {
		return value != ""
	}
	return s.machine(accountID, model, policy).observe(value, used, now)
}

func (s *Store) NeedsRefresh(accountID int64, model string, policy Policy, now time.Time) bool {
	if accountID <= 0 || strings.TrimSpace(model) == "" {
		return true
	}
	return s.machine(accountID, model, policy).needsRefresh(now)
}

func (s *Store) Status(accountID int64, model string, policy Policy, now time.Time) Status {
	return s.machine(accountID, model, policy).status(now)
}

func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.machines)
}

// Clear removes every account/model machine. It is used when the outbound
// route set changes because snapshots store route indexes, not route URLs.
func (s *Store) Clear() {
	s.mu.Lock()
	s.machines = make(map[string]*stateMachine)
	s.mu.Unlock()
}

func machineKey(accountID int64, model string) string {
	return strconv.FormatInt(accountID, 10) + "\x00" + strings.TrimSpace(model)
}
