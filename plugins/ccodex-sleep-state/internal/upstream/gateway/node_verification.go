package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/upstream/turnstate"
)

// NodeVerificationRow describes a real, bounded Responses request. It is
// deliberately separate from a TCP/HTTP connectivity check. Shape is a local
// heuristic, not a cryptographic verification or a statement about model quality.
// Raw headers, credentials, proxy addresses and token fingerprints never appear.
type NodeVerificationRow struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Protocol       string    `json:"protocol"`
	Status         string    `json:"status"`
	HeaderPresent  bool      `json:"header_present"`
	Parsed         bool      `json:"parsed"`
	Qualified      bool      `json:"qualified"`
	ExpectedLength int       `json:"expected_length"`
	ObservedLength int       `json:"observed_length"`
	ExpectedBlocks int       `json:"expected_blocks"`
	ObservedBlocks int       `json:"observed_blocks"`
	HTTPStatus     int       `json:"http_status"`
	LatencyMS      int64     `json:"latency_ms"`
	CheckedAt      time.Time `json:"checked_at"`
	Reason         string    `json:"reason,omitempty"`
}

type NodeVerificationReport struct {
	SessionID   string                `json:"session_id"`
	Model       string                `json:"model"`
	State       string                `json:"state"`
	Total       int                   `json:"total"`
	Completed   int                   `json:"completed"`
	NextRunAt   time.Time             `json:"next_run_at"`
	GeneratedAt time.Time             `json:"generated_at"`
	Reason      string                `json:"reason,omitempty"`
	Rows        []NodeVerificationRow `json:"rows"`
}

type nodeVerification struct {
	report  NodeVerificationReport
	indices []int
	cancel  context.CancelFunc
	done    chan struct{}
}

type probeObservation struct {
	headerPresent bool
	parsed        bool
	length        int
	blocks        int
}
type probeObservationKey struct{}

// Called by probe before checking HTTP status or reading the stream. Even an
// invalid, missing, or incomplete response produces truthful safe metadata.
func observeProbeResponse(ctx context.Context, h http.Header) {
	observation, ok := ctx.Value(probeObservationKey{}).(*probeObservation)
	if !ok {
		return
	}
	value := h.Get(turnstate.Header)
	observation.headerPresent = len(h.Values(turnstate.Header)) > 0
	observation.length = len(value)
	if token, err := turnstate.Parse(value); err == nil {
		observation.parsed = true
		observation.blocks = token.Blocks
	}
}

// StartNodeVerification returns immediately. Only credentials already borrowed
// from a real request are used, in RAM. Explicit verification remains available
// with both automatic collection and state injection disabled.
func (e *Engine) StartNodeVerification(sessionID string, routeIDs []string) error {
	if e.config.IsRelay() {
		return errors.New("中转模式不执行官方请求头验证")
	}
	e.mu.Lock()
	var s *session
	for _, candidate := range e.sessions {
		if sessionID != "" && candidate.id == sessionID {
			s = candidate
			break
		}
	}
	if s == nil || e.closed.Load() {
		e.mu.Unlock()
		return errors.New("找不到运行中的会话；先通过插件发送一条普通 Codex 请求，再刷新会话")
	}
	if status, seconds := s.rejection(); status != 0 {
		e.mu.Unlock()
		return fmt.Errorf("上游 %d 暂停尚未解除（剩余 %d 秒）；请求头验证不会绕过该限制", status, seconds)
	}
	selected := make(map[string]bool, len(routeIDs))
	for _, id := range routeIDs {
		selected[id] = true
	}
	indices := make([]int, 0, len(e.routes))
	for i, route := range e.routes {
		if len(routeIDs) == 0 || selected[route.ID] {
			indices = append(indices, i)
			delete(selected, route.ID)
		}
	}
	if len(selected) > 0 || len(indices) == 0 {
		e.mu.Unlock()
		return errors.New("所选节点已不存在，请刷新节点和会话后重试")
	}
	e.verificationMu.Lock()
	if e.verificationStopped {
		e.verificationMu.Unlock()
		e.mu.Unlock()
		return errors.New("运行实例已停止；请发送新请求建立会话")
	}
	if e.verifications == nil {
		e.verifications = make(map[string]*nodeVerification)
	}
	// Keep reports bounded by retained sessions. An active task pins its
	// session, so only finished reports can be removed here.
	activeSessions := make(map[string]bool, len(e.sessions))
	for _, current := range e.sessions {
		activeSessions[current.id] = true
	}
	for id, prior := range e.verifications {
		if !activeSessions[id] {
			select {
			case <-prior.done:
				delete(e.verifications, id)
			default:
			}
		}
	}
	old := e.verifications[sessionID]
	if old != nil {
		select {
		case <-old.done:
		default:
			e.verificationMu.Unlock()
			e.mu.Unlock()
			return errors.New("该会话已有请求头验证任务；可等待、刷新进度或取消任务")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	job := &nodeVerification{cancel: cancel, done: make(chan struct{}), report: NodeVerificationReport{
		SessionID: sessionID, Model: s.model, State: "pending", GeneratedAt: now,
		Rows: make([]NodeVerificationRow, len(indices)), Total: len(indices),
	}}
	// Resume the remaining routes after a pause/cancellation. An explicit row
	// selection intentionally starts a fresh check of that selected route.
	previous := make(map[string]NodeVerificationRow)
	if len(routeIDs) == 0 && old != nil && (old.report.State == "paused" || old.report.State == "cancelled") {
		for _, row := range old.report.Rows {
			switch row.Status {
			case "pending", "running", "cancelled", "upstream_rejected", "upstream_rate_limited":
			default:
				previous[row.ID] = row
			}
		}
	}
	for position, index := range indices {
		route := e.routes[index]
		row := NodeVerificationRow{ID: route.ID, Name: route.DisplayName, Protocol: route.Protocol, Status: "pending", ExpectedLength: s.policy.Length, ExpectedBlocks: s.policy.Blocks}
		if row.Name == "" {
			row.Name = route.ID
		}
		if prior, ok := previous[route.ID]; ok {
			row = prior
			job.report.Completed++
		} else {
			job.indices = append(job.indices, position)
		}
		job.report.Rows[position] = row
	}
	s.mu.Lock()
	s.busy++
	s.lastUsed = now
	s.mu.Unlock()
	e.verifications[sessionID] = job
	e.verificationMu.Unlock()
	e.mu.Unlock()
	go func() {
		defer close(job.done)
		defer release(s)
		defer cancel()
		e.runNodeVerification(ctx, s, job, indices)
	}()
	return nil
}

func (e *Engine) NodeVerifications() []NodeVerificationReport {
	e.verificationMu.Lock()
	defer e.verificationMu.Unlock()
	reports := make([]NodeVerificationReport, 0, len(e.verifications))
	for _, job := range e.verifications {
		report := job.report
		report.Rows = append([]NodeVerificationRow(nil), report.Rows...)
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].SessionID < reports[j].SessionID })
	return reports
}

