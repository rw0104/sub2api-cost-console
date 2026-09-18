package service

import (
	"io"
	"net/http"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if s.pluginManager != nil {
		prepared, err := s.pluginManager.PreprocessOpenAI(request.Context(), request, account)
		if err != nil {
			return nil, err
		}
		request = prepared
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if s.pluginManager != nil {
		if s.pluginManager.hasProtectionTransport(account) && request.GetBody != nil {
			// Account tests create synthetic requests; their prepared body is the baseline.
			body, readErr := request.GetBody()
			if readErr != nil {
				return nil, readErr
			}
			raw, readErr := io.ReadAll(io.LimitReader(body, pluginv2.MaxRequestBodyBytes+1))
			_ = body.Close()
			if readErr != nil {
				return nil, readErr
			}
			request = request.WithContext(withPluginProtectionOriginal(request.Context(), account, raw))
		}
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	if useTLSFallback {
		return s.httpUpstream.DoWithTLS(
			request,
			proxyURL,
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}
