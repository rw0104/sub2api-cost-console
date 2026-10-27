package service

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	officialOpenAIModelsURL = "https://api.openai.com/v1/models"
	officialCodexModelsURL  = "https://chatgpt.com/backend-api/codex/models"
)

type OfficialOpenAIModelMappingCheck struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Available bool   `json:"available"`
}

// OfficialOpenAIModelVerification contains only live, account-visible official
// model IDs. Suggested mappings are a preview; fetching never saves account data.
type OfficialOpenAIModelVerification struct {
	Models                []string                          `json:"models"`
	Metadata              map[string]UpstreamModelMetadata  `json:"metadata"`
	Source                string                            `json:"source"`
	FetchedAt             string                            `json:"fetched_at"`
	Checks                []OfficialOpenAIModelMappingCheck `json:"checks"`
	MissingModels         []string                          `json:"missing_models"`
	SuggestedModelMapping map[string]string                 `json:"suggested_model_mapping"`
}

// VerifyOfficialOpenAIModels fetches the official catalog and checks an account's
// mapping draft without registry enrichment, configured-model fallbacks, or writes.
// A nil mapping checks the saved mapping; a non-nil empty mapping checks an empty draft.
func (s *AccountTestService) VerifyOfficialOpenAIModels(ctx context.Context, account *Account, mapping map[string]string) (*OfficialOpenAIModelVerification, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, newUpstreamModelSyncConfigError("Upstream HTTP client is not configured", nil)
	}
	source, err := officialOpenAIModelsSource(account)
	if err != nil {
		return nil, err
	}

	// A redirected catalog must never be labeled as an official response.
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), codexModelsManifestRequestTimeout)
	defer cancel()
	req, err := s.buildOpenAIUpstreamModelsRequest(ctx, account)
	if err != nil {
		return nil, err
	}
	if !officialOpenAIModelsRequestMatchesSource(req.URL, source) {
		return nil, newUpstreamModelSyncConfigError("Official model verification requires an official OpenAI model list endpoint", nil)
	}
	body, err := s.readUpstreamModelsResponse(req, account)
	if err != nil {
		return nil, err
	}
	models, metadata, err := extractOfficialOpenAIModelCatalog(body, account.IsOpenAIOAuth())
	if err != nil {
		return nil, newUpstreamModelSyncUpstreamError("Official model list response was not a valid model catalog", err)
	}
	if len(models) == 0 {
		return nil, newUpstreamModelSyncUpstreamError("Official model list returned no model IDs", nil)
	}
	if mapping == nil {
		mapping = account.GetModelMapping()
	}
	result := compareOfficialOpenAIModelMapping(models, mapping)
	result.Metadata = metadata
	result.Source = source
	result.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	return result, nil
}

func officialOpenAIModelsSource(account *Account) (string, error) {
	if account == nil || !account.IsOpenAI() {
		return "", newUpstreamModelSyncUnsupportedError("Official model verification requires an OpenAI account", nil)
	}
	if account.IsOpenAIOAuth() {
		return officialCodexModelsURL, nil
	}
	if !account.IsOpenAIApiKey() {
		return "", newUpstreamModelSyncUnsupportedError("This OpenAI account type does not support official model verification", nil)
	}
	base, err := url.Parse(strings.TrimSpace(account.GetOpenAIFormatBaseURL()))
	if err != nil || !isStrictOfficialOpenAIURL(base, "api.openai.com") ||
		base.RawQuery != "" || base.ForceQuery ||
		(base.Path != "" && base.Path != "/" && base.Path != "/v1" && base.Path != "/v1/") {
		return "", newUpstreamModelSyncConfigError("Official model verification requires https://api.openai.com or https://api.openai.com/v1 as the account base URL", nil)
	}
	return officialOpenAIModelsURL, nil
}

func isStrictOfficialOpenAIURL(endpoint *url.URL, host string) bool {
	return endpoint != nil && strings.EqualFold(endpoint.Scheme, "https") &&
		strings.EqualFold(endpoint.Host, host) && endpoint.User == nil &&
		endpoint.Opaque == "" && endpoint.RawPath == "" && endpoint.Fragment == ""
}

func officialOpenAIModelsRequestMatchesSource(endpoint *url.URL, source string) bool {
	if source == officialOpenAIModelsURL {
		return isStrictOfficialOpenAIURL(endpoint, "api.openai.com") && endpoint.Path == "/v1/models" && endpoint.RawQuery == "" && !endpoint.ForceQuery
	}
	if source != officialCodexModelsURL || !isStrictOfficialOpenAIURL(endpoint, "chatgpt.com") || endpoint.Path != "/backend-api/codex/models" {
		return false
	}
	query, err := url.ParseQuery(endpoint.RawQuery)
	return err == nil && len(query) == 1 && len(query["client_version"]) == 1 && strings.TrimSpace(query.Get("client_version")) != ""
}

// Official responses have stable ID fields. Display names and generic provider
// name/model aliases must never serve as evidence that a model ID exists.
func extractOfficialOpenAIModelCatalog(body []byte, oauth bool) ([]string, map[string]UpstreamModelMetadata, error) {
	var envelope struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil, err
	}
	entries := envelope.Data
	if oauth {
		entries = envelope.Models
	}
	models := make([]string, 0, len(entries))
	metadata := make(map[string]UpstreamModelMetadata)
	for _, raw := range entries {
		var entry upstreamModelCapabilityEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		modelID := strings.TrimSpace(entry.ID)
		if oauth {
			modelID = strings.TrimSpace(entry.Slug)
		}
		if modelID == "" {
			continue
		}
		if _, duplicate := metadata[modelID]; duplicate {
			continue
		}
		model := upstreamMetadataFromCapabilityEntry(modelID, entry)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err == nil {
			model.CodexToolCapabilities = make(map[string]json.RawMessage)
			applyCodexToolCapabilities(model.CodexToolCapabilities, fields, true)
		}
		models = append(models, modelID)
		metadata[modelID] = model
	}
	return dedupeAndSortModelIDs(models), metadata, nil
}

func compareOfficialOpenAIModelMapping(models []string, mapping map[string]string) *OfficialOpenAIModelVerification {
	available := make(map[string]struct{}, len(models))
	for _, modelID := range models {
		available[modelID] = struct{}{}
	}
	result := &OfficialOpenAIModelVerification{
		Models:                models,
		Checks:                make([]OfficialOpenAIModelMappingCheck, 0, len(mapping)),
		MissingModels:         make([]string, 0),
		SuggestedModelMapping: make(map[string]string, len(mapping)),
	}
	keys := make([]string, 0, len(mapping))
	for from, to := range mapping {
		keys = append(keys, from)
		result.SuggestedModelMapping[from] = to
	}
	sort.Strings(keys)
	for _, from := range keys {
		to := mapping[from]
		_, found := available[strings.TrimSpace(to)]
		result.Checks = append(result.Checks, OfficialOpenAIModelMappingCheck{From: from, To: to, Available: found})
	}
	for _, modelID := range openai.RecommendedModelIDs() {
		if _, found := available[modelID]; !found {
			result.MissingModels = append(result.MissingModels, modelID)
			continue
		}
		if !mappingSupportsRequestedModel(mapping, modelID) {
			result.SuggestedModelMapping[modelID] = modelID
		}
	}
	return result
}
