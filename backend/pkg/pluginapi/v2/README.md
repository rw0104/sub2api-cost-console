# Sub2API 通用用户态插件接口 v2

`v2` 是面向通用用户态扩展的公共契约层。它与现有 `v1` OpenAI OAuth 出站传输协议并行存在，当前提交定义协议、能力模型和请求预处理语义，尚未替换宿主现有路由。

## 能力模型

插件通过 `Capability` 声明可以提供的能力。每个能力具有独立 ID、类型、权限、超时和失败策略。宿主应先校验声明，再根据能力 ID 和请求作用域建立路由。

首个同步能力是 `request.preprocess.v1`：

1. 宿主发送经过脱敏的 `RequestContext` 和可选 JSON 请求体；
2. 插件返回 `pass`、`modify`、`deny` 或 `error`；
3. `modify` 只能通过 `RequestPatch` 修改允许的请求字段；
4. 认证、账号选择、计费、持久化和最终重试决策仍由宿主负责。

插件不得接收 `authorization`、`proxy-authorization`、`cookie` 或 `set-cookie` 请求头。需要秘密时，后续版本应通过明确授权的 Secret Broker 能力提供短时凭据。

## 协议文件

- [`extension.proto`](./extension.proto)：gRPC 服务和线协议定义；
- [`contract.go`](./contract.go)：宿主和插件适配器可以直接依赖的 Go 类型、验证器和接口；
- [`manifest.schema.json`](./manifest.schema.json)：通用能力清单的 v2 Schema；
- [`contract_test.go`](./contract_test.go)：能力、敏感字段和决策语义的契约测试。

生成 gRPC 桩代码时，应使用仓库锁定的 `protoc`、`protoc-gen-go` 和 `protoc-gen-go-grpc` 版本，并把生成结果提交到本目录。当前版本刻意不提交手写生成代码，避免生成器版本不一致造成协议描述漂移。

## 兼容策略

- `backend/pkg/pluginapi/v1` 继续服务现有 `TransportPlugin` 插件；
- `v2` 能力按能力 ID 单独版本化，例如 `request.preprocess.v1`；
- 宿主遇到未知能力时应标记为不兼容，而不是仅凭清单字段启用；
- v2 接入宿主时应先增加能力路由和适配器，再逐步把业务入口接到 `ExtensionDispatcher`。
