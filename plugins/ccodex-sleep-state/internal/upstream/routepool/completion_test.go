package routepool

import (
	"path/filepath"
	"testing"
)

func TestCompletionPreservesManualChangesAcrossStores(t *testing.T) {
	for _, action := range []string{"disabled", "available"} {
		for _, outcome := range []string{"used", "failed"} {
			t.Run(action+"/"+outcome, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "pool.json")
				worker, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				manager, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				token, err := worker.ClaimWithToken("node", false)
				if err != nil {
					t.Fatal(err)
				}
				if err := manager.Change([]string{"node"}, action, "manual", false); err != nil {
					t.Fatal(err)
				}
				before := manager.Get("node")
				applied, err := worker.Complete("node", token, outcome, "request_finished")
				if err != nil || applied {
					t.Fatalf("stale completion applied=%v err=%v", applied, err)
				}
				if after := worker.Get("node"); after != before {
					t.Fatalf("manual action overwritten: %#v -> %#v", before, after)
				}
			})
		}
	}
}

func TestOlderCompletionCannotOverwriteNewerClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.json")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := first.ClaimProbeWithToken("node", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Change([]string{"node"}, "available", "manual", false); err != nil {
		t.Fatal(err)
	}
	newToken, err := second.ClaimWithToken("node", false)
	if err != nil {
		t.Fatal(err)
	}
	if oldToken == newToken || oldToken == "" || newToken == "" {
		t.Fatal("claim revisions are not unique")
	}
	before := second.Get("node")
	if applied, err := first.Complete("node", oldToken, "failed", "timeout"); err != nil || applied {
		t.Fatalf("stale result applied=%v err=%v", applied, err)
	}
	if after := second.Get("node"); after != before {
		t.Fatalf("new claim overwritten: %#v -> %#v", before, after)
	}
	if applied, err := second.Complete("node", newToken, "used", "request_dispatched"); err != nil || !applied {
		t.Fatalf("current result applied=%v err=%v", applied, err)
	}
	if applied, err := second.Complete("node", newToken, "failed", "duplicate"); err != nil || applied {
		t.Fatalf("duplicate result applied=%v err=%v", applied, err)
	}
	if got := second.Get("node"); got.State != "used" || got.Attempts != 2 {
		t.Fatalf("unexpected final state %#v", got)
	}
}
