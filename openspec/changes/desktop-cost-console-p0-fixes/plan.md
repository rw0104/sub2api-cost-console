# 桌面端账号与成本控制台问题排查及修复规划

日期：2026-09-25

## 背景

本规划基于历史任务 `codex://threads/01a0d748-544d-7fb0-92dd-8eae7b56ef04`、当前仓库 `AGENTS.md` 以及桌面端截图。历史任务确认桌面端通过 Tauri 管理本地 Go sidecar；本次问题发生在同一套账号管理与成本控制台 UI 中。

## 问题与根因

### P0：失效账号点击“次数”导致管理员退出

OpenAI OAuth 账号的“次数”按钮调用 `POST /admin/openai/accounts/:id/quota/refresh`。账号 access token 失效时，上游返回 401；`OpenAIQuotaService.mapUpstreamStatus` 原样透传 401。前端 Axios 全局拦截器把所有非登录接口的 401 当作管理员会话失效，刷新失败后清除 `auth_token` 并跳转登录页。

修复边界：账号上游认证失败属于账号数据错误，必须映射为 502，并保留 `OPENAI_QUOTA_UPSTREAM_ERROR` / `OPENAI_QUOTA_RESET_UPSTREAM_ERROR` 业务码；管理员 JWT 401 仍保持原有语义。该映射同时覆盖共享同一函数的 Grok 配额探测。

### 观察窗口粒度与控件不一致

成本总览和模型成本区各自维护一份原生 `<select>`，运维面板使用另一套 `Select` 与另一组时间值。窗口值虽然部分同名，实际可用范围和端点能力不同，容易造成用户以为请求口径一致。

修复边界：建立可复用的 `TimeRangeSelect`，统一成本控制台总览/模型窗口、Ops 头部、告警、系统日志、Token 统计和审计页面的时间控件交互；各后端端点仍保留自己支持的窗口目录。成本窗口增加 15 分钟档，并让查询构造继续携带显式窗口边界，避免滚动窗口由服务端当前时间漂移。秒级窗口需要后端趋势聚合协议另行扩展，本次不伪造秒级精度。

### 成本总览黄色“部分可用”

`TruthfulMetric` 的黄色状态来自 `sourceStates.dashboard === 'partial'`。旧版 dashboard trend 没有 `account_cost` 字段，前端因此分页读取 `usage_logs` 兼容聚合；兼容聚合完整成功时数值是完整的，但状态仍被标成 partial。后端已有迁移为小时/天聚合表准备 `account_cost`，只是趋势 DTO、查询和扫描漏掉了该列。

修复边界：补齐后端 `TrendDataPoint.account_cost`、原始/小时/天趋势 SQL 和扫描逻辑；前端类型同步。兼容聚合完整成功时标记为 measured，并保留来源原因，只有截断或失败才显示 partial/stale。采购档案默认价和经济预测的黄色“估算/部分可用”属于真实数据质量提示，继续保留。

账号成本字段只允许管理员成本中心读取。用户侧 dashboard trend/snapshot 使用无账号成本 DTO 投影，避免为了修复管理员成本而扩大普通用户数据权限。滚动窗口只有在 UTC 整桶边界才允许读取小时/天预聚合；1m/5m 强制使用 usage_logs 的 5 秒/15 秒桶，避免出现“窗口标签更细但数据仍是整分钟”的假精度。

## 实施任务

1. 对 `mapUpstreamStatus`、dashboard snapshot、trend scan 和 cost-center loader 做 GitNexus 影响分析；记录账号配额路径的 MEDIUM 风险。
2. 将账号配额上游 401/403 映射为 502，补服务层表驱动回归测试。
3. 新增 `TimeRangeSelect` 与共享成本窗口选项，迁移成本总览、模型成本和运维面板时间控件；补 15 分钟窗口的范围、桶宽、标签和查询测试。
4. 补齐 Go/TypeScript 趋势 `account_cost` 字段、SQL、扫描和 sqlmock 行；验证旧接口兼容聚合的 measured 状态。
5. 运行前端成本中心相关 Vitest、客户端认证回归测试，以及后端 service/repository/handler 测试；运行 `detect_changes` 检查受影响执行流。

## 验收标准

- 失效 OpenAI/Grok 账号点击次数或配额刷新后，管理员仍停留在当前页面，账号卡显示可重试的上游错误。
- 管理员自身 JWT 失效仍按原流程跳转登录。
- 成本总览与模型成本使用同一时间控件和同一选项目录；15 分钟窗口请求边界与 UI 口径一致。
- dashboard trend 直接返回 `account_cost` 时，总览收入、账号成本、毛利和每小时运行成本不再因为兼容聚合显示黄色“部分可用”。
- 用户 dashboard trend/snapshot 响应不包含 `account_cost`。
- 1 小时与 6 小时选择保持独立，1m/5m 图表的实际桶宽分别为 5 秒/15 秒。
- 兼容聚合失败、读取截断、默认采购价和预测样本不足仍明确显示对应状态。

## 风险与回滚

账号配额状态映射会影响 OpenAI 与 Grok 的管理探测调用，风险为 MEDIUM；不改变网关对终端用户请求的上游状态映射。趋势 DTO 增加 JSON 字段向后兼容旧客户端；若旧内核仍缺字段，前端保留完整 usage_logs 回退路径。所有变更均可通过回退对应提交恢复。
