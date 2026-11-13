package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIUltraRequestWithoutGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		for _, tc := range []struct {
			model, resolved, want string
		}{
			{"gpt-6.1-sol", "", "xhigh"},
			{"custom-model", service.PlatformOpenAI, "xhigh"},
			{"grok-4.3", "", "ultra"},
			{"gpt-6.1-sol", service.PlatformGrok, "ultra"},
			{"k3", service.PlatformKimi, "ultra"},
		} {
			t.Run(path+"/"+tc.model+"/"+tc.resolved, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", path, nil)
				if tc.resolved != "" {
					c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), tc.resolved))
				}
				field := "reasoning.effort"
				body := []byte(`{"model":"` + tc.model + `","reasoning":{"effort":"ultra"}}`)
				if path == "/v1/chat/completions" {
					field = "reasoning_effort"
					body = []byte(`{"model":"` + tc.model + `","reasoning_effort":"ultra"}`)
				}
				got, changed, err := applyOpenAIReasoningEffortPolicyForRequest(c, &service.APIKey{}, body)
				require.NoError(t, err)
				require.Equal(t, tc.want == "xhigh", changed)
				require.Equal(t, tc.want, gjson.GetBytes(got, field).String())
			})
		}
	}
}
