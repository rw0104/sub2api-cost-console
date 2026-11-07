//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type voiceAdmissionRepo struct {
	openAIImagesFailoverAccountRepo
	blocked map[int64]bool
}

func (r *voiceAdmissionRepo) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	a, err := r.openAIImagesFailoverAccountRepo.GetByID(ctx, id)
	if err != nil || a == nil {
		return a, err
	}
	copy := *a
	if r.blocked[id] {
		until := time.Now().Add(time.Hour)
		copy.RateLimitResetAt = &until
	}
	return &copy, nil
}

type voiceAdmissionSlots struct {
	testutil.StubConcurrencyCache
	acquired     int
	released     int
	afterAcquire func(int, int64)
}

func (s *voiceAdmissionSlots) AcquireAccountSlot(_ context.Context, id int64, _ int, _ string) (bool, error) {
	s.acquired++
	s.afterAcquire(s.acquired, id)
	return true, nil
}
func (s *voiceAdmissionSlots) ReleaseAccountSlot(context.Context, int64, string) error {
	s.released++
	return nil
}

type voiceAdmissionTransport struct {
	service.HTTPUpstream
	calls int
}

func (u *voiceAdmissionTransport) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls++
	return nil, errors.New("unexpected voice dispatch")
}

func TestGrokVoiceAdmission_RuntimeVetoAndCancellationNeverDispatch(t *testing.T) {
	for _, cancelSecond := range []bool{false, true} {
		t.Run(map[bool]string{false: "all vetoed returns 503", true: "cancelled second admission stops after response"}[cancelSecond], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			accounts := make([]service.Account, 4)
			for i := range accounts {
				accounts[i] = service.Account{ID: int64(i + 1), Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: i,
					GroupIDs: []int64{24}, Credentials: map[string]any{"api_key": "synthetic"}}
			}
			repo := &voiceAdmissionRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts}, blocked: map[int64]bool{}}
			slots := &voiceAdmissionSlots{afterAcquire: func(attempt int, id int64) {
				if cancelSecond && attempt == 2 {
					cancel()
					return
				}
				repo.blocked[id] = true
			}}
			concurrency := service.NewConcurrencyService(slots)
			transport := &voiceAdmissionTransport{}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, &testutil.StubGatewayCache{}, cfg, nil, concurrency, nil, nil, nil, transport, nil, nil, nil, nil, nil, nil, nil, nil)
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/custom-voices", strings.NewReader(`{}`)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			groupID := int64(24)
			c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 20, UserID: 10, GroupID: &groupID,
				Group: &service.Group{ID: 24, Platform: service.PlatformGrok}, User: &service.User{ID: 10}})
			h.GrokVoice(c, "custom-voices")
			require.Zero(t, transport.calls)
			require.Equal(t, slots.acquired, slots.released)
			require.True(t, json.Valid(w.Body.Bytes()), "only one complete error response may be written")
			if cancelSecond {
				require.Equal(t, 499, w.Code, w.Body.String())
				require.Equal(t, 2, slots.acquired, "must stop after the written cancellation response")
			} else {
				require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
				require.Equal(t, 4, slots.acquired, "bounded runtime veto exhaustion must be an explicit error")
			}
		})
	}
}
