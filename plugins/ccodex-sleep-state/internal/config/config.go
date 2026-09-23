package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const (
	DefaultMaxBodyBytes                 = 64 * 1024 * 1024
	MaxBodyBytesLimit                   = 128 * 1024 * 1024
	DefaultResponseHeaderTimeoutSeconds = 120
	DefaultProbeTimeoutSeconds          = 25
	DefaultStateTTLSeconds              = 3600
	DefaultRefreshBeforeSeconds         = 600
	DefaultCooldownSeconds              = 180
	DefaultSubscriptionRefreshSeconds   = 900
	DefaultStateTargetLength            = 292
	DefaultRouteMode                    = "round_robin"
	MaxRouteURLs                        = 256
)

// Config is the first plugin configuration surface. It deliberately contains
// no Codex filesystem paths or credentials; the host owns those boundaries.
type Config struct {
	CoreOptions
	Revision                     uint64                     `json:"revision"`
	Enabled                      bool                       `json:"enabled"`
	InjectState                  bool                       `json:"inject_state"`
	HarvestOnDemand              bool                       `json:"harvest_on_demand"`
	FailClosed                   bool                       `json:"fail_closed"`
	ProxyURL                     string                     `json:"proxy_url,omitempty"`
	ProxyURLs                    []string                   `json:"proxy_urls,omitempty"`
	Direct                       bool                       `json:"direct"`
	RouteMode                    string                     `json:"route_mode,omitempty"`
	FixedRouteID                 string                     `json:"fixed_route_id,omitempty"`
	ProxyEnvs                    []string                   `json:"proxy_envs,omitempty"`
	Subscriptions                []Subscription             `json:"subscriptions,omitempty"`
	SubscriptionProxyEnv         string                     `json:"subscription_proxy_env,omitempty"`
	SubscriptionRefreshSeconds   int                        `json:"subscription_refresh_seconds"`
	MaxBodyBytes                 int                        `json:"max_body_bytes"`
	ResponseHeaderTimeoutSeconds int                        `json:"response_header_timeout_seconds"`
	ProbeTimeoutSeconds          int                        `json:"probe_timeout_seconds"`
	StateTTLSeconds              int                        `json:"state_ttl_seconds"`
	RefreshBeforeSeconds         int                        `json:"refresh_before_seconds"`
	CooldownSeconds              int                        `json:"cooldown_seconds"`
	StateTargetLength            int                        `json:"state_target_length"`
	Models                       []string                   `json:"models"`
	Accounts                     map[string]AccountOverride `json:"accounts"`
	RouteRequest                 *RouteRequest              `json:"route_request,omitempty"`
	RouteReport                  *RouteReport               `json:"route_report,omitempty"`
	CoreRequest                  *CoreRequest               `json:"core_request,omitempty"`
	CoreReport                   *CoreReport                `json:"core_report,omitempty"`
}

type Subscription struct {
	URL              string   `json:"url,omitempty"`
	URLEnv           string   `json:"url_env,omitempty"`
	File             string   `json:"file,omitempty"`
	UserAgent        string   `json:"user_agent,omitempty"`
	IncludeProtocols []string `json:"include_protocols,omitempty"`
	ExcludeKeywords  []string `json:"exclude_keywords,omitempty"`
}

type AccountOverride struct {
	CoreOverrides
	Enabled                      *bool    `json:"enabled,omitempty"`
	InjectState                  *bool    `json:"inject_state,omitempty"`
	HarvestOnDemand              *bool    `json:"harvest_on_demand,omitempty"`
	FailClosed                   *bool    `json:"fail_closed,omitempty"`
	ProxyURL                     *string  `json:"proxy_url,omitempty"`
	ProxyURLs                    []string `json:"proxy_urls"`
	Direct                       *bool    `json:"direct,omitempty"`
	RouteMode                    *string  `json:"route_mode,omitempty"`
	FixedRouteID                 *string  `json:"fixed_route_id,omitempty"`
	MaxBodyBytes                 int      `json:"max_body_bytes,omitempty"`
	ResponseHeaderTimeoutSeconds int      `json:"response_header_timeout_seconds,omitempty"`
	ProbeTimeoutSeconds          int      `json:"probe_timeout_seconds,omitempty"`
	StateTTLSeconds              int      `json:"state_ttl_seconds,omitempty"`
	RefreshBeforeSeconds         *int     `json:"refresh_before_seconds,omitempty"`
	CooldownSeconds              int      `json:"cooldown_seconds,omitempty"`
	SubscriptionRefreshSeconds   int      `json:"subscription_refresh_seconds,omitempty"`
	StateTargetLength            int      `json:"state_target_length,omitempty"`
	Models                       []string `json:"models,omitempty"`
}

