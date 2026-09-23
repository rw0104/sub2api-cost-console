package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// pluginRuntimeTimeline stores bounded lifecycle facts for one runtime. It is
// deliberately code-only: plugin or upstream error text must never become part
// of the runtime status payload or logs.
type pluginRuntimeTimeline struct {
	mu sync.RWMutex

	startedAt        time.Time
	apiReadyAt       time.Time
	hostAttachedAt   time.Time
	readinessAt      time.Time
	drainRequestedAt time.Time
	drainFinishedAt  time.Time
	exitedAt         time.Time
	lastErrorCode    string
	lastErrorAt      time.Time
}

type pluginRuntimeTimelineSnapshot struct {
	StartedAt        time.Time `json:"started_at,omitempty"`
	APIReadyAt       time.Time `json:"api_ready_at,omitempty"`
	HostAttachedAt   time.Time `json:"host_attached_at,omitempty"`
	ReadinessAt      time.Time `json:"readiness_at,omitempty"`
	DrainRequestedAt time.Time `json:"drain_requested_at,omitempty"`
	DrainFinishedAt  time.Time `json:"drain_finished_at,omitempty"`
	ExitedAt         time.Time `json:"exited_at,omitempty"`
	LastErrorCode    string    `json:"last_error_code,omitempty"`
	LastErrorAt      time.Time `json:"last_error_at,omitempty"`
}

func (s pluginRuntimeTimelineSnapshot) mapValue() map[string]any {
	result := make(map[string]any, 8)
	put := func(key string, value time.Time) {
		if !value.IsZero() {
			result[key] = value.UTC().Format(time.RFC3339Nano)
		}
	}
	put("started_at", s.StartedAt)
	put("api_ready_at", s.APIReadyAt)
	put("host_attached_at", s.HostAttachedAt)
	put("readiness_at", s.ReadinessAt)
	put("drain_requested_at", s.DrainRequestedAt)
	put("drain_finished_at", s.DrainFinishedAt)
	put("exited_at", s.ExitedAt)
	if s.LastErrorCode != "" {
		result["last_error_code"] = s.LastErrorCode
	}
	put("last_error_at", s.LastErrorAt)
	return result
}

func newPluginRuntimeTimeline(now time.Time) *pluginRuntimeTimeline {
	return &pluginRuntimeTimeline{startedAt: now.UTC()}
}

func (r *pluginRuntime) ensureRuntimeTimeline() *pluginRuntimeTimeline {
	if r == nil {
		return nil
	}
	r.timelineMu.Lock()
	defer r.timelineMu.Unlock()
	if r.timeline == nil {
		r.timeline = newPluginRuntimeTimeline(time.Now())
	}
	return r.timeline
}

func (r *pluginRuntime) markRuntimeAPIReady() {
	if timeline := r.ensureRuntimeTimeline(); timeline != nil {
		timeline.mu.Lock()
		timeline.apiReadyAt = time.Now().UTC()
		timeline.mu.Unlock()
	}
}

func (r *pluginRuntime) markRuntimeHostAttached() {
	if timeline := r.ensureRuntimeTimeline(); timeline != nil {
		timeline.mu.Lock()
		timeline.hostAttachedAt = time.Now().UTC()
		timeline.mu.Unlock()
	}
}

func (r *pluginRuntime) markRuntimeReadiness(err error) {
	if timeline := r.ensureRuntimeTimeline(); timeline != nil {
		timeline.mu.Lock()
		timeline.readinessAt = time.Now().UTC()
		timeline.mu.Unlock()
		if err != nil {
			r.markRuntimeError(err)
		}
	}
}

func (r *pluginRuntime) markRuntimeDrainRequested() {
	if timeline := r.ensureRuntimeTimeline(); timeline != nil {
		timeline.mu.Lock()
		timeline.drainRequestedAt = time.Now().UTC()
		timeline.mu.Unlock()
	}
}

func (r *pluginRuntime) markRuntimeDrainFinished() {
	if timeline := r.ensureRuntimeTimeline(); timeline != nil {
		timeline.mu.Lock()
		timeline.drainFinishedAt = time.Now().UTC()
		timeline.mu.Unlock()
	}
}

func (r *pluginRuntime) markRuntimeExited() {
	if timeline := r.ensureRuntimeTimeline(); timeline != nil {
		timeline.mu.Lock()
		timeline.exitedAt = time.Now().UTC()
		timeline.mu.Unlock()
	}
}

func (r *pluginRuntime) markRuntimeError(err error) {
	if err == nil {
		return
	}
	if timeline := r.ensureRuntimeTimeline(); timeline != nil {
		timeline.mu.Lock()
		timeline.lastErrorCode = pluginRuntimeErrorCode(err)
		timeline.lastErrorAt = time.Now().UTC()
		timeline.mu.Unlock()
	}
}

func (r *pluginRuntime) runtimeTimelineSnapshot() pluginRuntimeTimelineSnapshot {
	if r == nil {
		return pluginRuntimeTimelineSnapshot{}
	}
	timeline := r.ensureRuntimeTimeline()
	if timeline == nil {
		return pluginRuntimeTimelineSnapshot{}
	}
	timeline.mu.RLock()
	defer timeline.mu.RUnlock()
	return pluginRuntimeTimelineSnapshot{
		StartedAt:        timeline.startedAt,
		APIReadyAt:       timeline.apiReadyAt,
		HostAttachedAt:   timeline.hostAttachedAt,
		ReadinessAt:      timeline.readinessAt,
		DrainRequestedAt: timeline.drainRequestedAt,
		DrainFinishedAt:  timeline.drainFinishedAt,
		ExitedAt:         timeline.exitedAt,
		LastErrorCode:    timeline.lastErrorCode,
		LastErrorAt:      timeline.lastErrorAt,
	}
}

// pluginRuntimeErrorCode intentionally uses only stable classes and gRPC codes.
// Error messages from plugins, upstreams, and credentials are never retained.
func pluginRuntimeErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "TIMEOUT"
	}
	if errors.Is(err, context.Canceled) {
		return "CANCELED"
	}
	if code := status.Code(err); code != codes.Unknown {
		return "GRPC_" + strings.ToUpper(code.String())
	}
	var transportErr *PluginTransportError
	if errors.As(err, &transportErr) && transportErr != nil && strings.TrimSpace(transportErr.Code) != "" {
		return sanitizePluginRuntimeErrorCode(transportErr.Code)
	}
	return "RUNTIME_ERROR"
}

func sanitizePluginRuntimeErrorCode(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "RUNTIME_ERROR"
	}
	return b.String()
}
