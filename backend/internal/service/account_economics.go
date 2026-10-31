package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	EconomicsProjectionVersion = "1.2.0"
	minimumEconomicsInterval   = 5 * time.Second
	// maximumEconomicsInterval excludes adjacent samples that are too far apart.
	// The background sampler runs every minute, so a longer gap usually means the
	// core was not running; counting it would spread its zero production over the
	// whole gap and understate the rate.
	maximumEconomicsInterval       = 30 * time.Minute
	minimumEconomicsValidIntervals = 2
	// minimumForecastCoverage is the stable coverage required before rates are
	// extrapolated into capacity-adjusted forecasts and payback estimates.
	minimumForecastCoverage = 15 * time.Minute
	// economicsProjectionMinimumLookback is the shortest sample history used by
	// the projection, independent of the selected display range.
	economicsProjectionMinimumLookback = time.Hour
	// maximumCapacityAdjustment bounds the extrapolation from the observed
	// average healthy capacity to the current healthy capacity.
	maximumCapacityAdjustment = 2.0
)

// AccountEconomicsSample is one immutable observation of cumulative, factual
// usage totals for a stable account-pool membership version.
type AccountEconomicsSample struct {
	SampledAt           time.Time `json:"sampled_at"`
	ScopeKey            string    `json:"scope_key"`
	MembershipHash      string    `json:"membership_hash"`
	AccountCount        int       `json:"account_count"`
	NormalCount         int       `json:"normal_count"`
	RateLimitedCount    int       `json:"rate_limited_count"`
	ErrorCount          int       `json:"error_count"`
	BilledUSDTotal      float64   `json:"billed_usd_total"`
	AccountCostUSDTotal float64   `json:"account_cost_usd_total"`
}

// AccountEconomicsProjection contains rates only when enough stable evidence
// exists. Pointer values intentionally serialize as null instead of a fake zero.
type AccountEconomicsProjection struct {
	Version                               string   `json:"version"`
	Confidence                            string   `json:"confidence"`
	Warning                               string   `json:"warning,omitempty"`
	ValidIntervals                        int      `json:"valid_intervals"`
	ResetIntervals                        int      `json:"reset_intervals"`
	GapIntervals                          int      `json:"gap_intervals"`
	CoverageHours                         float64  `json:"coverage_hours"`
	BilledUSDPerHour                      *float64 `json:"billed_usd_per_hour"`
	AccountCostUSDPerHour                 *float64 `json:"account_cost_usd_per_hour"`
	CapacityAdjustedBilledUSDPerHour      *float64 `json:"capacity_adjusted_billed_usd_per_hour"`
	CapacityAdjustedAccountCostUSDPerHour *float64 `json:"capacity_adjusted_account_cost_usd_per_hour"`
	CapacityAdjustment                    float64  `json:"capacity_adjustment"`
	HealthyCapacityRatio                  float64  `json:"healthy_capacity_ratio"`
}

type AccountPoolUnitEconomicsInput struct {
	BilledUSD             float64
	AccountCostUSD        float64
	ProcurementAccruedCNY float64
	ImpairmentLossCNY     float64
	ProcurementHourlyCNY  float64
	CNYPerUSD             float64
	BilledUSDPerHour      *float64
	AccountCostUSDPerHour *float64
}

type AccountPoolUnitEconomics struct {
	BilledUSD                           float64  `json:"billed_usd"`
	AccountCostUSD                      float64  `json:"account_cost_usd"`
	ProcurementAccruedCNY               float64  `json:"procurement_accrued_cny"`
	ImpairmentLossCNY                   float64  `json:"impairment_loss_cny"`
	EconomicCostCNY                     float64  `json:"economic_cost_cny"`
	WindowProcurementCNY                float64  `json:"window_procurement_cny"`
	WindowImpairmentLossCNY             float64  `json:"window_impairment_loss_cny"`
	WindowEconomicCostCNY               float64  `json:"window_economic_cost_cny"`
	ContributionMarginCNY               float64  `json:"contribution_margin_cny"`
	CNYPerBilledUSD                     *float64 `json:"cny_per_billed_usd"`
	PaybackRatio                        *float64 `json:"payback_ratio"`
	ProjectedContributionCNYPerHour     *float64 `json:"projected_contribution_cny_per_hour"`
	EstimatedPaybackHours               *float64 `json:"estimated_payback_hours"`
	MonthOneTimeProcurementCNY          float64  `json:"month_one_time_procurement_cny"`
	MonthOneTimePurchaseCount           int      `json:"month_one_time_purchase_count"`
	MonthDeletedOneTimePurchaseCount    int      `json:"month_deleted_one_time_purchase_count"`
	MonthDeletedRecurringProcurementCNY float64  `json:"month_deleted_recurring_procurement_cny"`
	MonthDeletedRecurringPurchaseCount  int      `json:"month_deleted_recurring_purchase_count"`
}

type AccountEconomicsUsageTotals struct {
	BilledUSD      float64
	AccountCostUSD float64
}

type AccountProcurementProfile struct {
	AccountID   int64
	Platform    string
	Deleted     bool
	DeletedAt   *time.Time
	CostProfile AccountCostProfileSnapshot
	// Invalid marks a stored cost_profile that cannot be parsed or normalized.
	// It is reported through data quality instead of failing the whole snapshot.
	Invalid bool
}

