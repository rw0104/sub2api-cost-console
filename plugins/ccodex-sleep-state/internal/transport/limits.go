package transport

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type accountLimit struct {
	blocked        bool
	rejectedStatus int
	retryUntil     time.Time
}

// Limits keeps upstream authentication and quota pauses per account. A valid
// cached state must never bypass an upstream 401, 403, or active 429 cooldown.
type Limits struct {
	mu       sync.Mutex
	entries  map[int64]accountLimit
	cooldown time.Duration
}

func NewLimits(cooldown time.Duration) *Limits {
	if cooldown <= 0 {
		cooldown = 180 * time.Second
	}
	return &Limits{entries: make(map[int64]accountLimit), cooldown: cooldown}
}

func (l *Limits) SetCooldown(cooldown time.Duration) {
	if l == nil || cooldown <= 0 {
		return
	}
	l.mu.Lock()
	l.cooldown = cooldown
	l.mu.Unlock()
}

func (l *Limits) Check(accountID int64, now time.Time) (status int, retryAfter time.Duration) {
	if l == nil || accountID <= 0 {
		return 0, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[accountID]
	if !ok {
		return 0, 0
	}
	if entry.blocked {
		return entry.rejectedStatus, 0
	}
	if now.Before(entry.retryUntil) {
		return http.StatusTooManyRequests, time.Until(entry.retryUntil)
	}
	delete(l.entries, accountID)
	return 0, 0
}

func (l *Limits) Record(accountID int64, status int, retryAfter time.Duration, now time.Time) {
	if l == nil || accountID <= 0 || (status != http.StatusUnauthorized && status != http.StatusForbidden && status != http.StatusTooManyRequests) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[accountID]
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		entry.blocked = true
		entry.rejectedStatus = status
	} else if !entry.blocked {
		entry.rejectedStatus = status
	}
	if retryAfter < l.cooldown {
		retryAfter = l.cooldown
	}
	until := now.Add(retryAfter)
	if until.After(entry.retryUntil) {
		entry.retryUntil = until
	}
	l.entries[accountID] = entry
}

func RetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := at.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}