func (e *Engine) CancelNodeVerification(sessionID string) error {
	e.verificationMu.Lock()
	defer e.verificationMu.Unlock()
	job := e.verifications[sessionID]
	if job == nil {
		return errors.New("该会话没有请求头验证任务")
	}
	select {
	case <-job.done:
		return nil
	default:
	}
	e.cancelNodeVerificationLocked(job, "user_cancelled")
	return nil
}

func (e *Engine) cancelNodeVerificationLocked(job *nodeVerification, reason string) {
	job.cancel()
	if job.report.State == "completed" || job.report.State == "paused" {
		return
	}
	job.report.State, job.report.Reason = "cancelled", reason
	job.report.GeneratedAt, job.report.NextRunAt = time.Now(), time.Time{}
	for i := range job.report.Rows {
		row := &job.report.Rows[i]
		if row.Status == "running" || row.Status == "pending" {
			row.Status, row.Reason = "cancelled", reason
		}
	}
}

func (e *Engine) stopNodeVerifications(reason string) {
	e.verificationMu.Lock()
	e.verificationStopped = true
	wait := make([]<-chan struct{}, 0, len(e.verifications))
	for _, job := range e.verifications {
		select {
		case <-job.done:
		default:
			e.cancelNodeVerificationLocked(job, reason)
			wait = append(wait, job.done)
		}
	}
	e.verificationMu.Unlock()
	for _, done := range wait {
		<-done
	}
}

func (e *Engine) verificationState(job *nodeVerification, state, reason string, next time.Time) {
	e.verificationMu.Lock()
	defer e.verificationMu.Unlock()
	if job.report.State == "cancelled" {
		return
	}
	job.report.State, job.report.Reason, job.report.NextRunAt = state, reason, next
	job.report.GeneratedAt = time.Now()
}

func verificationWait(ctx context.Context, until time.Time) bool {
	if delay := time.Until(until); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return false
		}
	}
	return ctx.Err() == nil
}

