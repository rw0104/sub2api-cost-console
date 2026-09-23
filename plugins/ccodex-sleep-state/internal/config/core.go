package config

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"local.sub2api/ccodex-sleep-state/internal/turnstate"
	"local.sub2api/ccodex-sleep-state/internal/upstream/settings"
)

// CoreOptions exposes the embedded gateway's behavior without importing the
// standalone application's credential or Codex configuration management.
type CoreOptions struct {
	ProbeRoundSeconds int    `json:"probe_round_seconds"`
	AccountMode       string `json:"account_mode"`
	StateRefreshMode  string `json:"state_refresh_mode"`
	MaxProbesPerRound int    `json:"max_probes_per_round"`
	EgressMode        string `json:"egress_mode"`
	EgressRoute       string `json:"egress_route,omitempty"`
	PoolEnabled       bool   `json:"pool_enabled"`
	ZstdWindowMiB     int    `json:"zstd_window_mib"`
	CompactLimitMiB   int    `json:"compact_limit_mib"`
}

type CoreOverrides struct {
	ProbeRoundSeconds int     `json:"probe_round_seconds,omitempty"`
	AccountMode       *string `json:"account_mode,omitempty"`
	StateRefreshMode  *string `json:"state_refresh_mode,omitempty"`
	MaxProbesPerRound int     `json:"max_probes_per_round,omitempty"`
	EgressMode        *string `json:"egress_mode,omitempty"`
	EgressRoute       *string `json:"egress_route,omitempty"`
	PoolEnabled       *bool   `json:"pool_enabled,omitempty"`
	ZstdWindowMiB     int     `json:"zstd_window_mib,omitempty"`
	CompactLimitMiB   int     `json:"compact_limit_mib,omitempty"`
}

func (c *Config) defaultCoreOptions(raw []byte) {
	if c.ProbeRoundSeconds == 0 {
		c.ProbeRoundSeconds = 20
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if c.AccountMode == "" {
		c.AccountMode = "auto"
		if c.StateTargetLength != DefaultStateTargetLength {
			c.AccountMode = "custom"
		}
	}
	if c.StateRefreshMode == "" {
		c.StateRefreshMode = "on_demand"
		if _, legacy := fields["state_ttl_seconds"]; legacy {
			c.StateRefreshMode = "standby"
		}
	}
	if c.MaxProbesPerRound == 0 {
		c.MaxProbesPerRound = 6
	}
	if c.EgressMode == "" {
		c.EgressMode = "state"
	}
	if c.ZstdWindowMiB == 0 {
		c.ZstdWindowMiB = 64
	}
	if c.CompactLimitMiB == 0 {
		c.CompactLimitMiB = 64
	}
}

func (c *Config) validateCoreOptions() error {
	if c.ProbeRoundSeconds < 1 || c.ProbeRoundSeconds > 60 {
		return errors.New("probe_round_seconds must be 1..60")
	}
	switch c.AccountMode {
	case "auto", "personal", "team", "custom":
	default:
		return errors.New("account_mode must be auto, personal, team or custom")
	}
	if c.StateRefreshMode != "on_demand" && c.StateRefreshMode != "standby" {
		return errors.New("state_refresh_mode must be on_demand or standby")
	}
	if c.MaxProbesPerRound < 1 || c.MaxProbesPerRound > 20 {
		return errors.New("max_probes_per_round must be 1..20")
	}
	if c.EgressMode != "state" && c.EgressMode != "random" && c.EgressMode != "fixed" {
		return errors.New("egress_mode must be state, random or fixed")
	}
	if c.EgressMode == "fixed" && !validRouteID(c.EgressRoute) {
		return errors.New("egress_route must identify a discovered node")
	}
	if c.ZstdWindowMiB < 16 || c.ZstdWindowMiB > 128 || c.CompactLimitMiB < 16 || c.CompactLimitMiB > 128 {
		return errors.New("zstd_window_mib and compact_limit_mib must be 16..128")
	}
	return nil
}

func (o CoreOptions) apply(v CoreOverrides) CoreOptions {
	if v.ProbeRoundSeconds != 0 {
		o.ProbeRoundSeconds = v.ProbeRoundSeconds
	}
	if v.AccountMode != nil {
		o.AccountMode = *v.AccountMode
	}
	if v.StateRefreshMode != nil {
		o.StateRefreshMode = *v.StateRefreshMode
	}
	if v.MaxProbesPerRound != 0 {
		o.MaxProbesPerRound = v.MaxProbesPerRound
	}
	if v.EgressMode != nil {
		o.EgressMode = *v.EgressMode
	}
	if v.EgressRoute != nil {
		o.EgressRoute = *v.EgressRoute
	}
	if v.PoolEnabled != nil {
		o.PoolEnabled = *v.PoolEnabled
	}
	if v.ZstdWindowMiB != 0 {
		o.ZstdWindowMiB = v.ZstdWindowMiB
	}
	if v.CompactLimitMiB != 0 {
		o.CompactLimitMiB = v.CompactLimitMiB
	}
	return o
}

func (c Config) CoreSettings(upstream string) settings.Config {
	result := settings.Default()
	result.Upstream = upstream
	if len(c.Models) > 0 {
		result.Model = c.Models[0]
	}
	result.ProbeRoundSeconds = c.ProbeRoundSeconds
	result.RoundRobin = c.RouteMode == "round_robin"
	result.InjectionDisabled = !c.Enabled || !c.InjectState
	result.HarvestDisabled = !c.HarvestOnDemand
	result.StateFallback = "passthrough"
	if c.FailClosed {
		result.StateFallback = "strict"
	}
	result.AccountMode = c.AccountMode
	result.BaselineBlocks = 10
	if c.AccountMode == "custom" {
		result.AccountMode = "auto"
		result.BaselineBlocks, _ = turnstate.BlocksForEncodedLength(c.StateTargetLength)
		// Upstream treats the default ten-block baseline as auto-detectable.
		// An explicitly selected custom 292 policy must remain ten blocks even
		// when the request contains a Team plan hint.
		if result.BaselineBlocks == 10 {
			result.AccountMode = "personal"
		}
	}
	result.StateRefreshMode = c.StateRefreshMode
	result.ProbeSeconds = c.ProbeTimeoutSeconds
	result.RefreshSeconds = c.RefreshBeforeSeconds
	result.CooldownSeconds = c.CooldownSeconds
	result.MaxProbes = c.MaxProbesPerRound
	result.TTLSeconds = c.StateTTLSeconds
	result.PinnedRoute = ""
	if c.RouteMode == "fixed" {
		result.PinnedRoute = c.FixedRouteID
	}
	result.EgressMode, result.EgressRoute, result.PoolEnabled = c.EgressMode, c.EgressRoute, c.PoolEnabled
	result.RequestLimitMiB = (c.MaxBodyBytes + (1 << 20) - 1) / (1 << 20)
	result.ZstdWindowMiB, result.CompactLimitMiB = c.ZstdWindowMiB, c.CompactLimitMiB
	return result
}

type CoreRequest struct {
	Operation string   `json:"operation"`
	SessionID string   `json:"session_id,omitempty"`
	RouteID   string   `json:"route_id,omitempty"`
	RouteIDs  []string `json:"route_ids,omitempty"`
	Action    string   `json:"action,omitempty"`
}

// Reports are snapshots for display. They never carry the token, credential,
// request headers, or a command that could run again after process restart.
type CoreReport struct {
	NodeVerifications []NodeVerification `json:"node_verifications,omitempty"`
	Schema            int                `json:"schema"`
	GeneratedAt       string             `json:"generated_at"`
	Engines           int                `json:"engines"`
	RequestsTotal     uint64             `json:"requests_total"`
	Sessions          []CoreSession      `json:"sessions"`
	Pool              []CorePoolNode     `json:"pool"`
	ErrorCode         string             `json:"error_code,omitempty"`
	Message           string             `json:"message,omitempty"`
}

type CoreSession struct {
	ID                string `json:"id"`
	AccountID         int64  `json:"account_id"`
	Model             string `json:"model"`
	Phase             string `json:"phase"`
	Usable            bool   `json:"usable"`
	Ready             bool   `json:"ready"`
	RemainingSeconds  int    `json:"remaining_seconds"`
	ExpectedLength    int    `json:"expected_length"`
	ObservedLength    int    `json:"observed_length"`
	CooldownSeconds   int    `json:"cooldown_seconds"`
	RejectedStatus    int    `json:"rejected_status"`
	RetryAfterSeconds int    `json:"retry_after_seconds"`
	Diagnostic        string `json:"diagnostic,omitempty"`
	DiagnosticMessage string `json:"diagnostic_message,omitempty"`
}

type CorePoolNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
	Reason   string `json:"reason,omitempty"`
}

