package routepool

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPersistentLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Get("node").State != "available" {
		t.Fatal("default")
	}
	if err = s.Claim("node", false); err != nil {
		t.Fatal(err)
	}
	if s.Claim("node", false) == nil {
		t.Fatal("reused")
	}
	s, err = Open(path)
	if err != nil || s.Get("node").State != "used" {
		t.Fatal("lost persistent state", err)
	}
	if err = s.Change([]string{"node"}, "failed", "timeout", false); err != nil {
		t.Fatal(err)
	}
	if s.Claim("node", true) == nil {
		t.Fatal("failed node reused")
	}
	if err = s.Change([]string{"node"}, "available", "manual", false); err != nil {
		t.Fatal(err)
	}
	if err = s.Claim("node", false); err != nil {
		t.Fatal(err)
	}
	if s.Get("node").Attempts != 2 {
		t.Fatal("attempt count")
	}
}
func TestConcurrentClaimOnce(t *testing.T) {
	s, _ := Open("")
	var n atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			if s.Claim("node", false) == nil {
				n.Add(1)
			}
		})
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatal(n.Load())
	}
}
func TestCorruptPoolFailsClosed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pool.json")
	os.WriteFile(p, []byte("{"), 0600)
	if _, err := Open(p); err == nil {
		t.Fatal("corruption reset silently")
	}
}
