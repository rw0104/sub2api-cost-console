package routepool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndependentStoresClaimOnceAndPreserveUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.json")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for _, s := range []*Store{a, b} {
		wg.Go(func() {
			if s.ClaimProbe("shared", false) == nil {
				claimed.Add(1)
			}
		})
	}
	wg.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("claimed %d times", claimed.Load())
	}
	if err := a.Change([]string{"shared"}, "available", "manual", false); err != nil {
		t.Fatal(err)
	}
	if b.Get("shared").State != "available" {
		t.Fatal("manual recycle in another process remained stale")
	}
	if err := a.Change([]string{"alpha"}, "disabled", "manual", false); err != nil {
		t.Fatal(err)
	}
	if err := b.Change([]string{"beta"}, "failed", "timeout", false); err != nil {
		t.Fatal(err)
	}
	latest, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Get("alpha").State != "disabled" || latest.Get("beta").State != "failed" || latest.Get("shared").Attempts != 1 {
		t.Fatalf("stale update lost records: %#v", latest.entries)
	}
}

func TestPoolLockChild(t *testing.T) {
	path := os.Getenv("CCODEX_POOL_LOCK_TEST_PATH")
	if path == "" {
		return
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Claim("shared-process-node", false) == nil {
		fmt.Print("CLAIMED\n")
	}
}

func TestIndependentProcessesClaimOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.json")
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			cmd := exec.Command(os.Args[0], "-test.run=^TestPoolLockChild$")
			cmd.Env = append(os.Environ(), "CCODEX_POOL_LOCK_TEST_PATH="+path)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("child failed: %v %s", err, output)
				return
			}
			if strings.Contains(string(output), "CLAIMED") {
				claimed.Add(1)
			}
		})
	}
	wg.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("claimed %d times across processes", claimed.Load())
	}
}

func TestReloadFailsClosedAndManualRepairRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if s.Claim("node", false) == nil || s.Err() == nil {
		t.Fatal("corruption was ignored")
	}
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if s.Claim("node", false) == nil {
		t.Fatal("sticky failure was reset without explicit change")
	}
	if err := s.Change([]string{"node"}, "available", "manual", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Claim("node", false); err != nil {
		t.Fatal(err)
	}
}

func TestPoolLockTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lockPool(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	started := time.Now()
	if s.Claim("node", false) == nil || s.Err() == nil {
		t.Fatal("claim ignored process lock")
	}
	if elapsed := time.Since(started); elapsed < poolLockTimeout || elapsed > poolLockTimeout+2*time.Second {
		t.Fatalf("lock acquisition not bounded: %s", elapsed)
	}
}

func TestPoolRejectsLinksAndCredentialFields(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "pool.json")
	if err := os.Symlink(target, link); err == nil {
		if _, err := Open(link); err == nil {
			t.Fatal("symlinked pool accepted")
		}
	}
	linkedDir := filepath.Join(dir, "linked")
	if err := os.Symlink(t.TempDir(), linkedDir); err == nil {
		if _, err := Open(filepath.Join(linkedDir, "pool.json")); err == nil {
			t.Fatal("symlinked parent accepted")
		}
	}
	s, err := Open(filepath.Join(dir, "safe.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Change([]string{"http://user:password@localhost:80"}, "used", "manual", false) == nil {
		t.Fatal("credential-bearing ID accepted")
	}
	if s.Change([]string{"node"}, "used", "Authorization: Bearer secret", false) == nil {
		t.Fatal("credential-bearing reason accepted")
	}
	if err := s.Change([]string{"node"}, "used", "manual", false); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(s.path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("file permissions: %v %v", info, err)
		}
		info, err = os.Stat(filepath.Dir(s.path))
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("directory permissions: %v %v", info, err)
		}
	}
}
