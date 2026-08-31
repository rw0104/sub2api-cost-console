# 前端成本真实性、模型核对与面板精简开发记录

> 记录时间：2026-08-31（America/Los_Angeles）
>
> 发布目标：桌面 `v0.2.28`
>
> 兼容内核：`0.1.183`；成本扩展：`1.1.1`；成本算法：`1.6.0`

## 结论

本次按《上游模型检测失真分析与修复方案》完成前端纵向切片：刷新失败不再清空最近成功成本事实，用户计费产出与上游成本使用明确字段和标签，默认总览从两套经济图收敛为一套财务事实趋势，模型成本回退值显式标记为估算，模型核对区分请求模型、实际发往上游模型和响应声明模型。

新增的诊断数据没有删除，只改为用户主动展开后显示。后端事实来源、历史 usage_logs 和模型审计字段保持兼容；本次没有修改 Token 计费公式、价格目录或数据库迁移。

## 实现范围

### 成本数据保持

- `useCostCenterData` 在 dashboard、账号清单、当日统计、账号用量、模型、路由、Ops、设置、价格和经济快照读取失败时保留已有成功值。
- 局部账号用量刷新使用账号 ID 合并，失败账号保留旧快照，不再用只包含成功项的临时映射覆盖整张表。
- 数据源状态新增 `lastSuccessAt` 与 `requestedWindow`，后台刷新失败使用 `stale`，首次读取失败才使用 `unavailable`。
- 账号表在使用旧用量快照时显示最近成功时间，避免旧值看起来像实时值。

### 成本事实与模型真实性

- 模型统计聚合保留 `account_cost_estimated`，当 `account_stats_cost` 缺失并回退到标准价时，模型摘要和路由明细显示估算标识。
- 总览财务趋势统一使用单一 `financialTrend`，以 USD/h 展示用户计费产出、上游账号成本和调用贡献；采购和封禁损失仍在独立 CNY 资产口径中。
- 模型核对摘要明确展示 `requested_model`、`upstream_model`、`upstream_response_model`，未声明响应单独计为未观测，不计入一致率分母。

### 面板精简

- 删除总览中重复的“实时成本速率”图，保留一张财务事实趋势图。
- `AdaptiveOperationsCharts` 默认折叠，只有点击“展开诊断数据”后显示请求质量、账号健康、TTFT、Token、缓存等诊断视图。
- 模型 Top 8、模型成本宽表和真实路由明细改为按需展开，默认保留模型成本摘要和核对摘要。
- 诊断组件继续使用同一响应式事实状态，不重新计算第二套金额。

## Evidence -> Finding -> Path

### E-001

- title: 成本刷新失败保留最近成功快照
- observed_at: 2026-08-31
- source_type: command
- source_ref: `frontend/src/features/cost-center/useCostCenterData.ts`
- content_hash: n/a
- artifact_path: n/a
- repro_command: `corepack pnpm@9 exec vitest run src/features/cost-center/__tests__/useCostCenterData.spec.ts src/features/cost-center/dataState.test.ts`
- raw_excerpt: `16 个 useCostCenterData 测试和 4 个 dataState 测试通过；局部账号用量合并测试确认失败刷新保留旧账号快照。`
- linked_workitem: n/a
- supersedes: none

### E-002

- title: 产出与账号成本回退的估算状态可追溯
- observed_at: 2026-08-31
- source_type: command
- source_ref: `frontend/src/features/cost-center/modelCostAnalysis.ts`
- content_hash: n/a
- artifact_path: n/a
- repro_command: `corepack pnpm@9 exec vitest run src/features/cost-center/__tests__/modelCostAnalysis.spec.ts src/features/cost-center/__tests__/modelRouteAnalysis.spec.ts`
- raw_excerpt: `21 个模型成本测试和 5 个模型路由测试通过；缺少 account_stats_cost 的聚合结果保留 accountCostEstimated=true。`
- linked_workitem: n/a
- supersedes: none

### E-003

- title: 总览重复图表和高频诊断默认折叠
- observed_at: 2026-08-31
- source_type: command
- source_ref: `frontend/src/views/admin/CostCenterView.vue`
- content_hash: n/a
- artifact_path: n/a
- repro_command: `corepack pnpm@9 exec vitest run src/features/cost-center/__tests__/uiShellContract.spec.ts`
- raw_excerpt: `7 个 UI shell 测试通过；源码包含单一财务事实趋势、showDiagnostics 折叠开关和模型核对真实性摘要。`
- linked_workitem: n/a
- supersedes: none