func (e *Engine) runNodeVerification(ctx context.Context, s *session, job *nodeVerification, routes []int) {
	position := 0
	for position < len(job.indices) {
		if ctx.Err() != nil {
			return
		}
		if status, _ := s.rejection(); status != 0 {
			e.verificationState(job, "paused", "upstream_rejected", time.Time{})
			return
		}
		s.mu.Lock()
		next, pending := s.nextProbe, s.probing
		s.mu.Unlock()
		if time.Now().Before(next) {
			e.verificationState(job, "cooling_down", "probe_cooldown", next)
			if !verificationWait(ctx, next) {
				return
			}
			continue
		}
		if pending != nil {
			e.verificationState(job, "pending", "waiting_for_probe", time.Time{})
			select {
			case <-pending:
			case <-ctx.Done():
				return
			}
			continue
		}
		select {
		case e.probeSlot <- struct{}{}:
		case <-ctx.Done():
			return
		}
		// Recheck after acquiring the process-global slot: another refresh may
		// have installed a cooldown or session claim while this task waited.
		s.mu.Lock()
		if s.probing != nil || time.Now().Before(s.nextProbe) {
			s.mu.Unlock()
			<-e.probeSlot
			continue
		}
		roundDone := make(chan struct{})
		s.probing = roundDone
		s.mu.Unlock()
		e.verificationState(job, "running", "", time.Time{})
		attempted, pause := 0, false
		for position < len(job.indices) && attempted < max(1, e.config.MaxProbes) {
			if ctx.Err() != nil {
				break
			}
			if status, _ := s.rejection(); status != 0 {
				pause = true
				break
			}
			rowIndex := job.indices[position]
			routeIndex := routes[rowIndex]
			if e.pool.Get(e.routes[routeIndex].ID).State == "disabled" {
				e.verificationRowSkipped(job, rowIndex)
				position++
				continue
			}
			attempted++
			pause = e.verifyNode(ctx, s, job, rowIndex, routeIndex)
			position++
			if pause {
				break
			}
		}
		s.mu.Lock()
		if attempted > 0 {
			cooldown := time.Now().Add(time.Duration(e.config.CooldownSeconds) * time.Second)
			if cooldown.After(s.nextProbe) {
				s.nextProbe = cooldown
			}
		}
		s.probing = nil
		close(roundDone)
		s.mu.Unlock()
		<-e.probeSlot
		if ctx.Err() != nil {
			return
		}
		if pause {
			e.verificationState(job, "paused", "upstream_rejected", time.Time{})
			return
		}
	}
	e.verificationState(job, "completed", "", time.Time{})
}

func (e *Engine) verificationRowSkipped(job *nodeVerification, rowIndex int) {
	e.verificationMu.Lock()
	defer e.verificationMu.Unlock()
	if job.report.State == "cancelled" {
		return
	}
	job.report.Rows[rowIndex].Status = "skipped"
	job.report.Rows[rowIndex].Reason = "node_disabled"
	job.report.Completed++
	job.report.GeneratedAt = time.Now()
}

func (e *Engine) verifyNode(ctx context.Context, s *session, job *nodeVerification, rowIndex, routeIndex int) bool {
	e.verificationMu.Lock()
	row := job.report.Rows[rowIndex]
	row.Status = "running"
	job.report.Rows[rowIndex] = row
	e.verificationMu.Unlock()
	observation := &probeObservation{}
	probeCtx := context.WithValue(ctx, probeObservationKey{}, observation)
	started := time.Now()
	token, status, retryAfter, err := e.probe(probeCtx, s.headers, e.routes[routeIndex], s.model)
	row.HTTPStatus, row.LatencyMS, row.CheckedAt = status, time.Since(started).Milliseconds(), time.Now()
	row.HeaderPresent, row.Parsed = observation.headerPresent, observation.parsed
	row.ObservedLength, row.ObservedBlocks = observation.length, observation.blocks
	row.Status = probeReason(err)
	if err == nil {
		policy := turnstate.Policy{Blocks: s.policy.Blocks, TTL: time.Duration(e.config.TTLSeconds) * time.Second}
		switch {
		case token.Blocks != s.policy.Blocks:
			row.Status = "shape_mismatch"
		case !policy.Accept(token, row.CheckedAt):
			row.Status = "state_time_rejected"
		default:
			row.Status, row.Qualified = "accepted", true
			// Verification is diagnostic only. It never replaces active/standby
			// state, changes the user's selected route, or persists the token.
		}
	}
	if ctx.Err() != nil {
		row.Status, row.Qualified = "cancelled", false
	}
	row.Reason = row.Status
	rejectionStatus := status
	var streamFailure *probeStreamError
	if errors.As(err, &streamFailure) && streamFailure.status != 0 {
		rejectionStatus = streamFailure.status
	}
	rejected := e.reject(s, rejectionStatus, retryAfter, routeIndex)
	e.verificationMu.Lock()
	if job.report.State == "cancelled" {
		row.Status, row.Qualified, row.Reason = "cancelled", false, job.report.Reason
		job.report.Rows[rowIndex] = row
		job.report.GeneratedAt = row.CheckedAt
	} else {
		job.report.Rows[rowIndex] = row
		job.report.Completed++
		job.report.GeneratedAt = row.CheckedAt
	}
	e.verificationMu.Unlock()
	// A definitive upstream error event stops the batch as well; it must not
	// turn into automatic exit rotation just because its outer HTTP was 200.
	return rejected || streamFailure != nil
}
