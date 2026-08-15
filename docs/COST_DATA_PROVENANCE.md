# 成本控制台数据真实性与口径

本文记录成本控制台每类指标的真实数据源、计算公式、保留范围和已知限制。界面不得把预测值、默认值或状态推导值描述为实测数据。

## 1. 数据来源矩阵

| 指标 | 来源 | 类型 | 说明 |
| --- | --- | --- | --- |
| 请求数、Token | PostgreSQL `usage_logs` | 实测 | 由 Sub2API 完成请求后写入；观察窗口按真实时间范围聚合 |
| 请求/发往上游/响应声明模型 | `requested_model`、`upstream_model`、`upstream_response_model` | 实测 | 区分客户端意图、本站映射结果和上游自报模型；`upstream_model_mismatch` 为三态审计结果 |
| API 账号实算成本 | `COALESCE(account_stats_cost, total_cost) × account_rate_multiplier` / 账号今日统计 | 实测 | 日志含账号定价快照时使用实算值；旧快照缺失时回退标准成本 |
| API 产出 | `usage_logs.actual_cost` / `user_cost` | 实测 | 用户实际计费金额；值为 `0` 时不会再回退成标准成本 |
| TTFT、成功、失败、切号 | Ops 监控 | 实测 | Ops 未启用时显示不可用，不再用账号状态冒充请求事件 |
| 单账号探测耗时 | `/admin/accounts/:id/test` SSE | 实测 | 会发送一次真实最小上游请求，展示端到端完成耗时，并可能产生少量调用成本 |
| 采购小时费率 | `account.extra.cost_profile` | 确定性计算 | 按用户填写金额与计费周期折算 |
| 累计采购成本 | 成本档案、起算时间与当前时间 | 确定性计算 | `hourly_rate × elapsed_hours`；账号加入即起算 |
| 账号封禁净损失 | `account_cost_loss_events` | 后端确认事件 + 确定性计算 | 仅接受终局原因；当前预付周期未摊销余额减退款与恢复冲销 |
| 经济总成本 | 累计采购成本 + 账号封禁净损失 | 确定性计算 | 终局时停止该账号继续累计；已删除账号仍按快照保留 |
| 预计月采购 | 当前筛选号池的小时采购费率 × 730 | 确定性预测 | 是当前现存账号合计，不是单账号值 |
| 经济运行样本 | `account_economics_samples` | 累计观察 | 每分钟保存累计 USD 产出、账号成本、成员版本和健康数量；不是第二套账本 |
| API 产出速率 | 相邻稳定样本累计差值 ÷ 区间小时数 | 预测输入 | 成员变化、累计倒退或样本不足时返回不可用，不以当天已过小时代替采样窗口 |
| 单位经济性 | `usage_logs` + 成本档案 + 损失账本 + 明确汇率 | 派生 | 输出每 1 USD 产出的采购成本、贡献结果、回本比例和预测置信度 |
| 滚动平均 | 真实时间桶的移动平均 | 派生 | 只用于平滑趋势，界面明确标记为滚动平均 |
| 剩余预期 | 持久化稳定区间速率线性外推 | 预测 | 至少两个稳定区间；不足时显示“待采样”，不是已经发生的账单 |
| 默认套餐成本 | 美国官方公开订阅价的版本化快照 | 默认估算 | 没有自定义采购价时使用；用户实际采购账单可逐账号覆盖 |
| CNY/USD 换算 | Frankfurter USD/CNY 汇率、12 小时本地缓存 | 联网参考 | 网络源和有效缓存都不可用时才回退到 `1 USD = 7.2 CNY` |

## 2. 当前号池与历史数据

- “当前号池”来自当前仍存在的账号记录，并按所选渠道/账号类型过滤。
- 删除账号后，手动刷新会立即将其移出当前号池；开启自动刷新时最长等待 30 秒。
- 删除账号不会删除 `account_cost_loss_events`；成本中心的经济总成本仍包含其最新终局快照。
- 已经写入的 `usage_logs` 不会因为账号删除而删除，所以全局历史趋势仍会包含旧账号曾经产生的请求。
- OAuth 页的账号数、采购成本、今日产出与预计月采购只汇总当前平台的现存 OAuth 账号。
- 全局历史趋势明确标记为 `usage_logs` 范围，可能包含已经删除的账号。
- “模型成本分析”不是当前号池支持模型清单，而是所选时间窗口内真实发生过的模型调用历史；一天内调用多个模型会分别成行。选择“全部账号历史”时会包含已删除账号，选择具体账号时只统计该账号。
- 当前池只有 DeepSeek 时仍可能看到窗口内此前真实调用的 GPT。模型表必须显示首次/最近调用时间；“仅看不一致”只筛选已确认的上游响应模型替换，不把旧日志或无模型声明的 `NULL` 当成一致。

## 3. 时间窗口

成本控制台支持：

- 最近 1、5、30 分钟：按分钟聚合。
- 最近 1、6、24 小时：按小时聚合。
- 最近 7 天、1 个月：按天聚合。
- 当天：成本中心使用 `Asia/Shanghai`（北京时间）自然日边界，不等同于滚动 24 小时。

短窗口优先通过 `time_range` 传入精确范围，不再用“当天日期范围”近似一小时数据。官方上游内核若尚未支持 `start_time/end_time` 与分钟桶，桌面前端会按日期分页读取真实 `usage_logs`，再按记录时间过滤并聚合；最多读取 25,000 条，超过上限会停止并提示，不会把部分结果冒充完整窗口。Dashboard snapshot 缓存仍为 30 秒。

## 4. 默认成本与算法版本