func validateCoreRequest(request *CoreRequest) error {
	if request == nil {
		return nil
	}
	switch request.Operation {
	case "verify_nodes", "cancel_verification":
		if request.SessionID == "" || len(request.SessionID) > 160 || strings.ContainsAny(request.SessionID, "\r\n\t ") {
			return errors.New("node verification requires a live session")
		}
		if len(request.RouteIDs) > MaxRouteURLs {
			return errors.New("node verification supports at most 256 routes")
		}
		for _, id := range request.RouteIDs {
			if !validRouteID(id) {
				return errors.New("invalid node verification route")
			}
		}
	case "status":
	case "retry":
		if request.SessionID == "" || len(request.SessionID) > 160 || strings.ContainsAny(request.SessionID, "\r\n\t ") {
			return errors.New("core retry requires a live session ID")
		}
		if request.RouteID != "" && !validRouteID(request.RouteID) {
			return errors.New("invalid core retry route")
		}
	case "pool":
		if request.Action != "available" && request.Action != "disabled" {
			return errors.New("pool action must be available or disabled")
		}
		if len(request.RouteIDs) == 0 || len(request.RouteIDs) > MaxRouteURLs {
			return errors.New("pool action requires 1..256 route IDs")
		}
		for _, id := range request.RouteIDs {
			if !validRouteID(id) {
				return errors.New("invalid pool route ID")
			}
		}
	default:
		return errors.New("unsupported core operation")
	}
	return nil
}

func sanitizeCoreReport(report *CoreReport) *CoreReport {
	if report == nil || report.Schema != 1 {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, report.GeneratedAt); err != nil {
		return nil
	}
	if len(report.NodeVerifications) > 128 {
		return nil
	}
	for _, job := range report.NodeVerifications {
		if len(job.SessionID) > 160 || len(job.Rows) > MaxRouteURLs || len(job.Model) > 128 || len(job.Reason) > 256 {
			return nil
		}
		for _, row := range job.Rows {
			if !validRouteID(row.ID) || len(row.Name) > 400 || len(row.Reason) > 256 {
				return nil
			}
		}
	}
	if len(report.Sessions) > 1536 || len(report.Pool) > MaxRouteURLs || len(report.Message) > 1000 {
		return nil
	}
	for _, row := range report.Sessions {
		if len(row.ID) > 160 || len(row.Model) > 128 || len(row.DiagnosticMessage) > 1000 {
			return nil
		}
	}
	for _, row := range report.Pool {
		if !validRouteID(row.ID) || len(row.Name) > 400 || len(row.Reason) > 100 {
			return nil
		}
	}
	return report
}
