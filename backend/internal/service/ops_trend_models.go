package service

import "time"

type OpsThroughputTrendPoint struct {
	BucketStart         time.Time `json:"bucket_start"`
	RequestCount        int64     `json:"request_count"`
	SuccessCount        int64     `json:"success_count"`
	ErrorCount          int64     `json:"error_count"`
	TokenConsumed       int64     `json:"token_consumed"`
	InputTokens         int64     `json:"input_tokens"`
	OutputTokens        int64     `json:"output_tokens"`
	CacheCreationTokens int64     `json:"cache_creation_tokens"`
	CacheReadTokens     int64     `json:"cache_read_tokens"`
	SwitchCount         int64     `json:"switch_count"`
	UserBilledUSD       float64   `json:"user_billed_usd"`
	AccountCostUSD      float64   `json:"account_cost_usd"`
	ContributionUSD     float64   `json:"contribution_usd"`
	DurationP95Ms       *int      `json:"duration_p95_ms"`
	TTFTP50Ms           *int      `json:"ttft_p50_ms"`
	TTFTP95Ms           *int      `json:"ttft_p95_ms"`
	QPS                 float64   `json:"qps"`
	TPS                 float64   `json:"tps"`
}

type OpsThroughputPlatformBreakdownItem struct {
	Platform      string `json:"platform"`
	RequestCount  int64  `json:"request_count"`
	TokenConsumed int64  `json:"token_consumed"`
}

type OpsThroughputGroupBreakdownItem struct {
	GroupID       int64  `json:"group_id"`
	GroupName     string `json:"group_name"`
	RequestCount  int64  `json:"request_count"`
	TokenConsumed int64  `json:"token_consumed"`
}

type OpsThroughputTrendResponse struct {
	Bucket string `json:"bucket"`

	Points []*OpsThroughputTrendPoint `json:"points"`

	// Optional drilldown helpers:
	// - When no platform/group is selected: returns totals by platform.
	// - When platform is selected but group is not: returns top groups in that platform.
	ByPlatform []*OpsThroughputPlatformBreakdownItem `json:"by_platform,omitempty"`
	TopGroups  []*OpsThroughputGroupBreakdownItem    `json:"top_groups,omitempty"`
}

type OpsErrorTrendPoint struct {
	BucketStart time.Time `json:"bucket_start"`

	ErrorCountTotal      int64 `json:"error_count_total"`
	BusinessLimitedCount int64 `json:"business_limited_count"`
	ErrorCountSLA        int64 `json:"error_count_sla"`

	UpstreamErrorCountExcl429529 int64 `json:"upstream_error_count_excl_429_529"`
	Upstream402Count             int64 `json:"upstream_402_count"`
	Upstream429Count             int64 `json:"upstream_429_count"`
	Upstream529Count             int64 `json:"upstream_529_count"`
}

type OpsErrorTrendResponse struct {
	Bucket string                `json:"bucket"`
	Points []*OpsErrorTrendPoint `json:"points"`
}

type OpsErrorDistributionItem struct {
	StatusCode      int   `json:"status_code"`
	Total           int64 `json:"total"`
	SLA             int64 `json:"sla"`
	BusinessLimited int64 `json:"business_limited"`
}

type OpsErrorDistributionResponse struct {
	Total int64                       `json:"total"`
	Items []*OpsErrorDistributionItem `json:"items"`
}
