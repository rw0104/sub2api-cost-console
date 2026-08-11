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
	EconomicsProjectionVersion     = "1.0.0"
	minimumEconomicsInterval       = 15 * time.Second
	minimumEconomicsValidIntervals = 2
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
	BilledUSD                       float64  `json:"billed_usd"`
	AccountCostUSD                  float64  `json:"account_cost_usd"`
	ProcurementAccruedCNY           float64  `json:"procurement_accrued_cny"`
	ImpairmentLossCNY               float64  `json:"impairment_loss_cny"`
	EconomicCostCNY                 float64  `json:"economic_cost_cny"`
	ContributionMarginCNY           float64  `json:"contribution_margin_cny"`
	CNYPerBilledUSD                 *float64 `json:"cny_per_billed_usd"`
	PaybackRatio                    *float64 `json:"payback_ratio"`
	ProjectedContributionCNYPerHour *float64 `json:"projected_contribution_cny_per_hour"`
	EstimatedPaybackHours           *float64 `json:"estimated_payback_hours"`
}

type AccountEconomicsUsageTotals struct {
	BilledUSD      float64
	AccountCostUSD float64
}

type AccountEconomicsRepository interface {
	SumUsageTotals(ctx context.Context, accountIDs []int64) (AccountEconomicsUsageTotals, error)
	UpsertSample(ctx context.Context, sample AccountEconomicsSample) error
	ListSamples(ctx context.Context, scopeKey string, since time.Time) ([]AccountEconomicsSample, error)
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

type AccountEconomicsSnapshot struct {
	AlgorithmVersion  string                      `json:"algorithm_version"`
	ProjectionVersion string                      `json:"projection_version"`
	SampledAt         time.Time                   `json:"sampled_at"`
	Scope             AccountEconomicsScope       `json:"scope"`
	CNYPerUSD         float64                     `json:"cny_per_usd"`
	Health            AccountEconomicsHealth      `json:"health"`
	Actual            AccountPoolUnitEconomics    `json:"actual"`
	Projection        AccountEconomicsProjection  `json:"projection"`
	DataQuality       AccountEconomicsDataQuality `json:"data_quality"`
}

type AccountEconomicsQuery struct {
	Scope              string
	Platform           string
	AccountIDs         []int64
	CNYPerUSD          float64
	ExchangeRateSource string
	Window             time.Duration
	Now                time.Time
}

type AccountEconomicsService struct {
	accounts  AccountEconomicsAccountReader
	repo      AccountEconomicsRepository
	losses    *AccountCostLossService
	startOnce sync.Once
}

func NewAccountEconomicsService(accounts AccountEconomicsAccountReader, repo AccountEconomicsRepository, losses *AccountCostLossService) *AccountEconomicsService {
	return &AccountEconomicsService{accounts: accounts, repo: repo, losses: losses}
}

func (s *AccountEconomicsService) Start() {
	if s == nil || s.accounts == nil || s.repo == nil {
		return
	}
	s.startOnce.Do(func() {
		go func() {
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
				<-ticker.C
			}
		}()
	})
}

func (s *AccountEconomicsService) GetSnapshot(ctx context.Context, query AccountEconomicsQuery) (*AccountEconomicsSnapshot, error) {
	if s == nil || s.accounts == nil || s.repo == nil || s.losses == nil {
		return nil, errors.New("account economics module is unavailable")
	}
	now := query.Now
	if now.IsZero() {
		now = time.Now()
	}
	window := query.Window
	if window <= 0 {
		window = time.Hour
	}
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

	current, err := s.recordSample(ctx, query.Scope, query.Platform, query.AccountIDs, now)
	if err != nil {
		return nil, err
	}
	samples, err := s.repo.ListSamples(ctx, current.ScopeKey, now.Add(-window))
	if err != nil {
		return nil, fmt.Errorf("list economics samples: %w", err)
	}
	quality.SampleCount = len(samples)
	projection := BuildAccountEconomicsProjection(samples)
	accounts, err := s.loadScopeAccounts(ctx, query.Platform, query.AccountIDs)
	if err != nil {
		return nil, err
	}
	states, err := s.losses.ListStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list account cost loss states: %w", err)
	}
	includeDeleted := includeArchivedEconomics(query.Scope, query.AccountIDs)
	procurement, impairment, hourly, invalid := summarizeProcurementEconomics(accounts, states, query.Platform, cnyPerUSD, now, includeDeleted)
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
		Actual: actual, Projection: projection, DataQuality: quality,
	}, nil
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
		if !isProcurementAccountForCostLoss(account) {
			continue
		}
		currentIDs[account.ID] = struct{}{}
		if state, ok := latestActive[account.ID]; ok {
			procurementCNY += costAmountToCNY(state.RecognizedCost, state.Currency, cnyPerUSD)
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
		procurementCNY += costAmountToCNY(state.RecognizedCost, state.Currency, cnyPerUSD)
		impairmentCNY += costAmountToCNY(state.NetLoss, state.Currency, cnyPerUSD)
	}
	return procurementCNY, impairmentCNY, hourlyCNY, invalid
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
	if projection.ValidIntervals < minimumEconomicsValidIntervals || durationHours <= 0 {
		projection.Warning = "insufficient stable samples after pool or counter resets"
		return projection
	}

	billedRate := billedDelta / durationHours
	accountCostRate := accountCostDelta / durationHours
	projection.BilledUSDPerHour = &billedRate
	projection.AccountCostUSDPerHour = &accountCostRate
	capacityAdjustment := 1.0
	averageHealthyAccounts := healthyAccountHours / durationHours
	if averageHealthyAccounts > 0 {
		capacityAdjustment = float64(latest.NormalCount) / averageHealthyAccounts
	}
	adjustedBilledRate := billedRate * capacityAdjustment
	adjustedAccountCostRate := accountCostRate * capacityAdjustment
	projection.CapacityAdjustment = capacityAdjustment
	projection.CapacityAdjustedBilledUSDPerHour = &adjustedBilledRate
	projection.CapacityAdjustedAccountCostUSDPerHour = &adjustedAccountCostRate
	switch {
	case durationHours >= 1 && projection.ValidIntervals >= 2:
		projection.Confidence = "high"
	case durationHours >= 0.25:
		projection.Confidence = "medium"
	default:
		projection.Confidence = "low"
	}
	if projection.ResetIntervals > 0 {
		projection.Warning = "some intervals were excluded after pool or counter resets"
	}
	return projection
}
