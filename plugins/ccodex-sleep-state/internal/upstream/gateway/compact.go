package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

const maxCompactBytes = 16 << 20

type compactError struct {
	status        int
	code, message string
}

func (e *compactError) Error() string { return e.code }

// CompactionInput and ResponsesApiRequest share their input, instructions,
// tools, reasoning, service_tier, cache key, text and access-program fields in
// official Codex rust-v0.154.0 codex-api/src/common.rs. Add only the Responses
// envelope and the protocol trigger; never synthesize a summary or ciphertext.
func bridgeCompactRequest(body []byte) ([]byte, error) {
	return bridgeCompactRequestLimit(body, maxRequestBytes)
}
func bridgeCompactRequestLimit(body []byte, limit int64) ([]byte, error) {
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil || request == nil {
		return nil, errors.New("远程压缩需要 JSON 对象")
	}
	var input []json.RawMessage
	if json.Unmarshal(request["input"], &input) != nil || input == nil {
		return nil, errors.New("远程压缩 input 必须是消息数组")
	}
	// A caller already carrying the official trigger must not receive two.
	if !remoteCompactionV2(body) {
		input = append(input, json.RawMessage(`{"type":"compaction_trigger"}`))
	}
	request["input"], _ = json.Marshal(input)
	request["stream"] = json.RawMessage("true")
	request["store"] = json.RawMessage("false")
	if _, ok := request["tool_choice"]; !ok {
		request["tool_choice"] = json.RawMessage(`"auto"`)
	}
	if _, ok := request["include"]; !ok {
		request["include"] = json.RawMessage(`["reasoning.encrypted_content"]`)
	}
	encoded, err := json.Marshal(request)
	if err != nil || int64(len(encoded)) > limit {
		return nil, fmt.Errorf("转换后的压缩请求超过 %d MiB 限制", limit>>20)
	}
	return encoded, nil
}

// bridgeCompactResponse waits for the complete upstream V2 result before
// returning the legacy JSON envelope. Errors never become fabricated success.
func (e *Engine) bridgeCompactResponse(resp *http.Response, s *session, route int) error {
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, e.config.CompactBytes()+1))
	if int64(len(data)) > e.config.CompactBytes() {
		return &compactError{502, "compact_response_too_large", fmt.Sprintf("远程压缩回复超过 %d MiB 限制；请求不会重放。", e.config.CompactBytes()>>20)}
	}
	finished, streamErr := probeStreamOutcome(data)
	if streamErr != nil {
		var failure *probeStreamError
		if errors.As(streamErr, &failure) {
			s.mu.Lock()
			s.diagnostic = failure.kind
			s.mu.Unlock()
			if failure.status != 0 {
				e.reject(s, failure.status, retryDelay(resp.Header.Get("Retry-After")), route)
			}
			status := 502
			if failure.kind == "model_capacity" {
				status = 503
			}
			if failure.status != 0 {
				status = failure.status
			}
			return &compactError{status, "compact_" + failure.kind, diagnosticMessage(failure.kind, s.policy, 0)}
		}
	}
	if readErr != nil || !finished {
		return &compactError{502, "compact_incomplete", "远程压缩回复未完整结束，未返回伪造的压缩结果；请求不会重放。"}
	}
	output, err := compactOutput(data)
	if err != nil {
		return &compactError{502, "compact_invalid_output", "上游未返回完整有效的 compaction 项，不能完成远程压缩；请求不会重放。"}
	}
	body, err := json.Marshal(struct {
		Object string            `json:"object"`
		Output []json.RawMessage `json:"output"`
	}{"response.compaction", output})
	if err != nil || len(body) > maxCompactBytes {
		return &compactError{502, "compact_response_too_large", "转换后的远程压缩结果超过大小限制；请求不会重放。"}
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.TransferEncoding = nil
	resp.Trailer = nil
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Transfer-Encoding")
	resp.Header.Del("Trailer")
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Set("Cache-Control", "no-store")
	resp.Header.Set("X-Sleep-State-Compaction", "v1-to-v2")
	return nil
}

func compactOutput(data []byte) ([]json.RawMessage, error) {
	var emitted, output []json.RawMessage
	invalid := false
	walkSSE(data, func(name string, payload []byte) {
		var event struct {
			Type     string          `json:"type"`
			Item     json.RawMessage `json:"item"`
			Response struct {
				Status string            `json:"status"`
				Output []json.RawMessage `json:"output"`
			} `json:"response"`
		}
		if json.Unmarshal(payload, &event) != nil {
			return
		}
		kind := event.Type
		if kind == "" {
			kind = name
		}
		switch kind {
		case "response.output_item.done":
			if len(event.Item) > 0 {
				emitted = append(emitted, event.Item)
			}
		case "response.completed":
			if event.Response.Status != "" && event.Response.Status != "completed" {
				invalid = true
			}
			output = event.Response.Output
		}
	})
	if len(output) == 0 {
		output = emitted
	}
	compactions := 0
	for _, raw := range output {
		var item struct {
			Type      string `json:"type"`
			Encrypted string `json:"encrypted_content"`
		}
		if json.Unmarshal(raw, &item) != nil {
			invalid = true
			continue
		}
		if item.Type == "compaction" {
			compactions++
			if item.Encrypted == "" {
				invalid = true
			}
		}
	}
	if invalid || compactions != 1 {
		return nil, errors.New("invalid compaction output")
	}
	return output, nil
}
