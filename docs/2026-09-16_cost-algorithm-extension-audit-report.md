# 成本算法与扩展审查及修复记录

审查基线：桌面 v0.2.37，Git `e7db8172a`（发布业务源码 `e28d98734`）。日期：2026-09-16。

审计确认的 F-01 至 F-05 均已实现修复，原失败用例已转为默认回归测试。算法版本为 1.6.1，成本扩展为 1.1.2，配套桌面源码版本为 0.2.38。没有读取或修改生产账单，以下金额均为合成测试样例。本文保留原始发现，并记录修复方案、验证证据与 Git 推送结果。

## 范围与方法

- 前端：成本档案、按量/订阅类型、起算时间、观察窗口、成本中心汇总及经济接口参数。
- 后端：固定采购聚合、终局损失事件、幂等键、退款与恢复冲销、经济快照和样本计算。
- 数据边界：管理员保存的成本配置 → 后端事件/快照 → 前端成本与回本展示。相关 HTTP 路由位于管理员认证和审计中间件之后。
- 方法：定向文本扫描、调用链人工审查、前端真实纯函数测试、后端服务层测试，以及现有仓储 SQL mock 测试。未运行面向注入类问题的全仓库 SAST，也未覆盖所有上游模型的 Token 计费、实际支付和生产数据库对账。

## 修复实现

| 问题 | 已实现行为 | 关键验证 |
| --- | --- | --- |
| F-01 | 同账号终局、退款和恢复共用事务级 advisory lock；同一有效生命周期复用首次终局，只有恢复事件开启新生命周期 | 并发 8 个不同请求键只生成一个终局；730−30 后重复确认仍为 700 |
| F-02 | 自定义固定费按所有账号类型计入；终局损失资格单独判断 | API Key 的 100 元附加费在窗口、累计和经济成本中一致 |
| F-03 | 客户端提交明确起止时间及时区，后端校验并将未来结束时间截到现在；月份按客户端时区统计 | 自然日中午只计 12 小时，夏令时切换日为 11 / 13 小时，午夜为 0 |
| F-04 | 未配置固定费的按量账号默认金额为 0；保留显式固定费及 OAuth / Setup Token 默认订阅价 | API Key 带 pro 标记仍为 0；共享样例核对美元/人民币金额 |
| F-05 | 未来档案的累计金额和当前小时费率同时为 0 | 起算日前当前费率从 1 修正为 0 |

终局默认键由数据库在锁内以恢复事件 ID 确定，不依赖账号 `updated_at`。退款余额检查覆盖同一未恢复生命周期中的全部有效重复记录，幂等请求键还校验账号、事件类型和源事件，防止将不同调整误当作成功重放。

旧重复事件不做破坏性清理：读取时合并同账号有效终局，以首次事件冻结累计成本，保留所有有效退款；恢复时在一个事务中对其全部有效终局追加冲销。原始事件 ID、金额、成本档案与历史算法版本继续保留，无新增数据库结构迁移。

时间参数保持兼容：只传 `window_hours` 的调用仍可用；新版客户端额外提供 `start_time`、`end_time`、`timezone`，后端返回实际 `window_start/window_end/timezone`。明确时间边界优先于持续时长，拒绝非法范围、无效时区及非有限数值。

前后端共同读取 `backend/internal/service/testdata/cost_accounting_parity.json` 的 8 组金额样例，覆盖计费类型、显式附加费、USD/CNY、起算时间、一次性与周期费用。测试是默认门禁的一部分。

## 原始审计发现（已修复）

| 编号 | 优先级 | 问题 | 复现结果 |
| --- | --- | --- | --- |
| F-01 | P1 | 重复终局确认会掩盖已记录的退款 | 730 元预付费用，退款 30 元后应保留 700 元；再次确认同一故障后聚合恢复为 730 元 |
| F-02 | P1 | API Key 的明确固定附加费被后端总成本遗漏 | 自定义一次性 100 元，窗口采购为 100，经济累计采购为 0，数据质量仍标记 complete |
| F-03 | P1 | “当天”成本与收入时间窗口不一致 | 中午查看时，收入从今日零点统计，经济接口却从昨天中午开始；前端回退路径又会把今日未来 12 小时计入 |
| F-04 | P2 | 按量账号可被错误套用订阅默认价 | API Key 带有 plan_type=pro、未配置固定费，经过 730 小时被累计 100 USD 固定成本 |
| F-05 | P2 | 尚未起算的档案已贡献当前小时成本 | 明天开始的 24 元/日档案，今天累计为 0，但当前小时费率已是 1 元/小时 |

### F-01：将幂等键绑定到 updated_at，无法保证同一终局只记一次

