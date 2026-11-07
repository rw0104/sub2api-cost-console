//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Codex 0.167 enables deferred_tool_world_state by default. Exercise the
// production forwarding boundary with both top-level and additional tools,
// rather than only testing the individual normalization helpers.
func TestCodex0167OAuthDeferredToolWorldStateSurvivesForwarding(t *testing.T) {
	for _, lite := range []bool{false, true} {
		t.Run(fmt.Sprintf("responses-lite=%t", lite), func(t *testing.T) {
			body := []byte(`{
				"model":"gpt-6.1-terra","stream":false,"instructions":"client instructions",
				"tools":[
					{"type":"tool_search","execution":"client"},
					{"type":"namespace","name":"repo","tools":[
						{"type":"function","name":"lookup","defer_loading":true,"parameters":{"type":"object","properties":{}}}
					]}
				],
				"input":[
					{"type":"message","role":"user","content":"find the relevant file"},
					{"type":"additional_tools","role":"developer","tools":[
						{"type":"namespace","name":"files","tools":[
							{"type":"function","name":"read","defer_loading":true,"parameters":{"type":"object","properties":{}}}
						]}
					]}
				]
			}`)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				newOpenAIRejectedFieldTestResponse(http.StatusOK, namespaceForwardOKResponse),
			}}
			c := newOpenAIRejectedFieldTestContext(body)
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.167.0")
			if lite {
				c.Request.Header.Set(responsesLiteHeader, "true")
			}
			result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
				context.Background(), c, newOpenAIOAuthNamespaceTestAccount(), body,
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 1)
			forwarded := upstream.bodies[0]
			require.Equal(t, "client", gjson.GetBytes(forwarded, `tools.#(type=="tool_search").execution`).String())
			declarations := gjson.GetBytes(forwarded, "tools").Array()
			for _, item := range gjson.GetBytes(forwarded, "input").Array() {
				if item.Get("type").String() == "additional_tools" {
					declarations = append(declarations, item.Get("tools").Array()...)
				}
			}
			namespaces := make(map[string]gjson.Result)
			for _, declaration := range declarations {
				if declaration.Get("type").String() == "namespace" {
					name := declaration.Get("name").String()
					_, duplicate := namespaces[name]
					require.False(t, duplicate, "each namespace must be declared once")
					namespaces[name] = declaration
				}
			}
			require.Len(t, namespaces, 2)
			for namespace, tool := range map[string]string{"repo": "lookup", "files": "read"} {
				require.Equal(t, tool, namespaces[namespace].Get("tools.0.name").String())
				require.True(t, namespaces[namespace].Get("tools.0.defer_loading").Bool())
				require.Equal(t, "object", namespaces[namespace].Get("tools.0.parameters.type").String())
			}
		})
	}
}

// 0.167 retains incomplete function calls for failure traces. The gateway must
// preserve their status and partial arguments, without manufacturing a completed
// event or replaying a response that already contains output and billable usage.
func TestCodex0167IncompleteToolOutputAndUsageSurviveForwarding(t *testing.T) {
	const partialCall = `{"type":"function_call","status":"incomplete","id":"fc_attempt","call_id":"attempt","name":"lookup","arguments":"{\"path\":"}`
	const terminal = `{"type":"response.incomplete","response":{"id":"resp_0167","object":"response","status":"incomplete","incomplete_details":{"reason":"invalid_recipient"},"output":[` + partialCall + `],"usage":{"input_tokens":17,"output_tokens":3,"input_tokens_details":{"cached_tokens":5}}}}`
	sse := "event: response.incomplete\ndata: " + terminal + "\n\n"
	for _, passthrough := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/stream=%t", passthrough, stream), func(t *testing.T) {
				request := fmt.Sprintf(`{"model":"gpt-6.1-terra","instructions":"client instructions","input":"look up a file","stream":%t}`, stream)
				rec, upstream, result := runOpenAIOAuthNonStreamRegression(t, passthrough, request, sse)
				require.Len(t, upstream.bodies, 1, "incomplete output must not be replayed")
				require.Equal(t, 17, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				require.Equal(t, 5, result.Usage.CacheReadInputTokens)
				response := gjson.Parse(rec.Body.String())
				if stream {
					var terminalCount int
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						payload, ok := strings.CutPrefix(line, "data: ")
						if !ok {
							continue
						}
						event := gjson.Parse(payload)
						require.NotEqual(t, "response.completed", event.Get("type").String())
						if event.Get("type").String() == "response.incomplete" {
							terminalCount++
							response = event.Get("response")
						}
					}
					require.Equal(t, 1, terminalCount)
				}
				require.Equal(t, "incomplete", response.Get("status").String())
				require.Equal(t, "invalid_recipient", response.Get("incomplete_details.reason").String())
				require.JSONEq(t, partialCall, response.Get("output.0").Raw)
				require.Equal(t, int64(17), response.Get("usage.input_tokens").Int())
			})
		}
	}
}