type AccountEconomicsRepository interface {
	SumUsageTotals(ctx context.Context, accountIDs []int64) (AccountEconomicsUsageTotals, error)
	ListProcurementProfiles(ctx context.Context) ([]AccountProcurementProfile, error)
	UpsertSample(ctx context.Context, sample AccountEconomicsSample) error
	ListSamples(ctx context.Context, scopeKey string, since, until time.Time) ([]AccountEconomicsSample, error)
	PruneSamples(ctx context.Context, before time.Time) error
}

type AccountEconomicsAccountReader interface {
	ListAllWithFilters(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, error)
}

type AccountEconomicsScope struct {
	Platform string `json:"platform,omitempty"`
}

type AccountEconomicsHealth struct {
	AccountCount     int     `json:"account_count"`
	NormalCount      int     `json:"normal_count"`
	RateLimitedCount int     `json:"rate_limited_count"`
	ErrorCount       int     `json:"error_count"`
	HealthyRatio     float64 `json:"healthy_ratio"`
	MembershipHash   string  `json:"membership_hash"`
}

type AccountEconomicsDataQuality struct {
	Status                  string `json:"status"`
	SampleCount             int    `json:"sample_count"`
	InvalidCostProfileCount int    `json:"invalid_cost_profile_count"`
	ExchangeRateSource      string `json:"exchange_rate_source"`
}

// AccountEconomicsSeriesPoint is a factual health observation plus rates
// derived from one or more stable adjacent cumulative samples. Null rates mean
// the interval was reset or did not contain enough evidence; they are rendered
// as chart gaps rather than zero production.
type AccountEconomicsSeriesPoint struct {
	SampledAt             time.Time `json:"sampled_at"`
	NormalCount           int       `json:"normal_count"`
	RateLimitedCount      int       `json:"rate_limited_count"`
	ErrorCount            int       `json:"error_count"`
	BilledUSDPerHour      *float64  `json:"billed_usd_per_hour"`
	AccountCostUSDPerHour *float64  `json:"account_cost_usd_per_hour"`
	Stable                bool      `json:"stable"`
}

type AccountEconomicsEvent struct {
	OccurredAt time.Time `json:"occurred_at"`
	Kind       string    `json:"kind"`
	Label      string    `json:"label"`
	Severity   string    `json:"severity"`
}

type AccountEconomicsSnapshot struct {
	AlgorithmVersion  string                        `json:"algorithm_version"`
	ProjectionVersion string                        `json:"projection_version"`
	SampledAt         time.Time                     `json:"sampled_at"`
	Scope             AccountEconomicsScope         `json:"scope"`
	CNYPerUSD         float64                       `json:"cny_per_usd"`
	Health            AccountEconomicsHealth        `json:"health"`
	Actual            AccountPoolUnitEconomics      `json:"actual"`
	Projection        AccountEconomicsProjection    `json:"projection"`
	DataQuality       AccountEconomicsDataQuality   `json:"data_quality"`
	Series            []AccountEconomicsSeriesPoint `json:"series"`
	Events            []AccountEconomicsEvent       `json:"events"`
	WindowStart       time.Time                     `json:"window_start"`
	WindowEnd         time.Time                     `json:"window_end"`
	Timezone          string                        `json:"timezone"`
}

type AccountEconomicsQuery struct {
	Scope              string
	Platform           string
	AccountIDs         []int64
	CNYPerUSD          float64
	ExchangeRateSource string
	Window             time.Duration
	Now                time.Time
	StartTime          time.Time
	EndTime            time.Time
	Timezone           string
}

type AccountEconomicsService struct {
	accounts  AccountEconomicsAccountReader
	repo      AccountEconomicsRepository
	losses    *AccountCostLossService
	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	// done is closed when the background sampler goroutine has returned.
	done      chan struct{}
	startedAt time.Time
}

func NewAccountEconomicsService(accounts AccountEconomicsAccountReader, repo AccountEconomicsRepository, losses *AccountCostLossService) *AccountEconomicsService {
	return &AccountEconomicsService{
		accounts: accounts, repo: repo, losses: losses, startedAt: time.Now().UTC(),
		stopCh: make(chan struct{}), done: make(chan struct{}),
	}
}