### E-004

- title: 前后端和桌面生产构建通过
- observed_at: 2026-08-31
- source_type: command
- source_ref: `frontend/package.json`, `frontend/src-tauri/tauri.conf.json`
- content_hash: n/a
- artifact_path: n/a
- repro_command: |
    `corepack pnpm@9 lint:check`
    `corepack pnpm@9 test:run`
    `corepack pnpm@9 build`
    `$env:GOTOOLCHAIN='go1.27.0'; go test -tags=unit ./...`
    `corepack pnpm@9 desktop:build`
- raw_excerpt: `前端 270 个测试文件/1930 个断言通过；Go unit 全包通过；Web 构建通过；NSIS v0.2.28 安装包生成。`
- linked_workitem: n/a
- supersedes: none

### F-001

- title: 临时刷新故障不应使成本事实自动消失
- severity: high
- category: design
- status: validated
- evidence_ids: [E-001]
- location: `frontend/src/features/cost-center/useCostCenterData.ts`
- impact: 网络抖动或单个接口失败时继续展示最近成功值并标记旧数据，避免误读为零成本或无记录。
- confidence: high
- repro_steps:
  1. 运行 E-001 定向测试。
  2. 检查账号用量和经济快照失败分支仍保留已有状态。
- remediation: 已实现快照保留、局部合并和 `stale` 状态。
- optional_attack:

### F-002

- title: 用户计费产出不能标称为上游成本
- severity: high
- category: design
- status: validated
- evidence_ids: [E-002, E-003]
- location: `frontend/src/views/admin/CostCenterView.vue`, `frontend/src/features/cost-center/modelCostAnalysis.ts`
- impact: 产出、标准价、账号成本、采购投入和封禁损失分别显示，估算回退不会伪装成请求快照成本。
- confidence: high
- repro_steps:
  1. 运行 E-002 的模型成本测试。
  2. 打开成本中心，确认财务趋势和模型摘要分别标出产出、账号成本和估算来源。
- remediation: 已实现事实字段标识和 `account_cost_estimated` 传播。
- optional_attack:

### F-003

- title: 重复面板和统计过载应改为按需诊断
- severity: medium
- category: design
- status: validated
- evidence_ids: [E-003]
- location: `frontend/src/views/admin/CostCenterView.vue`
- impact: 总览默认只保留可行动的财务事实和模型核对摘要，降低认知负担和重复渲染。
- confidence: high
- repro_steps:
  1. 运行 E-003 UI shell 测试。
  2. 打开总览，确认诊断图、Top 8 和宽表在展开前不渲染。
- remediation: 已实现单一财务趋势和 `showDiagnostics` 按需展开。
- optional_attack:

### P-001

- title: 刷新结果到真实性面板
- path_type: callflow
- start: 成本中心自动刷新
- goal: 精简、可追溯且不丢失事实的成本与模型展示
- steps:
  1. action: 并行读取 dashboard、账号、usage、Ops、模型和经济快照 - evidence: E-001 - finding: F-001
  2. action: 成功值写入 canonical 状态，失败值保留旧快照并标记 stale - evidence: E-001 - finding: F-001
  3. action: 财务趋势统一映射产出、账号成本和调用贡献 - evidence: E-002 - finding: F-002
  4. action: 总览只渲染一张事实趋势，其他统计按需展开 - evidence: E-003 - finding: F-003
- residual_risks: 本地桌面构建已生成 NSIS，但因缺少 Tauri 私钥未生成 updater 签名；正式发布必须使用 GitHub Actions Secret 完成签名和资产上传。

## 发布记录

- 桌面版本元数据：`v0.2.28`。
- 兼容内核和成本版本未变：`0.1.183` / `1.1.1` / `1.6.0`。
- 本地 NSIS 产物：`frontend/src-tauri/target/release/bundle/nsis/Sub2API Cost Console_0.2.28_x64-setup.exe`。
- 本地 NSIS 产物未作为正式更新源发布；updater 签名和 GitHub Release 由 `desktop-release.yml` 完成。