- 位置：`backend/internal/service/account_cost_loss.go:267`；`backend/internal/repository/account_cost_loss_repo.go:83`；`backend/internal/service/account_economics.go:528`。
- 默认终局幂等键使用账号 ID、`UpdatedAt.UnixNano()` 和原因。首次记账会更新账号 `updated_at`，后续对同一故障再次确认便得到新键。确认路径没有先检查该生命周期已有的有效终局事件。
- 数据库的唯一键只阻止相同 key，无法阻止这类新 key。退款绑定旧终局事件，而汇总只选择账号最新有效终局，旧退款因此不再影响当前合计。
- 测试：730 元/月（730 小时），第 100 小时终局，退款 30 元；第 101 小时再确认，账号未恢复。默认 key 不同，汇总从 700 回到 730。
- 影响：资产经济成本、损失余额和回本预测偏高；测试未涉及实际向供应商付款或用户余额扣减。
- 建议：建立账号生命周期标识；在事务中锁定账号/有效终局，复用已有终局记录。仅在明确恢复后开启新生命周期。不能使用普通资料修改也会变化的 `updated_at` 作为生命周期。
- status：resolved；evidence_ids：[E-02]；confidence：high。

### F-02：固定费用聚合误用“是否允许确认封禁损失”的过滤规则

- 位置：`backend/internal/service/account_economics.go:541`；`backend/internal/service/account_cost_loss.go:325`；前端固定附加费入口见 `frontend/src/features/cost-center/components/CostProfileInspector.vue`。
- `summarizeProcurementEconomics` 调用 `isProcurementAccountForCostLoss`，排除了 API Key、Service Account、中转等类型，连这些账号明确保存的自定义固定附加费也被排除。
- 前端明确支持给按量账号添加月租、手续费或专线费；窗口采购聚合也会计入这些档案，造成同一响应的窗口采购和总成本口径冲突。
- 测试通过真实 `GetSnapshot`：API Key 的自定义 CNY 100 一次性费用，`window_procurement_cny=100`、`procurement_accrued_cny=0`、`data_quality.status=complete`。
- 建议：将“允许固定附加费”“允许应用订阅默认价”“允许确认终局损失”拆成三个规则。按量账号可有明确固定费，但不应自动套订阅价，也不应因此具备封禁损失资格。
- status：resolved；evidence_ids：[E-02]；confidence：high。

### F-03：当天范围混用了自然日、滚动 24 小时和完整未来自然日

- 位置：`frontend/src/features/cost-center/useCostCenterData.ts:46`、`:592`；`frontend/src/features/cost-center/usageWindow.ts:40`；`frontend/src/views/admin/CostCenterView.vue:902`；`backend/internal/service/account_economics.go:263`。
- 收入查询使用设备本地自然日边界；`economicsWindowHours('today')` 固定返回 24，后端按“当前时刻减 24 小时”查询采购和损失。
- 复现时间为 2026-09-16 当地中午：收入起点是今日零点，经济接口起点是昨天中午，因此昨天的损失可出现在“当天资产损失”中。
- 当经济接口不可用时，前端回退计算将当天结束时间设为次日零点，且没有将已发生成本截止到现在。24 元/日的档案，中午应已摊销 12 元，回退路径却显示 24 元。
- 建议：统一传递明确的 `start_time/end_time/timezone`；已发生成本的结束时刻不超过当前时间。趋势轴可以显示当天剩余时间，但实际金额不能提前累计未来部分。
- status：resolved；evidence_ids：[E-01]；confidence：high。

### F-04：默认订阅价格没有在计费类型边界截断

- 位置：`frontend/src/features/cost-center/model.ts:199`；`frontend/src/views/admin/CostCenterView.vue:861`。
- `resolveCostProfile` 未配置自定义费时直接根据套餐标记返回默认价，没有先排除 metered 类型。成本中心随后对所有账号调用 `economicCostSnapshot`，显示标签上的“按 Token”不会阻止金额进入合计。
- 复现条件：OpenAI API Key 带 `extra.plan_type=pro`，且没有自定义固定档案。计费模式被正确识别为 metered，但默认档案仍是 100 USD/月，730 小时后累计 100 USD。
- 现有按量账号测试使用空套餐标记，恰好返回 unknown=0，没有覆盖该情况。
- 建议：无自定义档案的按量账号固定费应为零或明确不可用；自定义附加费则按 F-02 的独立规则保留。
- status：resolved；evidence_ids：[E-01]；confidence：high。

### F-05：未来档案的当前费率提前生效

- 位置：`frontend/src/features/cost-center/model.ts:315`；对照 `backend/internal/service/account_economics.go:720`。
- 前端 `accruedCost` 会把未来起算的累计成本设为零，但 `economicCostSnapshot` 仍无条件返回配置的 `hourlyRate`。后端 `accruedCostAt` 在未来起算时同时返回零累计、零小时费率，前后端不一致。
- 起算时间表单只有“不早于加入时间”的限制，允许保存未来时间。
- 测试：明日开始、24 CNY/日，今日累计是 0，但当前小时费率为 1。该费率会进入“每小时运行成本”和预测计算。
- 建议：当前有效小时费率应受起算时间约束。若要展示未来配置费率，应作为单独的计划字段。
- status：resolved；evidence_ids：[E-01]；confidence：high。