当前成本算法版本为 `1.6.0`，经济预测版本为 `1.0.0`。固定订阅采购、终局封禁损失与 API 按量成本继续分开计算；1.6 未调整 Sub2API Token 输入、输出、缓存公式或模型价格：

```text
fixed_hourly_rate = amount / billing_cycle_hours
fixed_accrued_cost = fixed_hourly_rate × max(0, now - started_at) / 3,600,000

metered_model_cost = input_tokens × input_price
                   + cache_read_tokens × cache_read_price
                   + cache_creation_tokens × cache_creation_price
                   + output_tokens × output_price
metered_account_cost = metered_model_cost × account_rate_multiplier
combined_cost = fixed_accrued_cost + metered_account_cost

terminal_unamortized = current_prepaid_cycle_price - accrued_in_current_cycle
terminal_net_loss = max(0, terminal_unamortized - refund - reversal)
economic_total = fixed_accrued_cost_at_terminal + terminal_net_loss + metered_account_cost

stable_output_rate = Σ(stable_sample_output_delta) / Σ(stable_interval_hours)
stable_account_cost_rate = Σ(stable_sample_account_cost_delta) / Σ(stable_interval_hours)
```

`1.6.0` 新增持续经济采样和账号池单位经济性 Module。累计样本只来自现有 `usage_logs`，成员哈希或账号数变化、累计值倒退、间隔过短都会重置相邻区间；至少两个稳定区间才返回速率。样本不足时 API 返回 `null`，界面显示“待采样”。采购档案保持原币种，只有明确的 CNY/USD 汇率边界执行换算。详细设计与部署方式见[成本经济算法 1.6 开发与部署说明](2026-08-11_cost-economics-1.6-development.md)。

`1.5.0` 新增独立的深层“账号成本损失” Module。它的 Interface 只接受后端已经确认的终局事件：OpenAI `token_revoked` / `token_invalidated`、明确永久 Unauthorized、OAuth 缺少 refresh token、`deactivated_workspace`，以及管理员确认。普通 OAuth 401 仍进入临时不可调度，泛化 402 仍只停用账号，二者都不会仅凭 HTTP 状态推导封禁损失。账号恢复、供应商退款只追加冲销事件，不修改原终局事件；幂等键负责去重。适用范围仅为本地采购的 OAuth / Setup Token，CRS 导入、中转池、影子账号、自定义中转、API Key、Upstream、Bedrock 与 Service Account 均排除。

`1.4.0` 仅对 OAuth / Setup Token 订阅账号应用美国默认月价；API Key、Service Account、Bedrock 与中转账号默认按 usage 和模型价格自动计算，不要求用户填写采购金额。中转渠道优先使用渠道自定义价格，没有渠道价时再使用模型目录价和账号倍率；缺少 usage 或价格时明确标记，不伪造成本。价格目录每 24 小时自动同步一次，远程目录优先；仓库内按官方页面核验的条目只在远程缺少模型或断网时兜底，不能阻止后续自动更新。

`1.3.1` 将实时总览、模型成本与真实路由的默认窗口统一为最近 1 小时。模型统计必须带有可信的起止时间；旧内核未返回精确边界时，桌面端从真实 `usage_logs` 按时间和账号重新聚合，避免把全历史误标为当前窗口。每个模型及请求路由同时展示首次和最近调用时间，用户可区分一天内先后使用的多个模型。趋势时间轴会补齐没有请求的真实零桶；移动平均只作为明确标记的派生曲线，不改变请求数、Token 或成本总额。

`1.4.0` 随 Sub2API `0.1.172` 增加上游响应模型审计。模型分析现在可按用户请求、实际发往上游、上游响应声明及请求到上游映射四种口径分组，并统计一致、不一致和未观测请求。切换分组维度不会重新查价或回写历史金额；`total_cost`、`account_stats_cost` 与 `actual_cost` 始终使用请求发生时保存的快照。完整语义见[上游响应模型审计与成本归因](UPSTREAM_RESPONSE_MODEL_AUDIT.md)。

`1.4.0` 沿用以下美国默认月价快照（核对日期：2026-08-07）：

| 套餐 | 默认值 | 官方依据与边界 |
| --- | ---: | --- |
| Plus | USD 20 / 月 | [OpenAI Plus 官方说明](https://help.openai.com/en/articles/6950777-chatgpt-plus) |
| Pro | USD 100 / 月起 | [OpenAI 发布说明](https://help.openai.com/en/articles/6825453-chatgpt-release-notes)；最高用量方案可为 USD 200，用户应按实际账单覆盖 |
| Business / 旧 Team | USD 25 / 席位 / 月 | [OpenAI Business 官方说明](https://help.openai.com/en/articles/8792828-what-is-chatgpt-business)；年付折算为 USD 20，至少 2 席，均应按实际采购覆盖 |
| 美国 K-12 教师 | USD 0 | [OpenAI Teachers 官方说明](https://help.openai.com/en/articles/12844995-chatgpt-for-teachers)；仅适用于通过验证且在官方免费期内的美国 K-12 教育工作者 |

这些值只用于固定订阅采购，不是 API Token 价格，也不会覆盖用户保存的自定义成本。美元兑人民币使用联网参考汇率并缓存 12 小时；联网与缓存都不可用时才回退到 `1 USD = 7.2 CNY`。后续修改计费模式、默认价格、730 小时、汇率或起算边界时，必须提升 `ALGORITHM_VERSION`。

## 5. 不属于生产数据的测试路径

上游内核保留一个仅供隔离 UI 测试数据集使用的 `synthetic_ui_test` 标记。只有账号 `extra.synthetic_ui_test === true` 时才会走该测试分支；普通用户账号不会自动获得此标记，成本控制台也不会创建这种账号。