func (s *AccountEconomicsService) Start() {
	if s == nil || s.accounts == nil || s.repo == nil {
		return
	}
	s.startOnce.Do(func() {
		go func() {
			defer close(s.done)
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			iteration := 0
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if _, err := s.recordSample(ctx, "all", "", nil, time.Now()); err != nil {
					slog.Warn("account economics sampling failed", "error", err)
				}
				if iteration%60 == 0 {
					if err := s.repo.PruneSamples(ctx, time.Now().Add(-90*24*time.Hour)); err != nil {
						slog.Warn("account economics sample pruning failed", "error", err)
					}
				}
				cancel()
				iteration++
				select {
				case <-s.stopCh:
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

// Stop ends the background sampler so shutdown does not sample against a
// database connection that the cleanup sequence is about to close.
func (s *AccountEconomicsService) Stop() {
	if s == nil || s.stopCh == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *AccountEconomicsService) GetSnapshot(ctx context.Context, query AccountEconomicsQuery) (*AccountEconomicsSnapshot, error) {
	if s == nil || s.accounts == nil || s.repo == nil || s.losses == nil {
		return nil, errors.New("account economics module is unavailable")
	}
	now := query.Now
	if now.IsZero() {
		now = time.Now()
	}
	windowStart, windowEnd, location, err := ResolveEconomicsWindow(query, now)
	if err != nil {
		return nil, err
	}
	window := windowEnd.Sub(windowStart)
	cnyPerUSD := query.CNYPerUSD
	quality := AccountEconomicsDataQuality{Status: "complete", ExchangeRateSource: strings.TrimSpace(query.ExchangeRateSource)}
	if cnyPerUSD <= 0 || math.IsNaN(cnyPerUSD) || math.IsInf(cnyPerUSD, 0) {
		cnyPerUSD = 7.2
		quality.Status = "partial"
		quality.ExchangeRateSource = "server_fallback"
	}
	if quality.ExchangeRateSource == "" {
		quality.ExchangeRateSource = "client"
	}
	if strings.EqualFold(quality.ExchangeRateSource, "fallback") {
		// The client could not reach the reference-rate source or its cache and
		// used the built-in rate. CNY results are estimates, not complete facts.
		quality.Status = "partial"
	}

	current, err := s.recordSample(ctx, query.Scope, query.Platform, query.AccountIDs, now)
	if err != nil {
		return nil, err
	}
	// Short display ranges (1m, 5m, the first minutes of today) can never hold
	// the stable coverage a forecast needs, so the projection looks back at
	// least economicsProjectionMinimumLookback. The series and sample count stay
	// limited to the requested window.
	projectionStart := windowStart
	if lookbackStart := windowEnd.Add(-economicsProjectionMinimumLookback); lookbackStart.Before(projectionStart) {
		projectionStart = lookbackStart
	}
	projectionSamples, err := s.repo.ListSamples(ctx, current.ScopeKey, projectionStart, windowEnd)
	if err != nil {
		return nil, fmt.Errorf("list economics samples: %w", err)
	}
	samples := make([]AccountEconomicsSample, 0, len(projectionSamples))
	for _, sample := range projectionSamples {
		if !sample.SampledAt.Before(windowStart) {
			samples = append(samples, sample)
		}
	}
	quality.SampleCount = len(samples)
	projection := BuildAccountEconomicsProjection(projectionSamples)
	accounts, err := s.loadScopeAccounts(ctx, query.Platform, query.AccountIDs)
	if err != nil {
		return nil, err
	}
	states, err := s.losses.ListStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list account cost loss states: %w", err)
	}
	includeDeleted := includeArchivedEconomics(query.Scope, query.AccountIDs)
	procurementProfiles, err := s.repo.ListProcurementProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list account procurement profiles: %w", err)
	}
	monthOneTimeProcurementCNY, monthOneTimePurchaseCount, monthDeletedOneTimePurchaseCount, monthDeletedRecurringProcurementCNY, monthDeletedRecurringPurchaseCount := summarizeMonthlyProcurementProfiles(
		procurementProfiles, states, query.Platform, query.AccountIDs, cnyPerUSD, now.In(location), includeDeleted,
	)
	windowProcurementCNY, windowImpairmentCNY := summarizeWindowEconomics(
		accounts, procurementProfiles, states, query.Platform, query.AccountIDs,
		cnyPerUSD, windowStart, windowEnd, includeDeleted,
	)
	procurement, impairment, hourly, invalid := summarizeProcurementEconomics(accounts, states, query.Platform, cnyPerUSD, now, includeDeleted)
	// Live accounts are validated above; deleted ones only exist in stored profiles.
	invalid += countInvalidDeletedProcurementProfiles(procurementProfiles, query.Platform, query.AccountIDs, includeDeleted)
	quality.InvalidCostProfileCount = invalid
	if invalid > 0 {
		quality.Status = "partial"
	}
	unitTotals := AccountEconomicsUsageTotals{BilledUSD: current.BilledUSDTotal, AccountCostUSD: current.AccountCostUSDTotal}
	if includeDeleted {
		unitTotals, err = s.repo.SumUsageTotals(ctx, economicsUsageAccountIDs(accounts, states, query.Platform))
		if err != nil {
			return nil, fmt.Errorf("sum account unit economics usage: %w", err)
		}
	}
	actual := BuildAccountPoolUnitEconomics(AccountPoolUnitEconomicsInput{
		BilledUSD:             unitTotals.BilledUSD,
		AccountCostUSD:        unitTotals.AccountCostUSD,
		ProcurementAccruedCNY: procurement,
		ImpairmentLossCNY:     impairment,
		ProcurementHourlyCNY:  hourly,
		CNYPerUSD:             cnyPerUSD,
		BilledUSDPerHour:      projection.CapacityAdjustedBilledUSDPerHour,
		AccountCostUSDPerHour: projection.CapacityAdjustedAccountCostUSDPerHour,
	})
	actual.MonthOneTimeProcurementCNY = monthOneTimeProcurementCNY
	actual.MonthOneTimePurchaseCount = monthOneTimePurchaseCount
	actual.MonthDeletedOneTimePurchaseCount = monthDeletedOneTimePurchaseCount
	actual.MonthDeletedRecurringProcurementCNY = monthDeletedRecurringProcurementCNY
	actual.MonthDeletedRecurringPurchaseCount = monthDeletedRecurringPurchaseCount
	actual.WindowProcurementCNY = windowProcurementCNY
	actual.WindowImpairmentLossCNY = windowImpairmentCNY
	actual.WindowEconomicCostCNY = windowProcurementCNY + windowImpairmentCNY
	series, events := BuildAccountEconomicsSeries(samples, window)
	if !s.startedAt.Before(windowStart) && !s.startedAt.After(windowEnd) {
		events = append(events, AccountEconomicsEvent{
			OccurredAt: s.startedAt, Kind: "core_started", Label: "核心进程启动", Severity: "info",
		})
	}
	for _, state := range states {
		if !state.Active || state.OccurredAt.Before(windowStart) || state.OccurredAt.After(windowEnd) {
			continue
		}
		events = append(events, AccountEconomicsEvent{
			OccurredAt: state.OccurredAt, Kind: "impairment_confirmed", Label: "封禁损失已核销", Severity: "warning",
		})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	healthyRatio := 0.0
	if current.AccountCount > 0 {
		healthyRatio = float64(current.NormalCount) / float64(current.AccountCount)
	}
	return &AccountEconomicsSnapshot{
		AlgorithmVersion:  CostAlgorithmVersion,
		ProjectionVersion: EconomicsProjectionVersion,
		SampledAt:         current.SampledAt,
		Scope:             AccountEconomicsScope{Platform: normalizeEconomicsPlatform(query.Platform)},
		CNYPerUSD:         cnyPerUSD,
		Health: AccountEconomicsHealth{
			AccountCount: current.AccountCount, NormalCount: current.NormalCount,
			RateLimitedCount: current.RateLimitedCount, ErrorCount: current.ErrorCount,
			HealthyRatio: healthyRatio, MembershipHash: current.MembershipHash,
		},
		Actual: actual, Projection: projection, DataQuality: quality, Series: series, Events: events,
		WindowStart: windowStart, WindowEnd: windowEnd, Timezone: location.String(),
	}, nil
}

// BuildAccountEconomicsSeries downsamples persistent factual samples into an
// adaptive, bounded series. Reset intervals keep their null values so clients
// can show a break and an explanatory event marker.
func BuildAccountEconomicsSeries(samples []AccountEconomicsSample, window time.Duration) ([]AccountEconomicsSeriesPoint, []AccountEconomicsEvent) {
	if len(samples) == 0 {
		return []AccountEconomicsSeriesPoint{}, []AccountEconomicsEvent{}
	}
	ordered := append([]AccountEconomicsSample(nil), samples...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].SampledAt.Before(ordered[j].SampledAt) })
	bucket := economicsSeriesBucket(window)
	type aggregate struct {
		point                      AccountEconomicsSeriesPoint
		billed, accountCost, hours float64
	}
	buckets := make(map[int64]*aggregate)
	order := make([]int64, 0, len(ordered))
	events := make([]AccountEconomicsEvent, 0)
	for index := 1; index < len(ordered); index++ {
		previous, current := ordered[index-1], ordered[index]
		duration := current.SampledAt.Sub(previous.SampledAt)
		key := current.SampledAt.Unix() / int64(bucket/time.Second)
		item, exists := buckets[key]
		if !exists {
			item = &aggregate{point: AccountEconomicsSeriesPoint{SampledAt: current.SampledAt, NormalCount: current.NormalCount, RateLimitedCount: current.RateLimitedCount, ErrorCount: current.ErrorCount}}
			buckets[key] = item
			order = append(order, key)
		} else {
			item.point.SampledAt = current.SampledAt
			item.point.NormalCount = current.NormalCount
			item.point.RateLimitedCount = current.RateLimitedCount
			item.point.ErrorCount = current.ErrorCount
		}
		stableMembership := strings.TrimSpace(previous.MembershipHash) != "" && previous.MembershipHash == current.MembershipHash && previous.AccountCount == current.AccountCount
		billed := current.BilledUSDTotal - previous.BilledUSDTotal
		accountCost := current.AccountCostUSDTotal - previous.AccountCostUSDTotal
		if !stableMembership {
			events = append(events, AccountEconomicsEvent{OccurredAt: current.SampledAt, Kind: "pool_membership_changed", Label: "账号池成员变化", Severity: "warning"})
			continue
		}
		if billed < -1e-9 || accountCost < -1e-9 {
			events = append(events, AccountEconomicsEvent{OccurredAt: current.SampledAt, Kind: "counter_regression", Label: "累计值倒退，预测基线已重置", Severity: "warning"})
			continue
		}
		if duration < minimumEconomicsInterval || duration > 3*bucket {
			if duration > 3*bucket {
				events = append(events, AccountEconomicsEvent{OccurredAt: current.SampledAt, Kind: "sampling_gap", Label: "采样中断", Severity: "warning"})
			}
			continue
		}
		item.billed += billed
		item.accountCost += accountCost
		item.hours += duration.Hours()
	}
	result := make([]AccountEconomicsSeriesPoint, 0, len(order))
	for _, key := range order {
		item := buckets[key]
		if item.hours > 0 {
			billedRate := item.billed / item.hours
			accountRate := item.accountCost / item.hours
			item.point.BilledUSDPerHour = &billedRate
			item.point.AccountCostUSDPerHour = &accountRate
			item.point.Stable = true
		}
		result = append(result, item.point)
	}
	return result, events
}

func economicsSeriesBucket(window time.Duration) time.Duration {
	switch {
	case window <= time.Minute:
		return 5 * time.Second
	case window <= 5*time.Minute:
		return 15 * time.Second
	case window <= time.Hour:
		return time.Minute
	case window <= 6*time.Hour:
		return 5 * time.Minute
	case window <= 24*time.Hour:
		return 15 * time.Minute
	case window <= 7*24*time.Hour:
		return 2 * time.Hour
	default:
		return 6 * time.Hour
	}
}

func includeArchivedEconomics(scope string, accountIDs []int64) bool {
	if len(accountIDs) == 0 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "codex", "anthropic", "gemini", "antigravity", "grok":
		return true
	default:
		return false
	}
}

func economicsUsageAccountIDs(accounts []Account, states []AccountCostLossState, platform string) []int64 {
	ids := make(map[int64]struct{}, len(accounts)+len(states))
	for index := range accounts {
		if accounts[index].ID > 0 {
			ids[accounts[index].ID] = struct{}{}
		}
	}
	platform = normalizeEconomicsPlatform(platform)
	for _, state := range states {
		if !state.Active || state.AccountIDSnapshot <= 0 || (platform != "" && !strings.EqualFold(state.Platform, platform)) {
			continue
		}
		ids[state.AccountIDSnapshot] = struct{}{}
	}
	result := make([]int64, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (s *AccountEconomicsService) recordSample(ctx context.Context, scope, platform string, accountIDs []int64, now time.Time) (AccountEconomicsSample, error) {
	accounts, err := s.loadScopeAccounts(ctx, platform, accountIDs)
	if err != nil {
		return AccountEconomicsSample{}, err
	}
	ids := make([]int64, 0, len(accounts))
	for index := range accounts {
		ids = append(ids, accounts[index].ID)
	}
	totals, err := s.repo.SumUsageTotals(ctx, ids)
	if err != nil {
		return AccountEconomicsSample{}, fmt.Errorf("sum account economics usage: %w", err)
	}
	normal, limited, failed := summarizeAccountEconomicsHealth(accounts, now)
	sample := AccountEconomicsSample{
		SampledAt: now.UTC(), ScopeKey: economicsScopeKey(scope, platform), MembershipHash: economicsMembershipHash(ids),
		AccountCount: len(accounts), NormalCount: normal, RateLimitedCount: limited, ErrorCount: failed,
		BilledUSDTotal: totals.BilledUSD, AccountCostUSDTotal: totals.AccountCostUSD,
	}
	if err := s.repo.UpsertSample(ctx, sample); err != nil {
		return AccountEconomicsSample{}, fmt.Errorf("store account economics sample: %w", err)
	}
	return sample, nil
}

func (s *AccountEconomicsService) loadScopeAccounts(ctx context.Context, platform string, accountIDs []int64) ([]Account, error) {
	databasePlatform := normalizeEconomicsPlatform(platform)
	if len(accountIDs) > 0 {
		databasePlatform = ""
	}
	rows, err := s.accounts.ListAllWithFilters(ctx, databasePlatform, "", "", "", 0, "")
	if err != nil {
		return nil, fmt.Errorf("list economics scope accounts: %w", err)
	}
	if len(accountIDs) == 0 {
		return rows, nil
	}
	selected := make(map[int64]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		if id > 0 {
			selected[id] = struct{}{}
		}
	}
	filtered := rows[:0]
	for index := range rows {
		if _, ok := selected[rows[index].ID]; ok {
			filtered = append(filtered, rows[index])
		}
	}
	return filtered, nil
}

func summarizeAccountEconomicsHealth(accounts []Account, now time.Time) (normal, limited, failed int) {
	for index := range accounts {
		account := &accounts[index]
		isLimited := (account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt)) ||
			(account.OverloadUntil != nil && now.Before(*account.OverloadUntil))
		if isLimited {
			limited++
			continue
		}
		blocked := account.Status != StatusActive || !account.Schedulable ||
			(account.AutoPauseOnExpired && account.ExpiresAt != nil && !now.Before(*account.ExpiresAt)) ||
			(account.TempUnschedulableUntil != nil && now.Before(*account.TempUnschedulableUntil))
		if blocked {
			failed++
			continue
		}
		normal++
	}
	return normal, limited, failed
}

func summarizeProcurementEconomics(accounts []Account, states []AccountCostLossState, platform string, cnyPerUSD float64, now time.Time, includeDeleted bool) (procurementCNY, impairmentCNY, hourlyCNY float64, invalid int) {
	states = ConsolidateActiveCostLossStates(states)
	latestActive := make(map[int64]AccountCostLossState)
	for _, state := range states {
		if !state.Active || (normalizeEconomicsPlatform(platform) != "" && !strings.EqualFold(state.Platform, platform)) {
			continue
		}
		previous, exists := latestActive[state.AccountIDSnapshot]
		if !exists || state.OccurredAt.After(previous.OccurredAt) {
			latestActive[state.AccountIDSnapshot] = state
		}
	}
	currentIDs := make(map[int64]struct{}, len(accounts))
	for index := range accounts {
		account := &accounts[index]
		// Explicit fixed overhead is valid for every account type. Eligibility
		// for terminal impairment is a separate rule, not a procurement filter.
		currentIDs[account.ID] = struct{}{}
		if state, ok := latestActive[account.ID]; ok {
			// RecognizedCost already contains NetLoss. Keep the two economic
			// components separate here so BuildAccountPoolUnitEconomics does not
			// add the impairment a second time.
			procurementCNY += costAmountToCNY(state.AccruedCost, state.Currency, cnyPerUSD)
			impairmentCNY += costAmountToCNY(state.NetLoss, state.Currency, cnyPerUSD)
			continue
		}
		profile, err := resolveAccountCostProfileSnapshot(account)
		if err != nil {
			invalid++
			continue
		}
		accrued, hourly := accruedCostAt(profile, now)
		procurementCNY += costAmountToCNY(accrued, profile.Currency, cnyPerUSD)
		hourlyCNY += costAmountToCNY(hourly, profile.Currency, cnyPerUSD)
	}
	if !includeDeleted {
		return procurementCNY, impairmentCNY, hourlyCNY, invalid
	}
	for accountID, state := range latestActive {
		if _, exists := currentIDs[accountID]; exists {
			continue
		}
		procurementCNY += costAmountToCNY(state.AccruedCost, state.Currency, cnyPerUSD)
		impairmentCNY += costAmountToCNY(state.NetLoss, state.Currency, cnyPerUSD)
	}
	return procurementCNY, impairmentCNY, hourlyCNY, invalid
}

// countInvalidDeletedProcurementProfiles reports malformed stored profiles of
// deleted accounts within the requested scope. Live accounts are counted by
// summarizeProcurementEconomics, so they are not counted twice here.
func countInvalidDeletedProcurementProfiles(profiles []AccountProcurementProfile, platform string, accountIDs []int64, includeDeleted bool) int {
	selected := make(map[int64]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		if id > 0 {
			selected[id] = struct{}{}
		}
	}
	platform = normalizeEconomicsPlatform(platform)
	count := 0
	for _, item := range profiles {
		if !item.Invalid || !item.Deleted {
			continue
		}
		if platform != "" && !strings.EqualFold(item.Platform, platform) {
			continue
		}
		if len(selected) > 0 {
			if _, exists := selected[item.AccountID]; !exists {
				continue
			}
		} else if !includeDeleted {
			continue
		}
		count++
	}
	return count
}

func summarizeMonthlyProcurementProfiles(
	profiles []AccountProcurementProfile,
	states []AccountCostLossState,
	platform string,
	accountIDs []int64,
	cnyPerUSD float64,
	now time.Time,
	includeDeleted bool,
) (oneTimeCNY float64, oneTimeCount, deletedOneTimeCount int, deletedRecurringCNY float64, deletedRecurringCount int) {
	selected := make(map[int64]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		if id > 0 {
			selected[id] = struct{}{}
		}
	}
	// A confirmed terminal event stops accrual (the loss ledger books the rest
	// of the current prepaid cycle), so no further cycles are paid after it.
	terminalAt := make(map[int64]time.Time)
	for _, state := range ConsolidateActiveCostLossStates(states) {
		if !state.Active || state.AccountIDSnapshot <= 0 {
			continue
		}
		if previous, exists := terminalAt[state.AccountIDSnapshot]; !exists || state.OccurredAt.After(previous) {
			terminalAt[state.AccountIDSnapshot] = state.OccurredAt
		}
	}
	platform = normalizeEconomicsPlatform(platform)
	for _, item := range profiles {
		if item.Invalid || item.CostProfile.Amount < 0 || item.CostProfile.StartedAt.IsZero() || item.CostProfile.StartedAt.After(now) {
			continue
		}
		if platform != "" && !strings.EqualFold(item.Platform, platform) {
			continue
		}
		if len(selected) > 0 {
			if _, exists := selected[item.AccountID]; !exists {
				continue
			}
		} else if item.Deleted && !includeDeleted {
			continue
		}
		started := item.CostProfile.StartedAt.In(now.Location())
		if started.Year() != now.Year() || started.Month() != now.Month() {
			continue
		}
		amountCNY := costAmountToCNY(item.CostProfile.Amount, item.CostProfile.Currency, cnyPerUSD)
		if item.CostProfile.BillingCycle == "one_time" {
			oneTimeCNY += amountCNY
			oneTimeCount++
			if item.Deleted {
				deletedOneTimeCount++
			}
		} else if item.Deleted {
			end := now
			if item.DeletedAt != nil && !item.DeletedAt.IsZero() && item.DeletedAt.Before(end) {
				end = *item.DeletedAt
			}
			if terminal, exists := terminalAt[item.AccountID]; exists && terminal.Before(end) {
				end = terminal
			}
			cycles := deletedRecurringPaidCycles(item.CostProfile, end)
			if cycles <= 0 {
				continue
			}
			deletedRecurringCNY += amountCNY * cycles
			deletedRecurringCount++
		}
	}
	return oneTimeCNY, oneTimeCount, deletedOneTimeCount, deletedRecurringCNY, deletedRecurringCount
}

// deletedRecurringPaidCycles counts the prepaid cycles a recurring profile
// started before end (its deletion, terminal event or now). A started cycle is
// paid in full, matching the terminal loss ledger: a daily account kept for 5.5
// days paid 6 cycles, and a monthly account deleted within its first 730-hour
// cycle paid one. An account removed before its start date paid nothing.
func deletedRecurringPaidCycles(profile AccountCostProfileSnapshot, end time.Time) float64 {
	if end.Before(profile.StartedAt) {
		return 0
	}
	cycleHours := costCycleHours[profile.BillingCycle]
	if cycleHours <= 0 {
		return 1
	}
	activeHours := end.Sub(profile.StartedAt).Hours()
	if activeHours <= 0 {
		return 1
	}
	return math.Max(1, math.Ceil(activeHours/cycleHours-1e-9))
}

func summarizeWindowEconomics(
	accounts []Account,
	profiles []AccountProcurementProfile,
	states []AccountCostLossState,
	platform string,
	accountIDs []int64,
	cnyPerUSD float64,
	windowStart, windowEnd time.Time,
	includeDeleted bool,
) (procurementCNY, impairmentCNY float64) {
	states = ConsolidateActiveCostLossStates(states)
	selected := make(map[int64]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		if id > 0 {
			selected[id] = struct{}{}
		}
	}
	platform = normalizeEconomicsPlatform(platform)
	latestActive := make(map[int64]AccountCostLossState)
	for _, state := range states {
		if !state.Active || (platform != "" && !strings.EqualFold(state.Platform, platform)) {
			continue
		}
		if len(selected) > 0 {
			if _, exists := selected[state.AccountIDSnapshot]; !exists {
				continue
			}
		} else if state.AccountDeleted && !includeDeleted {
			continue
		}
		previous, exists := latestActive[state.AccountIDSnapshot]
		if !exists || state.OccurredAt.After(previous.OccurredAt) {
			latestActive[state.AccountIDSnapshot] = state
		}
	}
	for _, state := range latestActive {
		if !state.OccurredAt.Before(windowStart) && !state.OccurredAt.After(windowEnd) {
			impairmentCNY += costAmountToCNY(state.NetLoss, state.Currency, cnyPerUSD)
		}
	}

	profileByID := make(map[int64]AccountProcurementProfile, len(profiles))
	for _, item := range profiles {
		if item.Invalid {
			continue
		}
		profileByID[item.AccountID] = item
	}
	for _, account := range accounts {
		profile, err := resolveAccountCostProfileSnapshot(&account)
		if err == nil {
			profileByID[account.ID] = AccountProcurementProfile{AccountID: account.ID, Platform: account.Platform, CostProfile: profile}
		} else {
			delete(profileByID, account.ID)
		}
	}
	for accountID, item := range profileByID {
		if platform != "" && !strings.EqualFold(item.Platform, platform) {
			continue
		}
		if len(selected) > 0 {
			if _, exists := selected[accountID]; !exists {
				continue
			}
		} else if item.Deleted && !includeDeleted {
			continue
		}
		stop := windowEnd
		if state, exists := latestActive[accountID]; exists && state.OccurredAt.Before(stop) {
			stop = state.OccurredAt
		}
		// A deleted account stops accruing at its deletion, as in the monthly summary.
		if item.DeletedAt != nil && !item.DeletedAt.IsZero() && item.DeletedAt.Before(stop) {
			stop = *item.DeletedAt
		}
		procurementCNY += costAmountToCNY(procurementCostBetween(item.CostProfile, windowStart, stop), item.CostProfile.Currency, cnyPerUSD)
	}
	return procurementCNY, impairmentCNY
}

func procurementCostBetween(profile AccountCostProfileSnapshot, start, end time.Time) float64 {
	if profile.Amount < 0 || profile.StartedAt.IsZero() || end.Before(start) {
		return 0
	}
	if profile.BillingCycle == "one_time" {
		if !profile.StartedAt.Before(start) && !profile.StartedAt.After(end) {
			return profile.Amount
		}
		return 0
	}
	cycleHours := costCycleHours[profile.BillingCycle]
	if cycleHours <= 0 {
		return 0
	}
	effectiveStart := start
	if profile.StartedAt.After(effectiveStart) {
		effectiveStart = profile.StartedAt
	}
	if !end.After(effectiveStart) {
		return 0
	}
	return profile.Amount / cycleHours * end.Sub(effectiveStart).Hours()
}

func accruedCostAt(profile AccountCostProfileSnapshot, now time.Time) (accrued, hourly float64) {
	if now.Before(profile.StartedAt) {
		return 0, 0
	}
	if profile.BillingCycle == "one_time" {
		return profile.Amount, 0
	}
	cycleHours := costCycleHours[profile.BillingCycle]
	if cycleHours <= 0 {
		return 0, 0
	}
	hourly = profile.Amount / cycleHours
	return hourly * now.Sub(profile.StartedAt).Hours(), hourly
}

func costAmountToCNY(amount float64, currency string, cnyPerUSD float64) float64 {
	if strings.EqualFold(currency, "USD") {
		return amount * cnyPerUSD
	}
	return amount
}

func normalizeEconomicsPlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "all" {
		return ""
	}
	return platform
}

func economicsScopeKey(scope, platform string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope != "" && scope != "all" {
		return "account-pool:provider:" + scope
	}
	platform = normalizeEconomicsPlatform(platform)
	if platform == "" {
		return "account-pool:all"
	}
	return "account-pool:platform:" + platform
}

func economicsMembershipHash(ids []int64) string {
	sorted := append([]int64(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	hash := sha256.New()
	for _, id := range sorted {
		_, _ = hash.Write([]byte(strconv.FormatInt(id, 10)))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// BuildAccountPoolUnitEconomics keeps factual usage in USD and procurement in
// CNY until the explicit exchange-rate boundary. It never relabels a configured
// CNY amount as USD.
func BuildAccountPoolUnitEconomics(input AccountPoolUnitEconomicsInput) AccountPoolUnitEconomics {
	economicCost := math.Max(0, input.ProcurementAccruedCNY) + math.Max(0, input.ImpairmentLossCNY)
	grossMarginCNY := input.BilledUSD*input.CNYPerUSD - input.AccountCostUSD*input.CNYPerUSD
	result := AccountPoolUnitEconomics{
		BilledUSD:             input.BilledUSD,
		AccountCostUSD:        input.AccountCostUSD,
		ProcurementAccruedCNY: math.Max(0, input.ProcurementAccruedCNY),
		ImpairmentLossCNY:     math.Max(0, input.ImpairmentLossCNY),
		EconomicCostCNY:       economicCost,
		ContributionMarginCNY: grossMarginCNY - economicCost,
	}
	if input.BilledUSD > 0 {
		value := economicCost / input.BilledUSD
		result.CNYPerBilledUSD = &value
	}
	if economicCost > 0 && input.BilledUSD > 0 {
		value := grossMarginCNY / economicCost
		result.PaybackRatio = &value
	}
	if input.BilledUSDPerHour == nil || input.AccountCostUSDPerHour == nil || input.CNYPerUSD <= 0 {
		return result
	}
	projectedContribution := (*input.BilledUSDPerHour-*input.AccountCostUSDPerHour)*input.CNYPerUSD - math.Max(0, input.ProcurementHourlyCNY)
	result.ProjectedContributionCNYPerHour = &projectedContribution
	outstanding := math.Max(0, economicCost-grossMarginCNY)
	if projectedContribution > 0 {
		hours := outstanding / projectedContribution
		result.EstimatedPaybackHours = &hours
	}
	return result
}

// BuildAccountEconomicsProjection derives a rate from adjacent cumulative
// samples. Membership changes and counter regressions split the series and are
// never treated as production.
func BuildAccountEconomicsProjection(samples []AccountEconomicsSample) AccountEconomicsProjection {
	projection := AccountEconomicsProjection{
		Version:    EconomicsProjectionVersion,
		Confidence: "unavailable",
	}
	if len(samples) < 2 {
		projection.Warning = "insufficient stable samples"
		return projection
	}

	ordered := append([]AccountEconomicsSample(nil), samples...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].SampledAt.Before(ordered[j].SampledAt) })
	var billedDelta, accountCostDelta, durationHours, healthyAccountHours float64
	for index := 1; index < len(ordered); index++ {
		previous, current := ordered[index-1], ordered[index]
		duration := current.SampledAt.Sub(previous.SampledAt)
		stableMembership := strings.TrimSpace(previous.MembershipHash) != "" &&
			previous.MembershipHash == current.MembershipHash &&
			previous.AccountCount == current.AccountCount
		billed := current.BilledUSDTotal - previous.BilledUSDTotal
		accountCost := current.AccountCostUSDTotal - previous.AccountCostUSDTotal
		if !stableMembership || duration < minimumEconomicsInterval || billed < -1e-9 || accountCost < -1e-9 {
			projection.ResetIntervals++
			continue
		}
		if duration > maximumEconomicsInterval {
			projection.GapIntervals++
			continue
		}
		projection.ValidIntervals++
		hours := duration.Hours()
		durationHours += hours
		healthyAccountHours += float64(current.NormalCount) * hours
		billedDelta += billed
		accountCostDelta += accountCost
	}

	projection.CoverageHours = durationHours
	latest := ordered[len(ordered)-1]
	if latest.AccountCount > 0 {
		projection.HealthyCapacityRatio = float64(latest.NormalCount) / float64(latest.AccountCount)
	}
	warnings := make([]string, 0, 3)
	if projection.ResetIntervals > 0 {
		warnings = append(warnings, "some intervals were excluded after pool or counter resets")
	}
	if projection.GapIntervals > 0 {
		warnings = append(warnings, "sampling gaps longer than 30 minutes were excluded")
	}
	if projection.ValidIntervals < minimumEconomicsValidIntervals || durationHours <= 0 {
		projection.Warning = "insufficient stable samples after pool resets or sampling gaps"
		return projection
	}

	billedRate := billedDelta / durationHours
	accountCostRate := accountCostDelta / durationHours
	projection.BilledUSDPerHour = &billedRate
	projection.AccountCostUSDPerHour = &accountCostRate
	switch {
	case durationHours >= 1 && projection.ValidIntervals >= 2:
		projection.Confidence = "high"
	case durationHours >= minimumForecastCoverage.Hours():
		projection.Confidence = "medium"
	default:
		projection.Confidence = "low"
	}
	if projection.Confidence == "low" {
		// The measured rate stays visible, but less than 15 minutes of stable
		// coverage is not enough evidence for forecasts or payback estimates.
		projection.CapacityAdjustment = 1
		warnings = append(warnings, "stable coverage below 15 minutes; forecast withheld")
		projection.Warning = strings.Join(warnings, "; ")
		return projection
	}

	capacityAdjustment := 1.0
	averageHealthyAccounts := healthyAccountHours / durationHours
	switch {
	case latest.NormalCount <= 0:
		// No healthy capacity remains, so no further production is expected.
		capacityAdjustment = 0
	case averageHealthyAccounts >= 1:
		capacityAdjustment = float64(latest.NormalCount) / averageHealthyAccounts
	default:
		// Fewer than one healthy account on average cannot support a reliable
		// per-account rate; keep the measured rate instead of extrapolating it.
	}
	if capacityAdjustment > maximumCapacityAdjustment {
		capacityAdjustment = maximumCapacityAdjustment
		warnings = append(warnings, "capacity adjustment capped at 2x")
	}
	adjustedBilledRate := billedRate * capacityAdjustment
	adjustedAccountCostRate := accountCostRate * capacityAdjustment
	projection.CapacityAdjustment = capacityAdjustment
	projection.CapacityAdjustedBilledUSDPerHour = &adjustedBilledRate
	projection.CapacityAdjustedAccountCostUSDPerHour = &adjustedAccountCostRate
	projection.Warning = strings.Join(warnings, "; ")
	return projection
}