## 已检查且没有据此认定为缺陷的部分

- 当前实现没有把 `recognized_cost` 与 `net_loss` 再次相加；现有避免重复计入减值的测试通过。
- 单次退款在仓储层锁定源事件并限制可退款余额；现有退款边界测试通过。F-01 是新建重复终局后聚合选错事件，不能因此声称退款额度检查完全缺失。
- 经济预测对成员变化、累计倒退和样本不足有阻断，未直接用缺失样本编造收入速率；相关测试通过。
- 删除普通账号后，“当前号池”费用可以下降；无终局事件的 100 元一次性采购在当前池由 100 变 0，但本月历史采购仍保留 100。页面明确标记了当前池范围，因此该现象单独记录为范围边界，不列入已确认漏账。若需要全生命周期经营账本，应另设独立指标。
- 730 小时/月、默认套餐价、实时汇率重估均属于模型或估算选择，不能当作实际供应商账单。此次没有联网核验各供应商现行价格，也没有审计全部 Token 定价主链路。

## 复现与验证

原先显式启用的失败用例已加入默认测试集，独立审计配置已移除。下面命令现在应返回成功；仍保留 `.git/cost-audit-2026-09-16/` 中的初始失败证据。真实数据库测试使用测试容器，不连接生产数据库。

```powershell
# 仓库根目录；前端边界、共享金额样例和请求窗口回归。
Push-Location frontend
corepack pnpm@9 exec vitest run src/features/cost-center/__tests__/model.accounting.spec.ts src/features/cost-center/__tests__/accountingParity.spec.ts src/features/cost-center/__tests__/useCostCenterData.spec.ts
Pop-Location

# 后端计账、时间边界、共享样例与真实 PostgreSQL 生命周期回归。
Push-Location backend
go test -tags=unit ./internal/service -run 'Test(CostRegression|CostAccounting|Economics)' -v
go test -tags=integration ./internal/repository -run TestCostLifecycle -v
Pop-Location
```

审计用例来源：

- [前端成本回归](../frontend/src/features/cost-center/__tests__/model.accounting.spec.ts)
- [前后端共享金额样例](../backend/internal/service/testdata/cost_accounting_parity.json)
- [后端成本回归](../backend/internal/service/account_cost_regression_test.go)

默认相关前端回归：13 个文件、129 项测试通过；按钮文件和新增前端审计文件 ESLint 通过。既有后端损失、退款、聚合、样本及仓储测试通过。测试日志保存在 `.git/cost-audit-2026-09-16/`，不包含生产数据。

## Evidence → Finding → Path

### E-01：前端边界复现

- observed_at：2026-09-16
- source_type：log
- source_ref：`.git/cost-audit-2026-09-16/frontend-audit.log`
- content_hash / artifact_path：n/a（保留的修复前本地日志）
- repro_command：在 frontend 执行 `corepack pnpm@9 exec vitest run src/features/cost-center/__tests__/model.accounting.spec.ts src/features/cost-center/__tests__/accountingParity.spec.ts src/features/cost-center/__tests__/useCostCenterData.spec.ts`
- raw_excerpt：修复前 today 起点相差 12 小时；24 received / 12 expected；100 received / 0 expected；1 received / 0 expected。当前上述命令执行修复后的默认回归，预期全部通过。
- linked_workitem：F-03、F-04、F-05
- supersedes：none

### E-02：成本扩展服务层复现

- observed_at：2026-09-16
- source_type：log
- source_ref：`.git/cost-audit-2026-09-16/backend-audit.log`
- content_hash / artifact_path：n/a（保留的修复前本地日志）
- repro_command：在 backend 执行 `go test -tags=unit ./internal/service -run 'Test(CostRegression|CostAccounting|Economics)' -v`，以及 `go test -tags=integration ./internal/repository -run TestCostLifecycle -v`。
- raw_excerpt：修复前 explicit API-key overhead: window=100 lifetime=0 quality=complete；same terminal lifecycle: after refund=700 after repeated confirmation=730。当前命令执行修复后回归，预期分别为 100/100 和 700/700。
- linked_workitem：F-01、F-02
- supersedes：none

### P-01：优先修复路径

E-01 / E-02 的原始失败 → 生命周期事务与费用资格修复 → 明确日历窗口和当前费率 → 默认回归与共享前后端样例 → 真实 PostgreSQL 并发验证 → 全量门禁 → 提交并推送 Git。此次目标是修复并推送源码，未推送新的桌面安装包标签。

## 账号管理入口修改

`CostCenterView.vue` 原按钮从“Sub2API 设置”改为“账号管理”，提示文字为“进入 Sub2API 账号管理”，直接复用 `goToAccounts()` 跳转到 `/admin/accounts`。已移除仅用于旧设置入口的函数，保留正常的系统设置页面。

该修改与成本修复位于 `codex/cost-audit-account-shortcut`。已发布的 v0.2.37 安装器不会因 Git 推送而改变；配套安装包需单独发布。
