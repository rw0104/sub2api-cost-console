package transport

import (
	"net/http"
	"testing"
	"time"
)

func TestLimitsBlockAuthAndCoolDownRateLimits(t *testing.T) {
	now := time.Now()
	limits := NewLimits(10 * time.Second)
	limits.Record(7, http.StatusTooManyRequests, time.Second, now)
	status, retry := limits.Check(7, now.Add(time.Second))
	if status != http.StatusTooManyRequests || retry <= 0 {
		t.Fatalf("rate limit not active: %d %s", status, retry)
	}
	status, _ = limits.Check(7, now.Add(11*time.Second))
	if status != 0 {
		t.Fatalf("rate limit did not expire: %d", status)
	}
	limits.Record(7, http.StatusForbidden, 0, now)
	status, _ = limits.Check(7, now.Add(24*time.Hour))
	if status != http.StatusForbidden {
		t.Fatalf("auth block did not persist: %d", status)
	}
	limits.Record(8, http.StatusUnauthorized, 0, now)
	status, _ = limits.Check(8, now.Add(24*time.Hour))
	if status != http.StatusUnauthorized {
		t.Fatalf("401 block did not persist: %d", status)
	}
}

func TestRetryAfterParsesSecondsAndHTTPDate(t *testing.T) {
	now := time.Now()
	if got := RetryAfter("12", now); got != 12*time.Second {
		t.Fatalf("seconds retry-after = %s", got)
	}
	if got := RetryAfter(now.Add(8*time.Second).UTC().Format(http.TimeFormat), now); got <= 0 {
		t.Fatalf("date retry-after = %s", got)
	}
}