func Parse(raw []byte) (*Config, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte(`{}`)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("configuration must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var c Config
	if err := decoder.Decode(&c); err != nil {
		return nil, errors.New("invalid configuration")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing configuration")
	}
	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if c.ResponseHeaderTimeoutSeconds == 0 {
		c.ResponseHeaderTimeoutSeconds = DefaultResponseHeaderTimeoutSeconds
	}
	if c.ProbeTimeoutSeconds == 0 {
		c.ProbeTimeoutSeconds = DefaultProbeTimeoutSeconds
	}
	if c.StateTTLSeconds == 0 {
		c.StateTTLSeconds = DefaultStateTTLSeconds
	}
	if c.RefreshBeforeSeconds == 0 {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if _, explicit := fields["refresh_before_seconds"]; !explicit {
			c.RefreshBeforeSeconds = DefaultRefreshBeforeSeconds
		}
	}
	if c.CooldownSeconds == 0 {
		c.CooldownSeconds = DefaultCooldownSeconds
	}
	if c.SubscriptionRefreshSeconds == 0 {
		c.SubscriptionRefreshSeconds = DefaultSubscriptionRefreshSeconds
	}
	if c.StateTargetLength == 0 {
		c.StateTargetLength = DefaultStateTargetLength
	}
	if c.RouteMode == "" {
		c.RouteMode = DefaultRouteMode
	}
	c.defaultCoreOptions(raw)
	if !c.Enabled && !c.InjectState {
		// An empty or disabled configuration must remain a safe passthrough.
		c.HarvestOnDemand = false
	}
	if len(c.Models) == 0 {
		c.Models = []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra"}
	}
	if c.Accounts == nil {
		c.Accounts = map[string]AccountOverride{}
	}
	if err := validate(&c); err != nil {
		return nil, err
	}
	if c.RouteRequest != nil {
		if c.RouteRequest.Operation != "discover" && c.RouteRequest.Operation != "test" {
			return nil, errors.New("route_request operation must be discover or test")
		}
		if len(c.RouteRequest.RouteID) > 128 || strings.ContainsAny(c.RouteRequest.RouteID, "\r\n\t ") {
			return nil, errors.New("route_request route_id is invalid")
		}
	}
	c.RouteReport = SanitizeRouteReport(c.RouteReport, c.SourceSignature())
	if err := validateCoreRequest(c.CoreRequest); err != nil {
		return nil, err
	}
	if c.CoreRequest != nil && c.RouteRequest != nil {
		return nil, errors.New("only one diagnostic operation is allowed per save")
	}
	c.CoreReport = sanitizeCoreReport(c.CoreReport)
	return &c, nil
}

func Normalize(raw []byte, current *Config) ([]byte, error) {
	next, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if current != nil && next.Revision != current.Revision {
		return nil, errors.New("configuration changed; reload before saving")
	}
	return json.Marshal(next)
}

func validate(c *Config) error {
	if err := validateBase(c); err != nil {
		return err
	}
	if len(c.Accounts) > 1000 {
		return errors.New("accounts must contain at most 1000 entries")
	}
	for id, override := range c.Accounts {
		accountID, err := strconv.ParseInt(id, 10, 64)
		if err != nil || accountID <= 0 || strconv.FormatInt(accountID, 10) != id {
			return errors.New("account IDs must be canonical positive integers")
		}
		if override.ProxyURL != nil {
			if strings.TrimSpace(*override.ProxyURL) != "" {
				canonical, err := normalizeProxyURL(*override.ProxyURL)
				if err != nil {
					return errors.New("account proxy_url must be a valid HTTP, HTTPS, SOCKS5 or SOCKS5H URL")
				}
				*override.ProxyURL = canonical
			}
		}
		override.ProxyURLs, err = normalizeProxyURLs(override.ProxyURLs)
		if err != nil {
			return err
		}
		effective := c.applyOverride(override)
		if err := validateBase(&effective); err != nil {
			return err
		}
		c.Accounts[id] = override
	}
	return nil
}

func validateBase(c *Config) error {
	if c.Revision > 1<<53-1 {
		return errors.New("configuration revision exceeds UI safe integer range")
	}
	if c.MaxBodyBytes < 1024 || c.MaxBodyBytes > MaxBodyBytesLimit {
		return errors.New("max_body_bytes must be 1024..134217728")
	}
	if err := c.validateCoreOptions(); err != nil {
		return err
	}
	if c.ResponseHeaderTimeoutSeconds < 1 || c.ResponseHeaderTimeoutSeconds > 120 {
		return errors.New("response_header_timeout_seconds must be 1..120")
	}
	if c.ProbeTimeoutSeconds < 1 || c.ProbeTimeoutSeconds > 120 {
		return errors.New("probe_timeout_seconds must be 1..120")
	}
	if c.StateTTLSeconds < 60 || c.StateTTLSeconds > 86400 {
		return errors.New("state_ttl_seconds must be 60..86400")
	}
	if c.RefreshBeforeSeconds < 0 || c.RefreshBeforeSeconds >= c.StateTTLSeconds {
		return errors.New("refresh_before_seconds must be less than state_ttl_seconds")
	}
	if c.CooldownSeconds < 1 || c.CooldownSeconds > 3600 {
		return errors.New("cooldown_seconds must be 1..3600")
	}
	if c.SubscriptionRefreshSeconds < 60 || c.SubscriptionRefreshSeconds > 86400 {
		return errors.New("subscription_refresh_seconds must be 60..86400")
	}
	if !validStateTargetLength(c.StateTargetLength) {
		return errors.New("state_target_length must match a padded opaque envelope length")
	}
	c.RouteMode = strings.ToLower(strings.TrimSpace(c.RouteMode))
	if c.RouteMode == "" {
		c.RouteMode = DefaultRouteMode
	}
	if c.RouteMode != "round_robin" && c.RouteMode != "fixed" {
		return errors.New("route_mode must be round_robin or fixed")
	}
	c.FixedRouteID = strings.TrimSpace(c.FixedRouteID)
	if len(c.FixedRouteID) > 128 || strings.ContainsAny(c.FixedRouteID, "\r\n\t ") {
		return errors.New("fixed_route_id is invalid")
	}
	if c.RouteMode == "fixed" && c.FixedRouteID == "" {
		return errors.New("fixed_route_id is required when route_mode is fixed")
	}
	if c.ProxyURL != "" {
		canonical, err := normalizeProxyURL(c.ProxyURL)
		if err != nil {
			return errors.New("proxy_url must be a valid HTTP, HTTPS, SOCKS5 or SOCKS5H URL")
		}
		c.ProxyURL = canonical
	}
	proxyURLs, err := normalizeProxyURLs(c.ProxyURLs)
	if err != nil {
		return err
	}
	// A legacy proxy_url is semantically migrated into the route list while
	// retaining the field so older hosts can still read the normalized config.
	if len(proxyURLs) == 0 && c.ProxyURL != "" {
		proxyURLs = []string{c.ProxyURL}
	}
	c.ProxyURLs = proxyURLs
	if err := validateSources(c); err != nil {
		return err
	}
	if len(c.Models) == 0 || len(c.Models) > 32 {
		return errors.New("models must contain 1..32 entries")
	}
	seen := make(map[string]struct{}, len(c.Models))
	for i, model := range c.Models {
		model = strings.TrimSpace(model)
		if model == "" || len(model) > 128 {
			return errors.New("models must contain non-empty names up to 128 bytes")
		}
		if _, ok := seen[model]; ok {
			return errors.New("models must be unique")
		}
		seen[model] = struct{}{}
		c.Models[i] = model
	}
	return nil
}

func validStateTargetLength(length int) bool {
	for blocks := 1; blocks <= 92; blocks++ {
		rawLength := 57 + 16*blocks
		if ((rawLength + 2) / 3 * 4) == length {
			return true
		}
	}
	return false
}

func (c Config) SupportsModel(model string) bool {
	for _, allowed := range c.Models {
		if strings.TrimSpace(model) == allowed {
			return true
		}
	}
	return false
}

func (c Config) ForAccount(accountID int64) Config {
	override, ok := c.Accounts[strconv.FormatInt(accountID, 10)]
	if !ok {
		c.Accounts = nil
		return c
	}
	return c.applyOverride(override)
}

func (c Config) applyOverride(override AccountOverride) Config {
	c.CoreOptions = c.CoreOptions.apply(override.CoreOverrides)
	if override.Enabled != nil {
		c.Enabled = *override.Enabled
	}
	if override.InjectState != nil {
		c.InjectState = *override.InjectState
	}
	if override.HarvestOnDemand != nil {
		c.HarvestOnDemand = *override.HarvestOnDemand
	}
	if override.FailClosed != nil {
		c.FailClosed = *override.FailClosed
	}
	if override.ProxyURL != nil {
		c.ProxyURL = *override.ProxyURL
		c.ProxyURLs = nil
	}
	if override.ProxyURLs != nil {
		c.ProxyURLs = slices.Clone(override.ProxyURLs)
		c.ProxyURL = ""
	}
	if override.Direct != nil {
		c.Direct = *override.Direct
	}
	if override.RouteMode != nil {
		c.RouteMode = *override.RouteMode
	}
	if override.FixedRouteID != nil {
		c.FixedRouteID = *override.FixedRouteID
	}
	if override.MaxBodyBytes != 0 {
		c.MaxBodyBytes = override.MaxBodyBytes
	}
	if override.ResponseHeaderTimeoutSeconds != 0 {
		c.ResponseHeaderTimeoutSeconds = override.ResponseHeaderTimeoutSeconds
	}
	if override.ProbeTimeoutSeconds != 0 {
		c.ProbeTimeoutSeconds = override.ProbeTimeoutSeconds
	}
	if override.StateTTLSeconds != 0 {
		c.StateTTLSeconds = override.StateTTLSeconds
	}
	if override.RefreshBeforeSeconds != nil {
		c.RefreshBeforeSeconds = *override.RefreshBeforeSeconds
	}
	if override.CooldownSeconds != 0 {
		c.CooldownSeconds = override.CooldownSeconds
	}
	if override.SubscriptionRefreshSeconds != 0 {
		c.SubscriptionRefreshSeconds = override.SubscriptionRefreshSeconds
	}
	if override.StateTargetLength != 0 {
		c.StateTargetLength = override.StateTargetLength
	}
	if len(override.Models) > 0 {
		c.Models = append([]string(nil), override.Models...)
	}
	c.Accounts = nil
	return c
}

func (c Config) String() string {
	return "revision=" + strconv.FormatUint(c.Revision, 10)
}

// RouteURLs returns the effective, deduplicated proxy URLs for this config.
// An empty result means that the caller may use its legacy request proxy or a
// direct route, depending on Direct and the caller's fallback policy.
func (c Config) RouteURLs() []string {
	if len(c.ProxyURLs) > 0 {
		return append([]string(nil), c.ProxyURLs...)
	}
	if strings.TrimSpace(c.ProxyURL) != "" {
		return []string{strings.TrimSpace(c.ProxyURL)}
	}
	return nil
}

func (c Config) HasRouteSources() bool {
	return c.Direct || len(c.RouteURLs()) > 0 || len(c.ProxyEnvs) > 0 || len(c.Subscriptions) > 0
}

// RouteSignature identifies route-affecting configuration. It deliberately
// excludes state policy and model settings so those changes do not invalidate
// an otherwise reusable outbound route registry.
func (c Config) RouteSignature() string {
	type accountRoute struct {
		ProxyURL  *string  `json:"proxy_url,omitempty"`
		ProxyURLs []string `json:"proxy_urls"`
		Direct    *bool    `json:"direct,omitempty"`
		RouteMode *string  `json:"route_mode,omitempty"`
		FixedID   *string  `json:"fixed_route_id,omitempty"`
	}
	type routeConfig struct {
		URLs                 []string                `json:"urls,omitempty"`
		Direct               bool                    `json:"direct"`
		RouteMode            string                  `json:"route_mode"`
		FixedRouteID         string                  `json:"fixed_route_id,omitempty"`
		ProxyEnvs            []string                `json:"proxy_envs,omitempty"`
		Subscriptions        []Subscription          `json:"subscriptions,omitempty"`
		SubscriptionProxyEnv string                  `json:"subscription_proxy_env,omitempty"`
		RefreshSeconds       int                     `json:"subscription_refresh_seconds"`
		Accounts             map[string]accountRoute `json:"accounts,omitempty"`
	}
	accounts := make(map[string]accountRoute, len(c.Accounts))
	for id, override := range c.Accounts {
		accounts[id] = accountRoute{
			ProxyURL:  override.ProxyURL,
			ProxyURLs: slices.Clone(override.ProxyURLs),
			Direct:    override.Direct,
			RouteMode: override.RouteMode,
			FixedID:   override.FixedRouteID,
		}
	}
	payload, _ := json.Marshal(routeConfig{URLs: c.RouteURLs(), Direct: c.Direct, RouteMode: c.RouteMode, FixedRouteID: c.FixedRouteID, ProxyEnvs: c.ProxyEnvs, Subscriptions: c.Subscriptions, SubscriptionProxyEnv: c.SubscriptionProxyEnv, RefreshSeconds: c.SubscriptionRefreshSeconds, Accounts: accounts})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func validateSources(c *Config) error {
	// The UI submits empty arrays while persisted JSON omits empty optional
	// sources. They identify the same global source set. Account proxy_urls is
	// intentionally different: an explicit [] there clears inherited proxies.
	if len(c.ProxyEnvs) == 0 {
		c.ProxyEnvs = nil
	}
	if len(c.Subscriptions) == 0 {
		c.Subscriptions = nil
	}
	if len(c.ProxyEnvs) > 16 {
		return errors.New("proxy_envs must contain at most 16 entries")
	}
	for i, name := range c.ProxyEnvs {
		name = strings.TrimSpace(name)
		if !validEnvName(name) {
			return errors.New("proxy_envs contains an invalid environment variable name")
		}
		c.ProxyEnvs[i] = name
	}
	if c.SubscriptionProxyEnv != "" {
		c.SubscriptionProxyEnv = strings.TrimSpace(c.SubscriptionProxyEnv)
		if !validEnvName(c.SubscriptionProxyEnv) {
			return errors.New("subscription_proxy_env must be a valid environment variable name")
		}
	}
	if len(c.Subscriptions) > 16 {
		return errors.New("subscriptions must contain at most 16 entries")
	}
	for i := range c.Subscriptions {
		source := &c.Subscriptions[i]
		source.URL = strings.TrimSpace(source.URL)
		source.URLEnv = strings.TrimSpace(source.URLEnv)
		source.File = strings.TrimSpace(source.File)
		source.UserAgent = strings.TrimSpace(source.UserAgent)
		count := 0
		if source.URL != "" {
			count++
		}
		if source.URLEnv != "" {
			count++
		}
		if source.File != "" {
			count++
		}
		if count != 1 {
			return errors.New("each subscription must provide exactly one of url, url_env or file")
		}
		if source.URLEnv != "" && !validEnvName(source.URLEnv) {
			return errors.New("subscription url_env must be a valid environment variable name")
		}
		if source.URL != "" {
			if err := validateSubscriptionURL(source.URL); err != nil {
				return err
			}
		}
		if strings.ContainsRune(source.File, '\x00') || len(source.File) > 4096 {
			return errors.New("subscription file path is invalid")
		}
		if len(source.UserAgent) > 256 {
			return errors.New("subscription user_agent is too long")
		}
		if len(source.IncludeProtocols) > 16 || len(source.ExcludeKeywords) > 64 {
			return errors.New("subscription filters exceed their limits")
		}
		for j, protocol := range source.IncludeProtocols {
			protocol = strings.ToLower(strings.TrimSpace(protocol))
			if protocol == "" || len(protocol) > 32 {
				return errors.New("subscription include_protocols contains an invalid value")
			}
			source.IncludeProtocols[j] = protocol
		}
		for j, keyword := range source.ExcludeKeywords {
			keyword = strings.TrimSpace(keyword)
			if keyword == "" || len(keyword) > 128 {
				return errors.New("subscription exclude_keywords contains an invalid value")
			}
			source.ExcludeKeywords[j] = keyword
		}
	}
	return nil
}

func validEnvName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for index, r := range name {
		if index == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validateSubscriptionURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("subscription url must be an HTTPS URL or a literal loopback HTTP URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "https" {
		return nil
	}
	if scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return errors.New("subscription url must be an HTTPS URL or a literal loopback HTTP URL")
}

func normalizeProxyURLs(values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) > MaxRouteURLs {
		return nil, errors.New("proxy_urls must contain at most 256 entries")
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		canonical, err := normalizeProxyURL(raw)
		if err != nil {
			return nil, errors.New("proxy_urls contains an invalid HTTP, HTTPS, SOCKS5 or SOCKS5H URL")
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out, nil
}

func normalizeProxyURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Hostname() == "" || u.Fragment != "" || u.User != nil && u.User.Username() == "" {
		return "", errors.New("invalid proxy URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Path == "/" {
		u.Path = ""
	}
	switch u.Scheme {
	case "http", "https":
	case "socks5", "socks5h":
	default:
		return "", errors.New("unsupported proxy scheme")
	}
	return u.String(), nil
}
