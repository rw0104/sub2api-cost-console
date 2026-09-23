package routepool

import (
	"path/filepath"
	"testing"
)

func TestRecoverOnlyMisclassifiedStateQualifications(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.json")
	pool, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ id, state, reason string }{{"mismatch", "failed", "shape_mismatch"}, {"missing", "failed", "missing_state_header"}, {"network", "failed", "network_failed"}, {"manual", "disabled", "shape_mismatch"}, {"used", "used", "accepted"}} {
		if err := pool.Change([]string{entry.id}, entry.state, entry.reason, true); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	count, err := restarted.RecoverQualificationFailures()
	if err != nil || count != 2 {
		t.Fatalf("reclassified=%d err=%v", count, err)
	}
	if pool.Get("mismatch").State != "available" || pool.Get("missing").State != "available" || pool.Get("network").State != "failed" || pool.Get("manual").State != "disabled" || pool.Get("used").State != "used" {
		t.Fatal("migration changed unrelated lifecycle states")
	}
	if pool.Get("mismatch").Attempts != 1 {
		t.Fatal("migration erased attempts")
	}
	if count, err = pool.RecoverQualificationFailures(); err != nil || count != 0 {
		t.Fatal("migration is not idempotent")
	}
}

func TestQualificationCompletionCannotUndoManualStop(t *testing.T) {
	pool, _ := Open("")
	token, err := pool.ClaimProbeWithToken("node", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = pool.Change([]string{"node"}, "disabled", "manual_disabled", false); err != nil {
		t.Fatal(err)
	}
	applied, err := pool.Complete("node", token, "available", "shape_mismatch")
	if err != nil || applied || pool.Get("node").State != "disabled" {
		t.Fatal("qualification completion overrode manual stop")
	}
}
