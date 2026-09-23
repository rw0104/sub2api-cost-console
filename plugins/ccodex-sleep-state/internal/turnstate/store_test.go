package turnstate

import (
	"encoding/base64"
	"encoding/binary"
	"testing"
	"time"
)

func encodedToken(blocks int, issued time.Time, fill byte) string {
	raw := make([]byte, 57+16*blocks)
	raw[0] = 0x80
	binary.BigEndian.PutUint64(raw[1:9], uint64(issued.Unix()))
	for index := 9; index < len(raw); index++ {
		raw[index] = fill
	}
	return base64.URLEncoding.EncodeToString(raw)
}

func TestParseAndStoreIsolation(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	value := encodedToken(1, now.Add(-time.Minute), 1)
	token, err := Parse(value)
	if err != nil || token.Blocks != 1 || token.Fingerprint == "" {
		t.Fatalf("parse failed: %+v %v", token, err)
	}
	policy := Policy{Blocks: 1, TTL: time.Hour, Refresh: 10 * time.Minute}
	store := New()
	if !store.Offer(7, "gpt-6-astra", value, 0, policy, now) {
		t.Fatal("expected state to be offered")
	}
	if _, ok := store.Acquire(8, "gpt-6-astra", policy, now); ok {
		t.Fatal("state must not cross accounts")
	}
	if _, ok := store.Acquire(7, "gpt-5.6-sol", policy, now); ok {
		t.Fatal("state must not cross models")
	}
	snapshot, ok := store.Acquire(7, "gpt-6-astra", policy, now)
	if !ok || snapshot.Token.Value != value || snapshot.Version != 1 {
		t.Fatalf("unexpected snapshot: %+v %v", snapshot, ok)
	}
}

func TestReadyPromotesAfterTwoSuspectResponses(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	policy := Policy{Blocks: 1, TTL: time.Hour, Refresh: 10 * time.Minute}
	store := New()
	active := encodedToken(1, now.Add(-10*time.Minute), 1)
	ready := encodedToken(1, now.Add(-5*time.Minute), 2)
	if !store.Offer(7, "gpt-6-astra", active, 0, policy, now) || !store.Offer(7, "gpt-6-astra", ready, 1, policy, now) {
		t.Fatal("failed to populate active/ready")
	}
	used, ok := store.Acquire(7, "gpt-6-astra", policy, now)
	if !ok || used.Token.Value != active {
		t.Fatalf("healthy active changed too early: %+v", used)
	}
	if !store.Observe(7, "gpt-6-astra", "invalid", used, policy, now) || !store.Observe(7, "gpt-6-astra", "invalid", used, policy, now) {
		t.Fatal("invalid observations must be suspect")
	}
	promoted, ok := store.Acquire(7, "gpt-6-astra", policy, now)
	if !ok || promoted.Token.Value != ready || promoted.Route != 1 || promoted.Version != 2 {
		t.Fatalf("ready state was not promoted: %+v", promoted)
	}
	status := store.Status(7, "gpt-6-astra", policy, now)
	if status.Strikes != 0 || status.Candidates != 4 || status.Ready {
		t.Fatalf("unexpected status after promotion: %+v", status)
	}
}

func TestNormalizeChecksEnvelopeLengthAndShape(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	value := encodedToken(1, now, 3)
	if Normalize(value, len(value)) != value {
		t.Fatal("valid envelope should be accepted")
	}
	if Normalize(value, len(value)+4) != "" || Normalize("bad", len(value)) != "" {
		t.Fatal("invalid state should be rejected")
	}
	if blocks, ok := BlocksForEncodedLength(len(value)); !ok || blocks != 1 {
		t.Fatalf("length did not resolve to blocks: %d %v", blocks, ok)
	}
}
