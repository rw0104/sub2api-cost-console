package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRefreshCachedOpenAIModelsRechecksFreshnessAfterCallerMiss(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cachedAge time.Duration
		seed      bool
		wantCalls int32
		wantETag  string
	}{
		{name: "another flight populated fresh data", seed: true},
		{name: "stale data still revalidates", seed: true, cachedAge: 2 * time.Minute, wantCalls: 1, wantETag: `"upstream-v1"`},
		{name: "expired data still fetches", seed: true, cachedAge: 6 * time.Minute, wantCalls: 1},
		{name: "cache remains missing", wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &OpenAIGatewayService{}
			request := openAIModelsRequest{url: "https://models.example/v1/models", accountID: 2, credentialAccountID: 2, standardModelsList: true}
			key := buildOpenAIModelsCacheKey(request)
			_, initialState := s.openAIModelsCache.get(key, time.Now())
			require.Equal(t, openAIModelsCacheMiss, initialState)
			cached := &OpenAIModelsResponse{Body: []byte(`{"data":[{"id":"cached-model"}]}`), upstreamETag: `"upstream-v1"`}
			if tc.seed {
				// Model a prior worker finishing between the caller's miss and its
				// registration of a new flight. No scheduler sleeps are necessary.
				s.openAIModelsCache.set(key, cached, time.Now().Add(-tc.cachedAge))
			}
			upstream := &OpenAIModelsResponse{Body: []byte(`{"data":[{"id":"new-model"}]}`)}
			var calls atomic.Int32
			var requestedETag string
			result := <-s.refreshCachedOpenAIModels(key, request, func(_ context.Context, etag string) (*OpenAIModelsResponse, error) {
				calls.Add(1)
				requestedETag = etag
				return upstream, nil
			})
			require.NoError(t, result.Err)
			require.Equal(t, tc.wantCalls, calls.Load())
			require.Equal(t, tc.wantETag, requestedETag)
			if tc.wantCalls == 0 {
				require.Same(t, cached, result.Val)
			} else {
				require.Same(t, upstream, result.Val)
			}
		})
	}
}
