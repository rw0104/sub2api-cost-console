package service

import (
	"context"
	"io"
	"net/http"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// withProtectionOriginal 在构造出站请求前把“本次传输的比较基线”挂到 ctx 上。
//
// 保护传输插件用它判断宿主是否对请求做了有损改写（OriginalBodyJson），并据此推导
// 映射后模型与默认 instructions 摘要。基线必须取自“即将交给 buildUpstreamRequest*
// 的最终请求体”，并随请求上下文传递给 doOpenAIUpstream；不挂基线时，真实流量的
// OriginalBodyJson 为空，启用完整性检查的插件会把每个 /responses 请求判为拒绝。
// 账号未命中保护传输时原样返回 ctx，没有额外开销。
func (s *OpenAIGatewayService) withProtectionOriginal(ctx context.Context, account *Account, body []byte) context.Context {
	if s == nil || s.pluginManager == nil || !s.pluginManager.hasProtectionTransport(account) {
		return ctx
	}
	return withPluginProtectionOriginal(ctx, account, body)
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
		// Header observation is an independent, fail-open capability. It receives
		// only derived signals and cannot modify or deny this request.
		s.pluginManager.ObserveOpenAIHeaderProbe(request.Context(), request, account)
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
		s.pluginManager.ObserveOpenAIHeaderProbe(request.Context(), request, account)
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
