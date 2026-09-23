// Package routepool tracks local node lifecycle, not public IP identity.
package routepool

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"sync"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/upstream/fsutil"
)

type Entry struct {
	State    string    `json:"state"`
	Reason   string    `json:"reason,omitempty"`
	Attempts int       `json:"attempts"`
	Updated  time.Time `json:"updated"`
	Revision string    `json:"revision,omitempty"`
}
type Store struct {
	mu      sync.Mutex
	path    string
	entries map[string]Entry
	err     error
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, entries: map[string]Entry{}}
	if path == "" {
		return s, nil
	}
	unlock, err := lockPool(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.reloadLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,128}$`)
var safeReason = regexp.MustCompile(`^[a-zA-Z0-9_-]{0,128}$`)
var safeRevision = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (s *Store) reloadLocked() error {
	if err := ValidateStoragePath(s.path); err != nil {
		return err
	}
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		s.entries = map[string]Entry{}
		return nil
	}
	if err != nil {
		return errors.New("无法读取代理池记录")
	}
	b, readErr := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return errors.New("无法读取代理池记录")
	}
	var entries map[string]Entry
	if len(b) > 2<<20 || json.Unmarshal(b, &entries) != nil || entries == nil || len(entries) > 8192 {
		return errors.New("代理池记录损坏，已停止自动采集；请保留文件备份后修复")
	}
	for id, e := range entries {
		if !safeID.MatchString(id) || !safeReason.MatchString(e.Reason) || e.Attempts < 0 ||
			(e.Revision != "" && !safeRevision.MatchString(e.Revision)) {
			return errors.New("代理池记录字段无效")
		}
		if e.State != "available" && e.State != "used" && e.State != "failed" && e.State != "disabled" {
			return errors.New("代理池记录状态无效")
		}
	}
	if err := os.Chmod(s.path, 0600); err != nil {
		return errors.New("无法保护代理池记录权限")
	}
	s.entries = entries
	return nil
}
func (s *Store) Get(id string) Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Manual pool actions can run in a temporary plugin process. Reflect those
	// changes before selection, including when every cached node was spent and
	// no new claim would otherwise trigger a durable reload.
	if s.path != "" && s.err == nil {
		_ = s.withDiskLocked(func() error { return nil })
	}
	if s.err != nil {
		return Entry{State: "disabled", Reason: "persistence_unavailable"}
	}
	e, ok := s.entries[id]
	if !ok {
		e.State = "available"
	}
	return e
}
func (s *Store) Err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Store) Change(ids []string, state, reason string, attempt bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withDiskLocked(func() error { return s.changeLocked(ids, state, reason, attempt) })
}

// Every durable mutation reloads while holding an OS lock. Different installed
// versions and temporary validation processes cannot overwrite stale snapshots.
func (s *Store) withDiskLocked(change func() error) error {
	if s.path == "" {
		return change()
	}
	unlock, err := lockPool(s.path)
	if err != nil {
		s.err = err
		return err
	}
	defer unlock()
	if err := s.reloadLocked(); err != nil {
		s.err = err
		return err
	}
	return change()
}
func (s *Store) changeLocked(ids []string, state, reason string, attempt bool) error {
	if state != "available" && state != "used" && state != "failed" && state != "disabled" {
		return errors.New("无效代理池操作")
	}
	if !safeReason.MatchString(reason) {
		return errors.New("代理池原因必须是安全状态码")
	}
	for _, id := range ids {
		if !safeID.MatchString(id) {
			return errors.New("代理池节点标识无效")
		}
	}
	next := make(map[string]Entry, len(s.entries))
	for k, v := range s.entries {
		next[k] = v
	}
	for _, id := range ids {
		e := next[id]
		var revision [16]byte
		if _, err := rand.Read(revision[:]); err != nil {
			s.err = errors.New("无法生成代理池操作标识，已停止自动采集")
			return s.err
		}
		e.Revision = hex.EncodeToString(revision[:])
		e.State, e.Reason, e.Updated = state, reason, time.Now().UTC()
		if attempt {
			e.Attempts++
		}
		next[id] = e
	}
	if len(next) > 8192 {
		return errors.New("代理池历史已达 8192 条，请先归档本地记录")
	}
	if s.path != "" {
		if err := ValidateStoragePath(s.path); err != nil {
			s.err = err
			return err
		}
		b, _ := json.MarshalIndent(next, "", "  ")
		if err := fsutil.Write(s.path, b); err != nil {
			s.err = errors.New("无法保存代理池记录，已停止自动采集")
			return s.err
		}
	}
	s.entries = next
	s.err = nil
	return nil
}

func (s *Store) Claim(id string, allowUsed bool) error {
	_, err := s.ClaimWithToken(id, allowUsed)
	return err
}

// ClaimWithToken returns the persisted revision of this exact reservation.
// Completion must present it so a later manual action or claim takes priority.
func (s *Store) ClaimWithToken(id string, allowUsed bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	var token string
	err := s.withDiskLocked(func() error {
		e := s.entries[id]
		if e.State == "disabled" || e.State == "failed" || (!allowUsed && e.State == "used") {
			return errors.New("节点已被其它请求使用，请刷新池后再试")
		}
		if err := s.changeLocked([]string{id}, "used", "request_started", true); err != nil {
			return err
		}
		token = s.entries[id].Revision
		return nil
	})
	return token, err
}

// ClaimProbe reserves a node atomically against both probes and generation.
// Only an explicit manual retry may reuse a used or failed node.
func (s *Store) ClaimProbe(id string, manual bool) error {
	_, err := s.ClaimProbeWithToken(id, manual)
	return err
}

func (s *Store) ClaimProbeWithToken(id string, manual bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	var token string
	err := s.withDiskLocked(func() error {
		e := s.entries[id]
		if e.State == "disabled" || (!manual && e.State != "" && e.State != "available") {
			return errors.New("节点已被使用或停用，请刷新清单")
		}
		if err := s.changeLocked([]string{id}, "used", "probe_started", true); err != nil {
			return err
		}
		token = s.entries[id].Revision
		return nil
	})
	return token, err
}

// Complete conditionally records a request/probe outcome. A mismatched token is
// an intentional no-op: manual disable/recycle and newer claims must survive an
// older in-flight operation, including across separate plugin processes.
func (s *Store) Complete(id, token, state, reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	if !safeRevision.MatchString(token) || (state != "used" && state != "failed" && state != "available") {
		return false, errors.New("无效代理池完成操作")
	}
	applied := false
	err := s.withDiskLocked(func() error {
		e := s.entries[id]
		if e.Revision != token || e.State != "used" {
			return nil
		}
		if err := s.changeLocked([]string{id}, state, reason, false); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

// RecoverQualificationFailures repairs only the 0.5.0 classification defect:
// a received but unusable state is not a failed proxy transport. Manual stops
// and actual network failures are untouched. The whole migration is locked.
func (s *Store) RecoverQualificationFailures() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	err := s.withDiskLocked(func() error {
		var ids []string
		for id, entry := range s.entries {
			if entry.State != "failed" {
				continue
			}
			switch entry.Reason {
			case "shape_mismatch", "missing_state_header", "invalid_state_envelope", "state_time_rejected":
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return nil
		}
		if err := s.changeLocked(ids, "available", "qualification_reclassified", false); err != nil {
			return err
		}
		count = len(ids)
		return nil
	})
	return count, err
}
