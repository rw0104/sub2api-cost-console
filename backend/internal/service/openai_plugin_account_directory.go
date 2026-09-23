package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
)

type pluginAccountScopeEntry struct {
	Platform    string
	AccountType string
	AccountIDs  []int64
	UserIDs     []int64
	GroupIDs    []int64
}

// PluginAccountScope is a host-created, immutable account-directory grant.
// Empty scope means no grant; an entry with empty account_ids is an explicit
// wildcard for its platform/account type.
type PluginAccountScope struct{ entries []pluginAccountScopeEntry }

func newPluginAccountScope(entries ...pluginAccountScopeEntry) PluginAccountScope {
	out := PluginAccountScope{entries: make([]pluginAccountScopeEntry, 0, len(entries))}
	for _, entry := range entries {
		copyEntry := entry
		copyEntry.AccountIDs = append([]int64(nil), entry.AccountIDs...)
		copyEntry.UserIDs = append([]int64(nil), entry.UserIDs...)
		copyEntry.GroupIDs = append([]int64(nil), entry.GroupIDs...)
		out.entries = append(out.entries, copyEntry)
	}
	return out
}

func pluginAccountScopeForInstallation(installation *PluginInstallation) PluginAccountScope {
	return pluginAccountScopeForCapability(installation, "")
}

func pluginAccountScopeForCapability(installation *PluginInstallation, capability string) PluginAccountScope {
	if installation == nil {
		return PluginAccountScope{}
	}
	entries := make([]pluginAccountScopeEntry, 0, len(installation.Bindings))
	for _, binding := range installation.Bindings {
		if !binding.Enabled || (capability != "" && binding.Capability != capability) || binding.Platform != PlatformOpenAI || binding.AccountType != AccountTypeOAuth {
			continue
		}
		entries = append(entries, pluginAccountScopeEntry{
			Platform: binding.Platform, AccountType: binding.AccountType,
			AccountIDs: append([]int64(nil), binding.AccountIDs...),
			UserIDs:    append([]int64(nil), binding.UserIDs...),
			GroupIDs:   append([]int64(nil), binding.GroupIDs...),
		})
	}
	return newPluginAccountScope(entries...)
}

func (s PluginAccountScope) allows(account *Account) bool {
	if account == nil || len(s.entries) == 0 {
		return false
	}
	for _, entry := range s.entries {
		if entry.Platform != "" && entry.Platform != account.Platform || entry.AccountType != "" && entry.AccountType != account.Type {
			continue
		}
		// Directory calls have no authenticated request principal. A binding
		// restricted only by user/group therefore cannot enumerate accounts.
		if len(entry.UserIDs) > 0 || len(entry.GroupIDs) > 0 {
			continue
		}
		if pluginScopeContains(entry.AccountIDs, account.ID) {
			return true
		}
	}
	return false
}

func (s PluginAccountScope) allowsID(accountID int64) bool {
	if accountID <= 0 || len(s.entries) == 0 {
		return false
	}
	for _, entry := range s.entries {
		if len(entry.UserIDs) > 0 || len(entry.GroupIDs) > 0 {
			continue
		}
		if pluginScopeContains(entry.AccountIDs, accountID) {
			return true
		}
	}
	return false
}

type PluginAccountInfo struct {
	ID           int64
	Platform     string
	AccountType  string
	Name         string
	Status       string
	Schedulable  bool
	IsShadow     bool
	MetadataJSON json.RawMessage
}

// PluginAccountDirectory is the legacy HostService directory contract.
type PluginAccountDirectory interface {
	ListPluginAccounts(context.Context, string, string) ([]int64, error)
	ResolvePluginOutboundIdentity(context.Context, int64) (*PluginOutboundIdentity, error)
}

// ScopedPluginAccountDirectory is the binding-scoped contract used by new
// host instances. The variadic shape keeps transitional callers source-safe.
type ScopedPluginAccountDirectory interface {
	ListPluginAccounts(context.Context, ...any) ([]PluginAccountInfo, error)
	ResolvePluginOutboundIdentityScoped(context.Context, PluginAccountScope, int64) (*PluginOutboundIdentity, error)
}

func (s *OpenAIGatewayService) ListPluginAccounts(ctx context.Context, args ...any) ([]PluginAccountInfo, error) {
	if s == nil || s.accountRepo == nil {
		return nil, nil
	}
	var scope PluginAccountScope
	var platform, accountType string
	if len(args) == 3 {
		scope, _ = args[0].(PluginAccountScope)
		platform, _ = args[1].(string)
		accountType, _ = args[2].(string)
	} else if len(args) == 2 {
		platform, _ = args[0].(string)
		accountType, _ = args[1].(string)
		scope = newPluginAccountScope(pluginAccountScopeEntry{Platform: platform, AccountType: accountType})
	} else {
		return nil, nil
	}
	if strings.TrimSpace(platform) != "" && strings.TrimSpace(platform) != PlatformOpenAI ||
		strings.TrimSpace(accountType) != "" && strings.TrimSpace(accountType) != AccountTypeOAuth {
		return nil, nil
	}
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	infos := make([]PluginAccountInfo, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if account.Status != StatusActive || account.Type != AccountTypeOAuth || !account.IsOpenAIOAuthLike() || account.IsShadow() || !scope.allows(account) {
			continue
		}
		infos = append(infos, PluginAccountInfo{ID: account.ID, Platform: account.Platform, AccountType: account.Type,
			Name: account.Name, Status: account.Status, Schedulable: account.IsSchedulable(), IsShadow: account.IsShadow(),
			MetadataJSON: accountReadableSnapshotJSON(account)})
	}
	return infos, nil
}

func (s *OpenAIGatewayService) ResolvePluginOutboundIdentityScoped(ctx context.Context, scope PluginAccountScope, accountID int64) (*PluginOutboundIdentity, error) {
	if s == nil || !scope.allowsID(accountID) {
		return nil, nil
	}
	return s.resolvePluginOutboundIdentity(ctx, accountID)
}

// ResolvePluginOutboundIdentity is retained for the legacy directory contract.
func (s *OpenAIGatewayService) ResolvePluginOutboundIdentity(ctx context.Context, accountID int64) (*PluginOutboundIdentity, error) {
	return s.resolvePluginOutboundIdentity(ctx, accountID)
}

func (s *OpenAIGatewayService) resolvePluginOutboundIdentity(ctx context.Context, accountID int64) (*PluginOutboundIdentity, error) {
	if s == nil || s.accountRepo == nil || accountID <= 0 {
		return nil, nil
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil || account.Type != AccountTypeOAuth || !account.IsOpenAIOAuthLike() || account.IsShadow() {
		return nil, nil
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, err
	}
	headers := http.Header{}
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, headers, account); err != nil {
		return nil, err
	}
	ensureCodexIdentityHeaders(headers)
	enforceCodexIdentityHeaders(headers)
	return &PluginOutboundIdentity{AccountID: account.ID, Platform: account.Platform, AccountType: account.Type, ProxyURL: resolveAccountProxyURL(account), Token: token, Headers: headers}, nil
}

// accountReadableSnapshotJSON returns a bounded, cycle-safe, non-secret view.
func accountReadableSnapshotJSON(account *Account) []byte {
	if account == nil {
		return nil
	}
	readable := struct {
		ID                      int64                         `json:"id"`
		Name                    string                        `json:"name"`
		Notes                   *string                       `json:"notes,omitempty"`
		Platform                string                        `json:"platform"`
		Type                    string                        `json:"type"`
		Extra                   map[string]any                `json:"extra,omitempty"`
		Proxy                   *Proxy                        `json:"proxy,omitempty"`
		ProxyID                 *int64                        `json:"proxy_id,omitempty"`
		ProxyFallbackOriginID   *int64                        `json:"proxy_fallback_origin_id,omitempty"`
		ProxyFallbackOriginName *string                       `json:"proxy_fallback_origin_name,omitempty"`
		Concurrency             int                           `json:"concurrency"`
		Priority                int                           `json:"priority"`
		RateMultiplier          *float64                      `json:"rate_multiplier,omitempty"`
		LoadFactor              *int                          `json:"load_factor,omitempty"`
		Status                  string                        `json:"status"`
		ErrorMessage            string                        `json:"error_message,omitempty"`
		LastUsedAt              *time.Time                    `json:"last_used_at,omitempty"`
		ExpiresAt               *time.Time                    `json:"expires_at,omitempty"`
		AutoPauseOnExpired      bool                          `json:"auto_pause_on_expired"`
		CreatedAt               time.Time                     `json:"created_at"`
		UpdatedAt               time.Time                     `json:"updated_at"`
		Schedulable             bool                          `json:"schedulable"`
		RateLimitedAt           *time.Time                    `json:"rate_limited_at,omitempty"`
		RateLimitResetAt        *time.Time                    `json:"rate_limit_reset_at,omitempty"`
		OverloadUntil           *time.Time                    `json:"overload_until,omitempty"`
		TempUnschedulableUntil  *time.Time                    `json:"temp_unschedulable_until,omitempty"`
		TempUnschedulableReason string                        `json:"temp_unschedulable_reason,omitempty"`
		SessionWindowStart      *time.Time                    `json:"session_window_start,omitempty"`
		SessionWindowEnd        *time.Time                    `json:"session_window_end,omitempty"`
		SessionWindowStatus     string                        `json:"session_window_status,omitempty"`
		ParentAccountID         *int64                        `json:"parent_account_id,omitempty"`
		QuotaDimension          string                        `json:"quota_dimension,omitempty"`
		GroupIDs                []int64                       `json:"group_ids,omitempty"`
		Subscription            *pluginv2.AccountSubscription `json:"subscription,omitempty"`
	}{
		ID: account.ID, Name: account.Name, Notes: account.Notes, Platform: account.Platform, Type: account.Type, Extra: account.Extra, Proxy: account.Proxy, ProxyID: account.ProxyID, ProxyFallbackOriginID: account.ProxyFallbackOriginID, ProxyFallbackOriginName: account.ProxyFallbackOriginName,
		Concurrency: account.Concurrency, Priority: account.Priority, RateMultiplier: account.RateMultiplier, LoadFactor: account.LoadFactor, Status: account.Status, ErrorMessage: account.ErrorMessage, LastUsedAt: account.LastUsedAt, ExpiresAt: account.ExpiresAt, AutoPauseOnExpired: account.AutoPauseOnExpired, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt, Schedulable: account.IsSchedulable(), RateLimitedAt: account.RateLimitedAt, RateLimitResetAt: account.RateLimitResetAt, OverloadUntil: account.OverloadUntil, TempUnschedulableUntil: account.TempUnschedulableUntil, TempUnschedulableReason: account.TempUnschedulableReason, SessionWindowStart: account.SessionWindowStart, SessionWindowEnd: account.SessionWindowEnd, SessionWindowStatus: account.SessionWindowStatus, ParentAccountID: account.ParentAccountID, QuotaDimension: account.QuotaDimension, GroupIDs: append([]int64(nil), account.GroupIDs...), Subscription: pluginAccountSubscription(account),
	}
	raw, err := json.Marshal(readable)
	if err != nil || len(raw) > 128*1024 {
		return nil
	}
	return raw
}
