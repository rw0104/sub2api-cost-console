<template>
  <component :is="desktopMode ? 'div' : AppLayout">
    <div
      class="cost-console"
      :class="{
        'cost-console--embedded': !desktopMode,
        'cost-console--physical-wide': desktopScale.useWideToolbar,
      }"
    >
      <header class="cost-toolbar">
        <div class="cost-brand-block">
          <span class="cost-eyebrow">SUB2API / DESKTOP ECONOMICS</span>
          <h1>{{ panelTitle }}</h1>
          <p>{{ panelDescription }} · 算法 v{{ COST_ALGORITHM_VERSION }}</p>
        </div>

        <nav class="cost-workspaces" aria-label="成本中心工作区">
          <button
            v-for="item in workspaceItems"
            :key="item.key"
            type="button"
            :class="{ active: activePanel === item.key }"
            :aria-current="activePanel === item.key ? 'page' : undefined"
            @click="activePanel = item.key"
          >
            <component :is="item.icon" :size="15" />
            <span>{{ item.label }}</span>
            <kbd>{{ item.shortcut }}</kbd>
          </button>
        </nav>

        <div class="cost-toolbar__actions">
          <label class="cost-select-label">
            <span>观察窗口</span>
            <select v-model="range" aria-label="观察窗口">
              <option value="today">当天</option>
              <option value="1m">最近 1 分钟</option>
              <option value="5m">最近 5 分钟</option>
              <option value="30m">最近 30 分钟</option>
              <option value="1h">最近 1 小时</option>
              <option value="6h">最近 6 小时</option>
              <option value="24h">最近 24 小时</option>
              <option value="7d">最近 7 天</option>
              <option value="30d">最近 1 个月</option>
            </select>
          </label>
          <label class="cost-select-label cost-refresh-interval-label">
            <span>刷新周期</span>
            <select v-model.number="refreshIntervalSeconds" aria-label="自动刷新周期">
              <option :value="5">5 秒</option>
              <option :value="10">10 秒</option>
              <option :value="15">15 秒</option>
              <option :value="30">30 秒</option>
            </select>
          </label>
          <button type="button" class="cost-tool-button" :class="{ active: autoRefresh }" :title="autoRefresh ? `自动刷新 ${countdown}s` : '开启自动刷新'" @click="toggleAutoRefresh">
            <Activity :size="16" />
            <span>{{ autoRefresh ? `${countdown}s` : '自动刷新' }}</span>
          </button>
          <button type="button" class="cost-icon-button" title="刷新 (Ctrl+R)" aria-label="刷新数据" :disabled="loading" @click="reload">
            <RefreshCcw :size="17" :class="{ 'cost-spin': loading }" />
          </button>
          <button type="button" class="cost-icon-button" title="全屏 (F11)" aria-label="切换全屏" @click="toggleFullscreen">
            <Maximize2 :size="17" />
          </button>
        </div>
      </header>

      <div v-if="error" class="cost-error" role="alert">
        <TriangleAlert :size="17" />
        <span>{{ error }}</span>
        <button type="button" @click="reload">重试</button>
      </div>

      <main>
        <section v-if="activePanel === 'overview'" class="cost-workspace cost-overview" aria-labelledby="overview-title">
          <div class="cost-quality-strip">
            <div class="cost-quality-strip__intro">
              <span>POOL QUALITY / LIVE SAMPLE</span>
              <h2 id="overview-title">混池 + 自用池综合质量</h2>
              <p>{{ lastUpdatedLabel }} · 最近 {{ formatInteger(totalObservedRequests) }} 次调用 · {{ activeAccounts.length }} 个可调度账号</p>
            </div>
            <MetricCell label="综合评分" :value="qualityScore.toFixed(1)" :note="opsOverview ? `${qualityGrade} · Ops 实测` : `${qualityGrade} · 账号状态派生`" accent="lime" />
            <MetricCell label="成功 / 失败" :value="opsOverview ? `${formatInteger(successCount)} / ${formatInteger(errorCount)}` : '— / —'" :note="opsOverview ? `失败率 ${formatPercent(errorRate)}` : '运维监控未开启，不做虚假推断'" />
            <MetricCell label="切号 / 恢复" :value="opsOverview ? `${formatInteger(switchCount)} / —` : '— / —'" note="真实切号 / 恢复事件暂未采集" />
            <MetricCell label="TTFT P95" :value="formatDuration(ttftP95)" :note="opsOverview ? '真实首 token 样本' : '运维监控未开启'" accent="blue" />
          </div>

          <div class="cost-provenance-strip" :class="{ 'has-default-warning': defaultCostProfileCount > 0 }" aria-label="成本数据口径">
            <div><CircleCheck :size="15" /><strong>实测数据</strong><span>请求、Token 与 API 金额来自 usage_logs / Ops</span></div>
            <div :class="{ 'is-warning': exchangeRate.source === 'fallback' }"><Calculator :size="15" /><strong>确定性计算</strong><span>固定采购档案 + API 模型按量成本；1 USD = {{ exchangeRate.rate.toFixed(4) }} CNY（{{ exchangeRateLabel }}）</span></div>
            <div><TrendingUp :size="15" /><strong>预测数据</strong><span>滚动平均和剩余预期会明确标记为推导值</span></div>
            <div v-if="defaultCostProfileCount" class="is-warning"><TriangleAlert :size="15" /><strong>{{ defaultCostProfileCount }} 个账号使用美国官方默认价</strong><span>Plus $20、Pro $100 起、Business/Team $25；实际账单可逐账号覆盖</span></div>
          </div>

          <div class="cost-assets-row">
            <div class="cost-section-heading">
              <span>LIVE QUOTA / JOINED COST</span>
              <h2>资产与成本总览</h2>
              <p>{{ lastUpdatedLabel }} · {{ accounts.length }} 个号码 · 成本从账号加入时刻起算</p>
            </div>

            <div class="cost-donut-wrap">
              <div class="cost-donut" :style="{ background: platformDonutBackground }" role="img" :aria-label="platformDonutLabel">
                <div><strong>{{ formatCny(totalAccruedCny, 2) }}</strong><span>{{ platformDonutModeLabel }}</span></div>
              </div>
            </div>

            <div class="cost-metric-grid">
              <MetricCell label="当前 API 产出速率" :value="formatUsd(apiOutputHourlyUsd, 2)" note="API 美元 / 小时" accent="gold" />
              <MetricCell label="平滑产出速率" :value="formatUsd(rollingOutputUsd, 2)" :note="`${rollingTrendLabel} · API 美元 / 小时`" accent="gold" />
              <MetricCell label="当前采购成本（配置推算）" :value="`${formatCny(procurementHourlyCny, 4)}/h`" note="用户成本档案 / 美国套餐默认价折算" accent="blue" />
              <MetricCell label="一小时综合成本" :value="formatCny(combinedHourlyCny, 4)" :note="`采购 + ${apiCostBasisLabel}`" accent="blue" />
              <MetricCell label="今日 API 账号成本" :value="formatUsd(todayAccountCostUsd, 3)" note="上游账号实际成本" />
              <MetricCell label="最近窗口 API 产出" :value="formatUsd(windowActualOutputUsd, 3)" note="用户实际计费" />
              <MetricCell label="预计月度采购" :value="formatCny(monthlyProcurementForecastCny, 2)" note="当前号码结构 × 730h" />
              <MetricCell label="可用账号" :value="`${activeAccounts.length} / ${accounts.length}`" :note="usageSyncedCount ? `窗口平均余量 ${formatPercent(quotaRemainingAverage)}` : '用量窗口等待同步'" />
            </div>
          </div>

          <div class="cost-chart-row">
            <ChartPanel title="API 产出速率" :caption="`${rangeLabel} · 真实 usage_logs，统一折算为 $/h`">
              <CostLineChart
                :labels="trendLabels"
                :series="[
                  { label: '当前采样', data: trendActualCost, color: '#e0bd4e', fill: true },
                  { label: rollingTrendLabel, data: rollingTrendActualCost, color: '#b9e55a', dashed: true },
                ]"
                value-prefix="$"
              />
            </ChartPanel>
            <ChartPanel title="实时成本速率" :caption="`${rangeLabel} · ${apiCostBasisLabel} / 当前账号采购配置推算`">
              <CostLineChart
                :labels="trendLabels"
                :series="[
                  { label: apiCostBasisLabel, data: trendStandardCost, color: '#d8b94d' },
                  { label: '采购基线', data: procurementBaseline, color: '#7eb6d8', dashed: true },
                  { label: '综合成本', data: trendCombinedCost, color: '#b9e55a' },
                ]"
                value-prefix="¥"
              />
            </ChartPanel>
          </div>

          <section class="cost-model-panel" aria-labelledby="model-cost-title">
            <div class="cost-model-panel__header">
              <div>
                <span>MODEL ECONOMICS / TOKEN BILLING</span>
                <h2 id="model-cost-title">模型成本分析</h2>
                <p>{{ modelCostRangeLabel }} · 按真实 usage token、实际模型、渠道价格与账号费率分别核算</p>
              </div>
              <div class="cost-model-controls">
                <label>
                  <span>统计窗口</span>
                  <select v-model="modelCostRange" aria-label="模型成本统计窗口">
                    <option value="today">当天</option>
                    <option value="1m">最近 1 分钟</option>
                    <option value="5m">最近 5 分钟</option>
                    <option value="30m">最近 30 分钟</option>
                    <option value="1h">最近 1 小时</option>
                    <option value="6h">最近 6 小时</option>
                    <option value="24h">最近 24 小时</option>
                    <option value="7d">最近 7 天</option>
                    <option value="30d">最近 1 个月</option>
                  </select>
                </label>
                <label>
                  <span>模型口径</span>
                  <select v-model="modelCostSource" aria-label="模型统计口径">
                    <option value="upstream">上游实际模型</option>
                    <option value="requested">用户请求模型</option>
                    <option value="mapping">请求 → 上游映射</option>
                  </select>
                </label>
                <label class="cost-model-controls__account">
                  <span>成本账号</span>
                  <select v-model="modelCostAccountSelection" aria-label="模型成本账号">
                    <option value="all">全部账号</option>
                    <option v-for="account in accounts" :key="account.id" :value="String(account.id)">
                      {{ account.name }} · {{ describeCostAccountOrigin(account) }}
                    </option>
                  </select>
                </label>
              </div>
            </div>

            <div class="cost-pricing-status" :class="{ 'is-unavailable': !pricingStatus }">
              <div>
                <span>价格目录</span>
                <strong>{{ pricingCatalogLabel }}</strong>
                <small>{{ pricingCatalogDetail }}</small>
              </div>
              <button type="button" :disabled="pricingRefreshing" @click="refreshPricingCatalog">
                <RefreshCcw :size="14" :class="{ 'cost-spin': pricingRefreshing }" />
                {{ pricingRefreshing ? '同步中' : '立即同步' }}
              </button>
            </div>

            <div class="cost-model-summary">
              <MetricCell label="模型数" :value="formatInteger(modelCostSummary.modelCount)" :note="modelCostAccountLabel" />
              <MetricCell label="标准 / 渠道价" :value="formatUsd(modelCostSummary.standardCost, 5)" note="价格目录或渠道自定义价" />
              <MetricCell label="上游账号成本" :value="formatUsd(modelCostSummary.accountCost, 5)" :note="modelCostSummary.estimatedModelCount ? `${modelCostSummary.estimatedModelCount} 个模型含历史回退` : '账号费率快照实算'" accent="blue" />
              <MetricCell label="用户实际计费" :value="formatUsd(modelCostSummary.revenue, 5)" note="usage_logs actual_cost" accent="gold" />
              <MetricCell label="毛利 / 毛利率" :value="`${formatUsd(modelCostSummary.grossProfit, 5)} / ${modelCostSummary.grossMargin == null ? '—' : formatPercent(modelCostSummary.grossMargin)}`" :note="modelCostSummary.missingPricingCount ? `${modelCostSummary.missingPricingCount} 个模型缺少 usage 或价格` : '用户计费 − 上游账号成本'" accent="lime" />
            </div>

            <p class="cost-model-note">
              DeepSeek 官方接口会区分缓存命中 Token；API 中转站按其返回的 usage、实际上游模型及渠道/账号价格核算。价格同步只影响后续请求，历史记录保留请求发生时的价格快照；上游未返回 usage 时不会伪造精确成本。
            </p>

            <div class="cost-model-table-wrap" tabindex="0" aria-label="模型成本表，可横向滚动">
              <table class="cost-model-table">
                <thead>
                  <tr><th>模型</th><th>请求</th><th>输入 Token</th><th>缓存命中</th><th>输出 Token</th><th>标准 / 渠道价</th><th>上游账号成本</th><th>用户实际计费</th><th>毛利 / 毛利率</th></tr>
                </thead>
                <tbody>
                  <tr v-for="row in modelCostRows" :key="row.model">
                    <td><strong>{{ row.model || '未知模型' }}</strong><small>{{ modelCostSourceLabel }}</small></td>
                    <td>{{ formatInteger(row.requests) }}</td>
                    <td>{{ formatTokens(row.input_tokens) }}</td>
                    <td><strong>{{ formatTokens(row.cache_read_tokens) }}</strong><small>{{ formatCacheHitRate(row.input_tokens, row.cache_read_tokens) }}</small></td>
                    <td>{{ formatTokens(row.output_tokens) }}</td>
                    <td><strong>{{ formatUsd(row.standardCost, 6) }}</strong><small v-if="row.pricingMissing" class="cost-warning-text">缺少 usage 或价格</small><small v-else>价格目录 / 渠道价</small></td>
                    <td><strong>{{ formatUsd(row.accountCost, 6) }}</strong><small v-if="row.accountCostEstimated" class="cost-warning-text">旧记录回退标准价</small><small v-else>账号费率快照</small></td>
                    <td><strong>{{ formatUsd(row.revenue, 6) }}</strong><small>actual_cost</small></td>
                    <td><strong :class="row.grossProfit >= 0 ? 'cost-lime' : 'cost-warning-text'">{{ formatUsd(row.grossProfit, 6) }}</strong><small>{{ row.grossMargin == null ? '—' : formatPercent(row.grossMargin) }}</small></td>
                  </tr>
                  <tr v-if="modelCostRows.length === 0"><td colspan="9" class="cost-empty-row">{{ modelCostRangeLabel }}没有可分析的模型调用</td></tr>
                </tbody>
              </table>
            </div>

            <div class="cost-route-heading">
              <div><strong>真实模型 / 渠道 / 接口明细</strong><span>只展示 usage_logs 实际记录，不从账号名称猜测</span></div>
              <small v-if="modelRoutesTruncated" class="cost-warning-text">超过 25,000 条，仅显示最近样本</small>
            </div>
            <div class="cost-model-table-wrap" tabindex="0" aria-label="真实模型渠道接口明细，可横向滚动">
              <table class="cost-model-table cost-route-table">
                <thead>
                  <tr><th>请求 → 实际模型</th><th>渠道</th><th>账号</th><th>分组</th><th>入站接口</th><th>上游接口</th><th>当前单价</th><th>请求</th><th>Token / 缓存</th><th>标准 / 账号成本</th></tr>
                </thead>
                <tbody>
                  <tr v-for="routeRow in modelRoutes" :key="routeRow.key">
                    <td><strong>{{ routeRow.requestedModel }} → {{ routeRow.upstreamModel }}</strong><small>{{ routeRow.mappingChain }}</small></td>
                    <td><strong>{{ routeRow.channelName }}</strong><small>{{ routeRow.channelId == null ? '无 channel_id' : `channel_id ${routeRow.channelId}` }}</small></td>
                    <td><strong>{{ routeRow.accountName }}</strong><small>{{ routeRow.accountId == null ? '历史记录' : `account_id ${routeRow.accountId}` }}</small></td>
                    <td><strong>{{ routeRow.groupName }}</strong><small>{{ routeRow.groupId == null ? '无 group_id' : `group_id ${routeRow.groupId}` }}</small></td>
                    <td><strong>{{ routeRow.inboundEndpoint }}</strong><small>客户端入口</small></td>
                    <td><strong>{{ routeRow.upstreamEndpoint }}</strong><small>实际上游路径</small></td>
                    <td><strong>入 {{ formatUsdPerMillion(resolveRoutePricing(routeRow)?.input_price) }} · 出 {{ formatUsdPerMillion(resolveRoutePricing(routeRow)?.output_price) }}</strong><small>缓存写 {{ formatUsdPerMillion(resolveRoutePricing(routeRow)?.cache_write_price) }} · 命中 {{ formatUsdPerMillion(resolveRoutePricing(routeRow)?.cache_read_price) }} · {{ routePricingSource(routeRow) }}</small></td>
                    <td>{{ formatInteger(routeRow.requests) }}</td>
                    <td><strong>{{ formatTokens(routeRow.inputTokens + routeRow.cacheReadTokens + routeRow.outputTokens) }}</strong><small>缓存 {{ formatTokens(routeRow.cacheReadTokens) }}</small></td>
                    <td><strong>{{ formatUsd(routeRow.standardCost, 6) }} / {{ formatUsd(routeRow.accountCost, 6) }}</strong><small>请求快照</small></td>
                  </tr>
                  <tr v-if="modelRoutes.length === 0"><td colspan="10" class="cost-empty-row">{{ modelCostRangeLabel }}没有真实路由记录</td></tr>
                </tbody>
              </table>
            </div>
          </section>

          <div class="cost-bottom-row">
            <ChartPanel title="真实请求量趋势" :caption="`${rangeLabel} · usage_logs / Ops 请求桶`" class="cost-score-panel">
              <CostLineChart
                :labels="qualityTrendLabels"
                :series="[
                  { label: '真实请求', data: requestVolumeTrend, color: '#b9e55a', fill: true },
                  { label: '滚动平均', data: movingAverage(requestVolumeTrend, requestSmoothingPoints), color: '#83b7d3', dashed: true },
                ]"
              />
            </ChartPanel>
            <div class="cost-distribution-panel">
              <div class="cost-panel-heading"><strong>上游参与比例</strong><span>今日真实请求</span></div>
              <div class="cost-distribution-panel__body">
                <div class="cost-donut cost-donut--small" :style="{ background: accountDonutBackground }">
                  <div><strong>{{ formatInteger(todayRequests) }}</strong><span>调用</span></div>
                </div>
                <ol class="cost-ranking-list">
                  <li v-for="item in accountDistribution" :key="item.id">
                    <span class="cost-swatch" :style="{ background: item.color }"></span>
                    <div><strong>{{ item.name }}</strong><small>{{ formatCny(item.hourlyCost, 4) }}/h · {{ item.platform }}</small></div>
                    <b>{{ formatPercent(item.share) }}</b>
                  </li>
                </ol>
              </div>
            </div>
          </div>
        </section>

        <section v-else-if="activePanel === 'upstreams'" class="cost-workspace cost-upstreams" aria-labelledby="upstream-title">
          <div class="cost-page-heading">
            <div>
              <span>UPSTREAM ASSETS / OPERATOR TABLE</span>
              <h2 id="upstream-title">上游资产与实时成本</h2>
              <p>账号质量、调度、采购与产出统一运维视图</p>
            </div>
            <div class="cost-page-heading__actions">
              <button v-if="desktopMode" type="button" class="cost-primary-button cost-primary-button--outline" @click="openAccountPurchase">
                <ShoppingBag :size="16" /> 采购账号 <ExternalLink :size="12" />
              </button>
              <button type="button" class="cost-primary-button" @click="goToAccounts"><Plus :size="17" /> 新增上游</button>
            </div>
          </div>

          <div class="cost-table-tools">
            <div class="cost-platform-tabs" role="tablist" aria-label="平台筛选">
              <button
                v-for="item in upstreamProviderTabs"
                :key="item.key"
                type="button"
                role="tab"
                :class="{ active: platformFilter === item.key }"
                :aria-selected="platformFilter === item.key"
                @click="platformFilter = item.key"
              >
                <span>{{ item.label }}</span>
                <small>{{ item.count }}</small>
              </button>
            </div>
            <span class="cost-refresh-stamp">上次刷新：{{ lastUpdatedLabel }} · 最近 {{ formatInteger(totalObservedRequests) }} 次</span>
            <label class="cost-search"><Search :size="15" /><input v-model.trim="searchQuery" type="search" placeholder="查找账号、分组或备注" /></label>
          </div>

          <div class="cost-ranking-bar" aria-label="上游实时排行控制">
            <div class="cost-ranking-bar__title">
              <Trophy :size="16" />
              <div><strong>实时排行</strong><span>根据当前筛选结果动态重排</span></div>
            </div>
            <label class="cost-ranking-select">
              <span>排行依据</span>
              <select v-model="rankingMetric" aria-label="排行依据">
                <option value="score">综合评分</option>
                <option value="reliability">可用性</option>
                <option value="output">今日 API 产出</option>
                <option value="requests">今日请求量</option>
                <option value="cost">今日账号成本</option>
                <option value="latency">连接测试总耗时</option>
              </select>
            </label>
            <span class="cost-ranking-bar__summary">显示 {{ upstreamRows.length }} 个账号<span v-if="upstreamRows[0]"> · 当前第 1 名：{{ upstreamRows[0].account.name }}</span></span>
          </div>

          <div class="cost-selection-bar" aria-label="上游账号批量操作">
            <label class="cost-check-label">
              <input type="checkbox" :checked="allVisibleAccountsSelected" :indeterminate="someVisibleAccountsSelected && !allVisibleAccountsSelected" @change="toggleSelectAllVisible" />
              <span>选择当前筛选结果</span>
            </label>
            <span class="cost-selection-count">已选 {{ selectedAccountIds.size }} 个账号</span>
            <button type="button" class="cost-primary-button cost-primary-button--outline" :disabled="selectedAccountIds.size === 0" @click="openBulkCostProfile">
              <Settings2 :size="16" />
              批量设置成本档案
            </button>
            <button v-if="selectedAccountIds.size" type="button" class="cost-text-button" @click="clearSelection">清空选择</button>
          </div>

          <p class="cost-table-scroll-hint">
            <ArrowLeftRight :size="14" />
            横向滚动查看完整 16 列数据
          </p>

          <div class="cost-data-table-wrap" tabindex="0" aria-label="上游资产表，可横向滚动">
            <table class="cost-data-table">
              <thead>
                <tr>
                  <th class="cost-select-column">选择</th><th>账号</th><th>状态</th><th>用量窗口</th><th>调度评分 ↓</th><th>优先级</th><th>加入时间</th><th>采购费率（推算）</th><th>累计采购（推算）</th><th>今日账号成本</th><th>API 产出</th><th>请求 / Token</th><th>连接测试总耗时</th><th>当前异常 / 当前限流</th><th>分组</th><th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in upstreamRows" :key="row.account.id" :class="[`status-${row.account.status}`, { selected: selectedAccount?.id === row.account.id, 'row-selected': selectedAccountIds.has(row.account.id) }]">
                  <td class="cost-select-column"><input type="checkbox" :checked="selectedAccountIds.has(row.account.id)" :aria-label="`选择 ${row.account.name}`" @change="toggleAccountSelection(row.account.id)" /></td>
                  <td class="cost-account-cell">
                    <div class="cost-account-primary"><span class="cost-rank-badge" :data-rank="row.rank">#{{ row.rank }}</span><strong>{{ row.account.name }}</strong></div>
                    <small>#{{ row.account.id }} · {{ describeCostAccountOrigin(row.account) }} / {{ row.plan }}</small>
                  </td>
                  <td><StatusLabel :status="row.account.status" :schedulable="row.account.schedulable" /></td>
                  <td>
                    <div v-if="row.usage" class="cost-usage-windows">
                      <span v-if="row.usage.five_hour"><b>5h</b><i><em :style="{ width: `${clampUtilization(row.usage.five_hour.utilization)}%` }"></em></i><strong>{{ formatUtilization(row.usage.five_hour.utilization) }}</strong><small>{{ formatUsageReset(row.usage.five_hour.resets_at) }}</small></span>
                      <span v-if="row.usage.seven_day"><b>7d</b><i><em :style="{ width: `${clampUtilization(row.usage.seven_day.utilization)}%` }"></em></i><strong>{{ formatUtilization(row.usage.seven_day.utilization) }}</strong><small>{{ formatUsageReset(row.usage.seven_day.resets_at) }}</small></span>
                      <small v-if="!row.usage.five_hour && !row.usage.seven_day">暂无窗口</small>
                    </div>
                    <small v-else>等待同步</small>
                  </td>
                  <td><span class="cost-score" :data-grade="scoreGrade(row.score)">{{ row.score.toFixed(1) }}</span><small>{{ row.scoreRaw == null ? '质量分回退' : `${row.scoreRaw.toFixed(2)} / ${row.scoreMax.toFixed(2)}` }} · {{ row.account.scheduler_score?.sticky_weighted_enabled ? 'sticky' : 'base' }}</small></td>
                  <td><strong>{{ row.account.priority }}</strong><small>当前</small></td>
                  <td><strong>{{ formatCompactDate(row.account.created_at) }}</strong><small>{{ row.elapsedHours.toFixed(1) }}h 已计费</small></td>
                  <td><strong class="cost-lime">{{ row.billingMode === 'metered' && row.profile.source !== 'custom' ? '按 Token' : `${formatCny(row.hourlyCost, 5)}/h` }}</strong><small>{{ row.billingMode === 'metered' ? (row.profile.source === 'custom' ? '自动按量 + 固定附加' : '模型/渠道价格自动计算') : `配置推算 · ${row.profile.source === 'custom' ? '用户自定义' : '美国套餐默认'}` }}</small></td>
                  <td><strong class="cost-lime">{{ row.billingMode === 'metered' && row.profile.source !== 'custom' ? '无需设置' : formatCny(row.accrued, 3) }}</strong><small>{{ row.billingMode === 'metered' ? (row.profile.source === 'custom' ? `固定附加 · ${row.profile.billing_cycle}` : '不存在固定采购成本') : `配置推算 · ${row.profile.billing_cycle}` }}</small></td>
                  <td><strong>{{ formatUsd(row.today.cost, 4) }}</strong><small>标准 {{ formatUsd(row.today.standard_cost || 0, 4) }}</small></td>
                  <td><strong class="cost-lime">{{ formatUsd(actualUserCost(row.today), 3) }}</strong><small>今日用户计费</small></td>
                  <td><strong>{{ formatInteger(row.today.requests) }}</strong><small>{{ formatTokens(row.today.tokens) }} Token</small></td>
                  <UpstreamProbeCell :account-name="row.account.name" :state="probes[String(row.account.id)]" @probe="runProbe(row.account)" />
                  <td><strong>{{ row.currentState.error }} / {{ row.currentState.limited }}</strong><small>{{ row.currentState.note }}</small></td>
                  <td class="cost-group-cell"><span v-for="group in row.groups" :key="group" class="cost-tag">{{ group }}</span><span v-if="row.groups.length === 0" class="cost-tag">自用</span></td>
                  <td class="cost-actions-cell">
                    <button type="button" title="检测真实上游连接总耗时" aria-label="检测真实上游连接总耗时" :class="{ 'is-loading': probes[String(row.account.id)]?.loading, 'is-success': probes[String(row.account.id)]?.success === true, 'is-error': probes[String(row.account.id)]?.success === false }" :disabled="probes[String(row.account.id)]?.loading" @click.stop="runProbe(row.account)">
                      <LoaderCircle v-if="probes[String(row.account.id)]?.loading" :size="15" class="cost-spin" />
                      <FlaskConical v-else :size="15" />
                      <span class="cost-action-tooltip" role="tooltip">{{ probes[String(row.account.id)]?.loading ? '正在请求真实上游…' : '发送一次真实最小请求并测量完整测试耗时，会产生少量调用成本' }}</span>
                    </button>
                    <button type="button" :title="row.billingMode === 'metered' ? '查看 API 按量成本规则' : '配置账号采购成本'" :aria-label="row.billingMode === 'metered' ? '查看 API 按量成本规则' : '配置账号采购成本'" @click.stop="selectedAccount = row.account">
                      <Settings2 :size="15" />
                      <span class="cost-action-tooltip" role="tooltip">{{ row.billingMode === 'metered' ? '按 usage、实际上游模型、价格目录或渠道价格自动计算' : '设置采购金额、周期与成本起算时间' }}</span>
                    </button>
                  </td>
                </tr>
                <tr v-if="upstreamRows.length === 0"><td colspan="16" class="cost-empty-row">没有匹配的上游账号</td></tr>
              </tbody>
            </table>
          </div>
        </section>

        <section v-else-if="activePanel === 'oauth'" class="cost-workspace cost-oauth" aria-labelledby="oauth-title">
          <div class="cost-oauth-header">
            <div class="cost-page-heading cost-page-heading--compact">
              <div><span>ACCOUNT POOL / LIVE ECONOMICS</span><h2 id="oauth-title">渠道号池实时成本</h2><p>{{ lastUpdatedLabel }}</p></div>
            </div>
            <div class="cost-oauth-kpis">
              <MetricCell label="当前池 API 产出速率" :value="formatUsd(oauthOutputHourlyUsd, 2)" note="今日当前池实测 / 已过小时" accent="gold" />
              <MetricCell label="当前池综合成本" :value="`${formatCny(oauthCombinedHourlyCny, 2)}/小时`" note="真实 API + 配置推算采购" accent="gold" />
              <MetricCell label="今日当前池产出" :value="formatUsd(oauthTodayOutputUsd, 3)" note="现存账号今日真实用户计费" />
              <MetricCell label="今日剩余预期" :value="formatUsd(oauthRemainingForecastUsd, 2)" note="按当前池今日速率线性外推" />
              <MetricCell label="预计月采购" :value="formatCny(oauthMonthlyProcurementForecastCny, 1)" :note="`${selectedPlatformLabel} 当前 ${oauthAccounts.length} 个账号合计`" />
              <MetricCell label="用量窗口平均余量" :value="oauthUsageSyncedCount ? formatPercent(oauthQuotaRemainingAverage) : '等待同步'" :note="`${oauthUsageSyncedCount} / ${oauthAccounts.length} 个账号已同步 5h / 7d`" accent="lime" />
            </div>
          </div>

          <div class="cost-scope-notice">
            <strong>当前号池口径</strong>
              <span>这里只核算当前仍存在的 {{ selectedPlatformLabel }} 账号；删除账号后手动刷新立即移出，自动刷新最多等待 {{ refreshIntervalSeconds }} 秒。</span>
            <small>历史请求不会随账号删除而清空，仍保留在全局 usage_logs 趋势中。当前池起点：{{ currentPoolStartedAtLabel }}</small>
          </div>

          <div class="cost-pool-heading-row">
            <div class="cost-section-heading"><span>POOL / JOINED COST</span><h2>{{ selectedPlatformLabel }} 当前号池实时成本</h2><p>配置推算采购累计 · 有效账号 {{ oauthAccounts.length }} · 使用套餐默认成本 {{ defaultCostAccountCount }} 个</p></div>
            <div class="cost-platform-tabs" role="tablist" aria-label="渠道号池平台">
              <button v-for="item in accountPoolProviderTabs" :key="item.key" type="button" role="tab" :aria-selected="poolPlatform === item.key" :class="{ active: poolPlatform === item.key }" @click="poolPlatform = item.key">{{ item.label }}</button>
            </div>
            <button type="button" class="cost-primary-button cost-primary-button--outline" @click="reload"><RefreshCcw :size="16" /> 刷新号池核算</button>
          </div>

          <div class="cost-pool-summary">
            <MetricCell label="渠道账号" :value="formatInteger(oauthAccounts.length)" :note="`${oauthActiveCount} 个已产生请求`" />
            <MetricCell label="净采购成本（配置推算）" :value="formatCny(oauthAccruedCny, 2)" :note="`推算小时成本 ${formatCny(oauthHourlyCny, 4)}`" />
            <MetricCell label="今日 API 账号成本" :value="formatUsd(oauthTodayCostUsd, 4)" note="真实账号成本" accent="lime" />
            <MetricCell label="今日 API 产出" :value="formatUsd(oauthTodayOutputUsd, 4)" :note="`预估利润 ${formatUsd(oauthTodayOutputUsd - oauthTodayCostUsd, 3)}`" accent="blue" />
            <MetricCell label="号池状态" :value="`${oauthNormalCount} 正常`" :note="`限流 ${oauthLimitedCount} · 错误 ${oauthErrorCount}`" />
            <div class="cost-pool-output">
              <span>API 美元产出</span><strong>{{ formatUsd(oauthTodayOutputUsd, 2) }}</strong>
              <div><i :style="{ width: `${Math.min(100, poolOutputProgress)}%` }"></i></div>
              <small>{{ formatInteger(oauthRequests) }} 次请求 · {{ formatTokens(oauthTokens) }} Token</small>
            </div>
          </div>

          <div class="cost-chart-row">
            <ChartPanel title="全局 API 产出速率" :caption="`${rangeLabel} · 历史 usage_logs，可能包含已删除账号`">
              <CostLineChart :labels="trendLabels" :series="[{ label: '当前产出', data: trendActualCost, color: '#e0bd4e', fill: true }, { label: rollingTrendLabel, data: rollingTrendActualCost, color: '#b9e55a', dashed: true }]" value-prefix="$" />
            </ChartPanel>
            <ChartPanel title="当前号池采购费率" :caption="`${selectedPlatformLabel} 现存账号 · 确定性折算，不是历史实测`">
              <CostLineChart :labels="trendLabels" :series="[{ label: '当前池采购费率', data: oauthProcurementBaseline, color: '#7eb6d8', dashed: true }]" value-prefix="¥" />
            </ChartPanel>
          </div>

          <div class="cost-pool-table-wrap">
            <table class="cost-pool-table">
              <thead><tr><th>核算范围</th><th>账号类型</th><th>账号数</th><th>有产出</th><th>状态分布</th><th>采购成本（推算）</th><th>平均单价（推算）</th><th>当前产出 / 实时预期 / 月预期</th><th>成本计算</th><th>请求</th><th>Token</th></tr></thead>
              <tbody>
                <tr v-for="group in poolGroups" :key="group.plan">
                  <td><strong>当前号池</strong></td><td><strong>{{ group.planLabel }}</strong></td><td>{{ group.count }}</td><td>{{ group.productive }}</td>
                  <td><div class="cost-status-ring" :style="{ background: group.statusRing }"><span></span></div><small>正常 {{ group.normal }} · 限流 {{ group.limited }} · 错误 {{ group.errors }} · 窗口余量 {{ formatPercent(group.quotaRemaining) }}</small></td>
                  <td><strong>{{ formatCny(group.accruedCny, 2) }}</strong><small>小时 {{ formatCny(group.hourlyCny, 4) }}</small></td>
                  <td><strong>{{ formatCny(group.averageCostCny, 2) }}</strong><small>累计采购成本 / 号</small></td>
                  <td><strong class="cost-lime">{{ formatUsd(group.outputUsd, 2) }} / {{ formatUsd(group.outputForecastUsd, 2) }} / {{ formatUsd(group.monthForecastUsd, 2) }}</strong><div class="cost-pool-progress"><i :style="{ width: `${group.progress}%` }"></i></div><small>当前产出 / 实时预期 / 月预期</small></td>
                  <td><strong class="cost-lime">{{ formatCny(group.hourlyCny, 5) }} / {{ formatUsd(group.apiCostUsd, 5) }}</strong><small>采购小时成本 / API 账号成本</small></td>
                  <td>{{ formatInteger(group.requests) }}</td><td>{{ formatTokens(group.tokens) }}</td>
                </tr>
                <tr v-if="poolGroups.length === 0"><td colspan="11" class="cost-empty-row">当前渠道没有账号</td></tr>
              </tbody>
            </table>
          </div>
        </section>

        <section v-else class="cost-workspace cost-api-workspace" aria-labelledby="api-access-title">
          <CostApiAccessPanel :desktop-mode="desktopMode" :ops-overview="opsOverview" />
        </section>
      </main>

      <CostProfileInspector :account="selectedAccount" :account-count="bulkCostEdit ? selectedAccountIds.size : undefined" :saving="saving" :now="now" @close="closeCostProfileInspector" @save="saveSelectedCostProfile" />
    </div>
  </component>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Activity,
  ArrowLeftRight,
  BarChart3,
  Calculator,
  CircleCheck,
  Database,
  ExternalLink,
  FlaskConical,
  Gauge,
  KeyRound,
  LoaderCircle,
  Maximize2,
  Plus,
  RefreshCcw,
  Search,
  Settings2,
  ShoppingBag,
  TriangleAlert,
  TrendingUp,
  Trophy,
} from '@lucide/vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores'
import type { Account, AccountUsageInfo, WindowStats } from '@/types'
import type { ChannelModelPricing, ModelDefaultPricing } from '@/api/admin/channels'
import CostLineChart from '@/features/cost-center/components/CostLineChart.vue'
import CostApiAccessPanel from '@/features/cost-center/components/CostApiAccessPanel.vue'
import CostProfileInspector from '@/features/cost-center/components/CostProfileInspector.vue'
import UpstreamProbeCell from '@/features/cost-center/components/UpstreamProbeCell.vue'
import {
  accruedCost,
  actualUserCost,
  COST_ALGORITHM_VERSION,
  convertCurrency,
  elapsedHours,
  formatMoney,
  hourlyRate,
  inferPlan,
  isDefaultSubscriptionCostProfile,
  resolveAccountBillingMode,
  resolveCostProfile,
  type CostProfile,
} from '@/features/cost-center/model'
import {
  DEFAULT_COST_CENTER_RANGE,
  useCostCenterData,
  type CostCenterRange,
} from '@/features/cost-center/useCostCenterData'
import { costTrendBucketHours as resolveCostTrendBucketHours } from '@/features/cost-center/usageWindow'
import type { ModelRouteRow } from '@/features/cost-center/modelRouteAnalysis'
import { calculateDesktopScale } from '@/features/cost-center/desktopScale'
import { ACCOUNT_PURCHASE_URL, openProjectExternalUrl } from '@/features/desktop/externalLinks'
import {
  buildModelCostRows,
  describeCostAccountOrigin,
  summarizeModelCosts,
} from '@/features/cost-center/modelCostAnalysis'
import {
  describeCurrentAccountState,
  normalizeSchedulerScore,
  resolveSchedulerBaseScoreMax,
} from '@/features/cost-center/upstreamTable'
import {
  buildAccountPoolProviderTabs,
  buildUpstreamProviderTabs,
  matchesAccountPoolProvider,
  matchesUpstreamProvider,
  type UpstreamProviderFilter,
} from '@/features/cost-center/upstreamProvider'

type WorkspaceKey = 'overview' | 'upstreams' | 'oauth' | 'api'
type RankingMetric = 'score' | 'output' | 'requests' | 'cost' | 'latency' | 'reliability'

const MetricCell = defineComponent({
  props: { label: String, value: String, note: String, accent: String },
  setup(props) {
    return () => h('div', { class: ['cost-metric-cell', props.accent ? `accent-${props.accent}` : ''] }, [
      h('span', props.label), h('strong', props.value), h('small', props.note),
    ])
  },
})

const ChartPanel = defineComponent({
  props: { title: String, caption: String },
  setup(props, { slots, attrs }) {
    return () => h('section', { ...attrs, class: ['cost-chart-panel', attrs.class] }, [
      h('div', { class: 'cost-panel-heading' }, [h('strong', props.title), h('span', props.caption)]),
      slots.default?.(),
    ])
  },
})

const StatusLabel = defineComponent({
  props: { status: String, schedulable: Boolean },
  setup(props) {
    return () => h('div', { class: ['cost-status-label', `status-${props.status}`] }, [
      h('i'), h('strong', props.status === 'active' ? (props.schedulable ? '可调度' : '已停调度') : props.status),
      h('small', props.schedulable ? 'active' : 'unscheduled'),
    ])
  },
})

const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const data = useCostCenterData()
const {
  accounts, accountUsage, stats, trend, trendUsesAccountCost, models, modelCostSource, modelCostAccountId, modelCostRange, modelRoutes, modelRoutesTruncated, modelPricing, pricingStatus, pricingRefreshing, opsOverview, opsTrend, systemSettings, probes, loading, saving, error, lastUpdated, exchangeRate,
} = data

const workspaceItems = [
  { key: 'overview' as const, label: '资产总览', shortcut: '1', icon: Gauge },
  { key: 'upstreams' as const, label: '上游排行', shortcut: '2', icon: Database },
  { key: 'oauth' as const, label: '渠道号池', shortcut: '3', icon: BarChart3 },
  { key: 'api' as const, label: 'API 接入', shortcut: '4', icon: KeyRound },
]
const distributionColors = ['#b9e55a', '#79b6d9', '#d6aa47', '#d58473', '#8a91bf', '#72a68d', '#b78db7']

const activePanel = ref<WorkspaceKey>(normalizePanel(route.query.panel))
const range = ref<CostCenterRange>(DEFAULT_COST_CENTER_RANGE)
const platformFilter = ref<UpstreamProviderFilter>('all')
const rankingMetric = ref<RankingMetric>('score')
const poolPlatform = ref<UpstreamProviderFilter>('all')
const searchQuery = ref('')
const selectedAccount = ref<Account | null>(null)
const selectedAccountIds = ref<Set<number>>(new Set())
const bulkCostEdit = ref(false)
const now = ref(new Date())
const autoRefresh = ref(true)
const refreshIntervalSeconds = ref<5 | 10 | 15 | 30>(30)
const countdown = ref(refreshIntervalSeconds.value)
let clockTimer: number | null = null

const isTauriDesktop = '__TAURI_INTERNALS__' in (window as any)
const viewport = ref(readDesktopViewport())
const desktopMode = computed(() => route.query.desktop === '1' || isTauriDesktop)
const desktopScale = computed(() => isTauriDesktop
  ? calculateDesktopScale(viewport.value)
  : { scale: 1, effectiveWidth: viewport.value.cssWidth, physicalWidth: viewport.value.cssWidth, useWideToolbar: false })

function openAccountPurchase() {
  openProjectExternalUrl(ACCOUNT_PURCHASE_URL).catch((reason) => {
    error.value = reason instanceof Error ? reason.message : String(reason)
  })
}
const panelTitle = computed(() => activePanel.value === 'overview' ? '上游资产与实时成本' : activePanel.value === 'upstreams' ? '上游运行矩阵' : activePanel.value === 'oauth' ? 'OAuth 实时成本' : 'API 接入中心')
const panelDescription = computed(() => activePanel.value === 'overview' ? '评分、调度、采购与 API 产出统一视图' : activePanel.value === 'upstreams' ? '桌面级密集账号巡检与成本操作台' : activePanel.value === 'oauth' ? '号码加入即起算的号池经济模型' : '本地网关、Agent 配置与延迟诊断')
const rangeLabels: Record<CostCenterRange, string> = { today: '当天', '1m': '最近 1 分钟', '5m': '最近 5 分钟', '30m': '最近 30 分钟', '1h': '最近 1 小时', '6h': '最近 6 小时', '24h': '最近 24 小时', '7d': '最近 7 天', '30d': '最近 1 个月' }
const rangeLabel = computed(() => rangeLabels[range.value])
const modelCostRangeLabel = computed(() => rangeLabels[modelCostRange.value])
const modelCostSourceLabel = computed(() => ({ requested: '用户请求模型', upstream: '实际上游模型', mapping: '请求 → 上游映射' })[modelCostSource.value])
const lastUpdatedLabel = computed(() => lastUpdated.value ? lastUpdated.value.toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '等待首次刷新')
const exchangeRateLabel = computed(() => {
  const date = exchangeRate.value.rateDate ? ` · ${exchangeRate.value.rateDate}` : ''
  if (exchangeRate.value.source === 'network') return `网络参考价${date}`
  if (exchangeRate.value.source === 'cache') return `12 小时缓存${date}`
  return '离线回退值'
})
const modelCostRows = computed(() => buildModelCostRows(models.value))
const modelCostSummary = computed(() => summarizeModelCosts(modelCostRows.value))
const pricingCatalogLabel = computed(() => {
  if (!pricingStatus.value) return '状态不可用'
  const synchronized = pricingStatus.value.catalog_source !== ''
    && pricingStatus.value.catalog_source === pricingStatus.value.configured_source
  return `${formatInteger(pricingStatus.value.model_count)} 个模型 · ${synchronized ? '缓存已同步' : '等待同步'}`
})
const pricingCatalogDetail = computed(() => {
  if (!pricingStatus.value) return '当前内核未提供价格目录状态接口'
  const updated = new Date(pricingStatus.value.last_updated)
  const updatedLabel = Number.isFinite(updated.getTime()) ? updated.toLocaleString() : '更新时间未知'
  let source = pricingStatus.value.catalog_source || pricingStatus.value.configured_source || '未配置远程目录'
  try { source = new URL(source).hostname } catch { /* keep the configured label */ }
  return `${updatedLabel} · ${source} · 每 ${pricingStatus.value.update_interval_hours || 24} 小时自动同步 · ${pricingStatus.value.fallback_available ? '离线兜底已加载' : '离线兜底缺失'}`
})
const modelCostAccountSelection = computed({
  get: () => modelCostAccountId.value == null ? 'all' : String(modelCostAccountId.value),
  set: (value: string) => {
    const parsed = Number(value)
    modelCostAccountId.value = value === 'all' || !Number.isFinite(parsed) ? null : parsed
  },
})
const modelCostAccountLabel = computed(() => {
  if (modelCostAccountId.value == null) return '全部账号合计'
  const account = accounts.value.find((item) => item.id === modelCostAccountId.value)
  return account ? `${account.name} · ${describeCostAccountOrigin(account)}` : `账号 #${modelCostAccountId.value}`
})

const activeAccounts = computed(() => accounts.value.filter((account) => account.status === 'active' && account.schedulable))
const upstreamProviderTabs = computed(() => buildUpstreamProviderTabs(accounts.value))
const totalObservedRequests = computed(() => opsOverview.value?.request_count_total ?? trend.value.reduce((sum, point) => sum + Number(point.requests || 0), 0))
const successCount = computed(() => opsOverview.value?.success_count ?? Math.max(0, totalObservedRequests.value - errorCount.value))
const errorCount = computed(() => opsOverview.value?.error_count_total ?? accounts.value.filter((account) => account.status === 'error').length)
const errorRate = computed(() => opsOverview.value?.error_rate ?? (totalObservedRequests.value > 0 ? errorCount.value / totalObservedRequests.value : 0))
const switchCount = computed(() => opsTrend.value.reduce((sum, point) => sum + Number(point.switch_count || 0), 0))
const ttftP95 = computed(() => Number(opsOverview.value?.ttft?.p95_ms || 0))
const qualityScore = computed(() => {
  const server = Number(opsOverview.value?.health_score)
  if (opsOverview.value?.health_score != null && Number.isFinite(server)) return Math.max(0, Math.min(100, server))
  const availability = accounts.value.length ? activeAccounts.value.length / accounts.value.length : 0
  return Math.max(0, Math.min(100, availability * 82 + (1 - errorRate.value) * 18))
})
const qualityGrade = computed(() => scoreGrade(qualityScore.value))
const schedulerBaseScoreMax = computed(() => resolveSchedulerBaseScoreMax(systemSettings.value as Record<string, unknown> | null))

const accountLedgers = computed(() => accounts.value.map((account) => {
  const profile = resolveCostProfile(account)
  const today = data.accountStats.value(account)
  const usage = accountUsage.value[String(account.id)]
  return {
    account,
    billingMode: resolveAccountBillingMode(account),
    profile,
    usage,
    plan: inferPlan(account),
    hourlyCny: convertCurrency(hourlyRate(profile), profile.currency, 'CNY', exchangeRate.value.rate),
    accruedCny: convertCurrency(accruedCost(profile, now.value), profile.currency, 'CNY', exchangeRate.value.rate),
    elapsedHours: elapsedHours(profile.started_at, now.value),
    today,
  }
}))
const usageSyncedCount = computed(() => accountLedgers.value.filter((row) => hasPrimaryUsageWindow(row.usage)).length)
const quotaRemainingAverage = computed(() => averageQuotaRemaining(accountLedgers.value.map((row) => row.usage)))
const totalAccruedCny = computed(() => accountLedgers.value.reduce((sum, row) => sum + row.accruedCny, 0))
const procurementHourlyCny = computed(() => accountLedgers.value.reduce((sum, row) => sum + row.hourlyCny, 0))
const monthlyProcurementForecastCny = computed(() => procurementHourlyCny.value * 730)
const defaultCostProfileCount = computed(() => accountLedgers.value.filter((row) => isDefaultSubscriptionCostProfile(row.account)).length)
const todayAccountCostUsd = computed(() => Number(stats.value?.today_account_cost ?? accountLedgers.value.reduce((sum, row) => sum + Number(row.today.cost || 0), 0)))
const dayElapsedHours = computed(() => Math.max(1 / 60, now.value.getHours() + now.value.getMinutes() / 60))
const selectedRangeHours = computed(() => ({ today: dayElapsedHours.value, '1m': 1 / 60, '5m': 5 / 60, '30m': .5, '1h': 1, '6h': 6, '24h': 24, '7d': 168, '30d': 720 })[range.value])
const trendBucketHours = computed(() => resolveCostTrendBucketHours(range.value))
const trendSmoothingPoints = computed(() => ({ today: 4, '1m': 2, '5m': 3, '30m': 5, '1h': 10, '6h': 3, '24h': 4, '7d': 3, '30d': 7 })[range.value])
const rollingTrendLabel = computed(() => ({ today: '4 小时移动平均', '1m': '2 点移动平均', '5m': '3 分钟移动平均', '30m': '5 分钟移动平均', '1h': '10 分钟移动平均', '6h': '3 小时移动平均', '24h': '4 小时移动平均', '7d': '3 天移动平均', '30d': '7 天移动平均' })[range.value])
const requestSmoothingPoints = computed(() => ({ today: 12, '1m': 2, '5m': 3, '30m': 5, '1h': 10, '6h': 6, '24h': 12, '7d': 6, '30d': 6 })[range.value])
const windowActualOutputUsd = computed(() => trend.value.reduce((sum, point) => sum + Number(point.actual_cost || 0), 0))
const windowAccountCostUsd = computed(() => trend.value.reduce((sum, point) => sum + Number(point.account_cost ?? point.cost ?? 0), 0))
const apiCostBasisLabel = computed(() => trendUsesAccountCost.value ? 'API 账号实算成本' : 'API 标准价成本')
const apiOutputHourlyUsd = computed(() => windowActualOutputUsd.value / selectedRangeHours.value)
const combinedHourlyCny = computed(() => procurementHourlyCny.value + windowAccountCostUsd.value * exchangeRate.value.rate / selectedRangeHours.value)
const todayRequests = computed(() => Number(stats.value?.today_requests ?? accountLedgers.value.reduce((sum, row) => sum + row.today.requests, 0)))

const trendLabels = computed(() => trend.value.map((point) => formatTrendLabel(point.date)))
const trendActualCost = computed(() => trend.value.map((point) => Number(point.actual_cost || 0) / trendBucketHours.value))
const rollingTrendActualCost = computed(() => movingAverage(trendActualCost.value, trendSmoothingPoints.value))
const rollingOutputUsd = computed(() => rollingTrendActualCost.value.at(-1) ?? apiOutputHourlyUsd.value)
const trendStandardCost = computed(() => trend.value.map((point) => Number(point.account_cost ?? point.cost ?? 0) * exchangeRate.value.rate / trendBucketHours.value))
const procurementBaseline = computed(() => trend.value.map(() => procurementHourlyCny.value))
const trendCombinedCost = computed(() => trendStandardCost.value.map((value, index) => value + (procurementBaseline.value[index] ?? 0)))
const qualityTrendLabels = computed(() => opsTrend.value.length ? opsTrend.value.map((point) => formatTrendLabel(point.bucket_start)) : trendLabels.value)
const requestVolumeTrend = computed(() => opsTrend.value.length ? opsTrend.value.map((point) => Number(point.request_count || 0)) : trend.value.map((point) => Number(point.requests || 0)))

const platformDistributionUsesCost = computed(() => accountLedgers.value.some((row) => row.accruedCny > 0))
const platformDistribution = computed(() => {
  const totals = new Map<string, number>()
  for (const row of accountLedgers.value) {
    const value = platformDistributionUsesCost.value ? row.accruedCny : 1
    totals.set(row.account.platform, (totals.get(row.account.platform) || 0) + value)
  }
  return [...totals.entries()].sort((a, b) => b[1] - a[1])
})
const platformDonutBackground = computed(() => donutGradient(platformDistribution.value.map(([label, value], index) => ({ label, value, color: distributionColors[index % distributionColors.length] }))))
const platformDonutModeLabel = computed(() => platformDistributionUsesCost.value ? '累计采购（配置推算）' : '采购未配置 · 环图为账号结构')
const platformDonutLabel = computed(() => platformDistribution.value.map(([name, value]) => platformDistributionUsesCost.value ? `${name} ${formatCny(value, 2)}` : `${name} ${formatInteger(value)} 个账号`).join('，'))

const accountDistribution = computed(() => {
  const rows = accountLedgers.value.filter((row) => row.today.requests > 0).sort((a, b) => b.today.requests - a.today.requests).slice(0, 6)
  const total = Math.max(1, rows.reduce((sum, row) => sum + row.today.requests, 0))
  return rows.map((row, index) => ({
    id: row.account.id, name: row.account.name, platform: row.account.platform, hourlyCost: row.hourlyCny,
    share: row.today.requests / total, value: row.today.requests, color: distributionColors[index % distributionColors.length],
  }))
})
const accountDonutBackground = computed(() => donutGradient(accountDistribution.value))

function rankingValue(row: { score: number; hourlyCost: number; today: WindowStats; account: Account; usage?: AccountUsageInfo }, metric: RankingMetric): number {
  if (metric === 'output') return actualUserCost(row.today)
  if (metric === 'requests') return Number(row.today.requests || 0)
  if (metric === 'cost') return Number(row.today.cost || 0)
  if (metric === 'reliability') {
    const statusScore = row.account.status === 'active' ? 1 : 0
    const schedulableScore = row.account.schedulable ? 1 : 0
    return statusScore * 2 + schedulableScore + row.score / 100 + quotaRemainingRatio(row.usage)
  }
  if (metric === 'latency') return probes.value[String(row.account.id)]?.latency_ms ?? Number.POSITIVE_INFINITY
  return row.score
}

const upstreamRows = computed(() => {
  const query = searchQuery.value.toLowerCase()
  const rows = accountLedgers.value
    .filter((row) => matchesUpstreamProvider(row.account, platformFilter.value))
    .filter((row) => !query || [row.account.name, row.account.notes, row.account.platform, describeCostAccountOrigin(row.account), ...(row.account.groups?.map((group) => group.name) || [])].some((value) => String(value || '').toLowerCase().includes(query)))
    .map((row) => {
      const rawScore = Number(row.account.scheduler_score?.base_score)
      const hasRawScore = Number.isFinite(rawScore)
      return {
        ...row,
        score: hasRawScore ? normalizeSchedulerScore(rawScore, schedulerBaseScoreMax.value) : qualityScore.value,
        scoreRaw: hasRawScore ? rawScore : null,
        scoreMax: schedulerBaseScoreMax.value,
        hourlyCost: row.hourlyCny,
        accrued: row.accruedCny,
        currentState: describeCurrentAccountState(row.account),
        groups: row.account.groups?.map((group) => group.name) ?? [],
      }
    })
  return rows
    .sort((a, b) => {
      const aValue = rankingValue(a, rankingMetric.value)
      const bValue = rankingValue(b, rankingMetric.value)
      if (rankingMetric.value === 'latency') {
        const aMissing = !Number.isFinite(aValue)
        const bMissing = !Number.isFinite(bValue)
        if (aMissing !== bMissing) return aMissing ? 1 : -1
        return aValue - bValue || b.score - a.score
      }
      return bValue - aValue || b.score - a.score
    })
    .map((row, index) => ({ ...row, rank: index + 1 }))
})

const allVisibleAccountsSelected = computed(() => upstreamRows.value.length > 0 && upstreamRows.value.every((row) => selectedAccountIds.value.has(row.account.id)))
const someVisibleAccountsSelected = computed(() => upstreamRows.value.some((row) => selectedAccountIds.value.has(row.account.id)))

function toggleAccountSelection(accountId: number) {
  const next = new Set(selectedAccountIds.value)
  if (next.has(accountId)) next.delete(accountId)
  else next.add(accountId)
  selectedAccountIds.value = next
}

function toggleSelectAllVisible() {
  const next = new Set(selectedAccountIds.value)
  if (allVisibleAccountsSelected.value) upstreamRows.value.forEach((row) => next.delete(row.account.id))
  else upstreamRows.value.forEach((row) => next.add(row.account.id))
  selectedAccountIds.value = next
}

function clearSelection() {
  selectedAccountIds.value = new Set()
}

function openBulkCostProfile() {
  const first = upstreamRows.value.find((row) => selectedAccountIds.value.has(row.account.id))
  if (!first) return
  bulkCostEdit.value = true
  selectedAccount.value = first.account
}

function closeCostProfileInspector() {
  selectedAccount.value = null
  bulkCostEdit.value = false
}

const accountPoolProviderTabs = computed(() => buildAccountPoolProviderTabs(accounts.value))
const oauthAccounts = computed(() => accountLedgers.value.filter((row) => matchesAccountPoolProvider(row.account, poolPlatform.value)))
const selectedPlatformLabel = computed(() => poolPlatform.value === 'all'
  ? '全部渠道'
  : accountPoolProviderTabs.value.find((item) => item.key === poolPlatform.value)?.label || '当前渠道')
const defaultCostAccountCount = computed(() => oauthAccounts.value.filter((row) => isDefaultSubscriptionCostProfile(row.account)).length)
const oauthAccruedCny = computed(() => oauthAccounts.value.reduce((sum, row) => sum + row.accruedCny, 0))
const oauthHourlyCny = computed(() => oauthAccounts.value.reduce((sum, row) => sum + row.hourlyCny, 0))
const oauthTodayCostUsd = computed(() => oauthAccounts.value.reduce((sum, row) => sum + Number(row.today.cost || 0), 0))
const oauthTodayOutputUsd = computed(() => oauthAccounts.value.reduce((sum, row) => sum + actualUserCost(row.today), 0))
const oauthRequests = computed(() => oauthAccounts.value.reduce((sum, row) => sum + row.today.requests, 0))
const oauthTokens = computed(() => oauthAccounts.value.reduce((sum, row) => sum + row.today.tokens, 0))
const oauthUsageSyncedCount = computed(() => oauthAccounts.value.filter((row) => hasPrimaryUsageWindow(row.usage)).length)
const oauthQuotaRemainingAverage = computed(() => averageQuotaRemaining(oauthAccounts.value.map((row) => row.usage)))
const oauthOutputHourlyUsd = computed(() => oauthTodayOutputUsd.value / dayElapsedHours.value)
const oauthCombinedHourlyCny = computed(() => oauthHourlyCny.value + oauthTodayCostUsd.value * exchangeRate.value.rate / dayElapsedHours.value)
const oauthRemainingForecastUsd = computed(() => Math.max(0, oauthOutputHourlyUsd.value * Math.max(0, 24 - dayElapsedHours.value)))
const oauthMonthlyProcurementForecastCny = computed(() => oauthHourlyCny.value * 730)
const oauthProcurementBaseline = computed(() => trend.value.map(() => oauthHourlyCny.value))
const currentPoolStartedAtLabel = computed(() => {
  const timestamps = oauthAccounts.value
    .map((row) => new Date(row.account.created_at).getTime())
    .filter(Number.isFinite)
  if (!timestamps.length) return '当前无账号'
  return new Date(Math.min(...timestamps)).toLocaleString([], {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
})
const oauthActiveCount = computed(() => oauthAccounts.value.filter((row) => row.today.requests > 0).length)
const oauthNormalCount = computed(() => oauthAccounts.value.filter((row) => row.account.status === 'active' && !row.account.rate_limited_at).length)
const oauthLimitedCount = computed(() => oauthAccounts.value.filter((row) => Boolean(row.account.rate_limited_at)).length)
const oauthErrorCount = computed(() => oauthAccounts.value.filter((row) => row.account.status === 'error').length)
const poolOutputProgress = computed(() => oauthTodayOutputUsd.value > 0 ? oauthTodayOutputUsd.value / Math.max(oauthTodayOutputUsd.value, oauthTodayCostUsd.value + oauthHourlyCny.value / exchangeRate.value.rate * dayElapsedHours.value) * 100 : 0)
const poolGroups = computed(() => {
  const planOrder = ['metered', 'free', 'k12', 'plus', 'pro', 'team', 'business', 'unknown']
  return planOrder.map((plan) => {
    const rows = oauthAccounts.value.filter((row) => plan === 'metered'
      ? row.billingMode === 'metered'
      : row.billingMode === 'subscription' && row.plan === plan)
    if (!rows.length) return null
    const accruedCny = rows.reduce((sum, row) => sum + row.accruedCny, 0)
    const hourlyCny = rows.reduce((sum, row) => sum + row.hourlyCny, 0)
    const outputUsd = rows.reduce((sum, row) => sum + actualUserCost(row.today), 0)
    const apiCostUsd = rows.reduce((sum, row) => sum + Number(row.today.cost || 0), 0)
    const requests = rows.reduce((sum, row) => sum + row.today.requests, 0)
    const tokens = rows.reduce((sum, row) => sum + row.today.tokens, 0)
    const normal = rows.filter((row) => row.account.status === 'active' && !row.account.rate_limited_at).length
    const limited = rows.filter((row) => Boolean(row.account.rate_limited_at)).length
    const errors = rows.filter((row) => row.account.status === 'error').length
    const quotaRemaining = averageQuotaRemaining(rows.map((row) => row.usage))
    const outputForecastUsd = outputUsd / dayElapsedHours.value * 24
    const monthForecastUsd = outputUsd / dayElapsedHours.value * 730
    return {
      plan, planLabel: plan === 'metered' ? 'API 按量' : plan === 'unknown' ? 'Other' : plan === 'k12' ? 'K12' : plan[0].toUpperCase() + plan.slice(1),
      count: rows.length, productive: rows.filter((row) => row.today.requests > 0).length,
      normal, limited, errors, quotaRemaining, accruedCny, hourlyCny, averageCostCny: accruedCny / rows.length,
      outputUsd, outputForecastUsd, monthForecastUsd, apiCostUsd, requests, tokens,
      progress: Math.min(100, outputForecastUsd > 0 ? outputUsd / outputForecastUsd * 100 : 0),
      statusRing: statusRingGradient(normal, limited, errors),
    }
  }).filter(Boolean) as Array<any>
})

watch(activePanel, (panel) => router.replace({ query: { ...route.query, panel } }))
watch(range, () => reload())
watch([modelCostSource, modelCostAccountId, modelCostRange], () => reload())
watch(refreshIntervalSeconds, (seconds) => { countdown.value = seconds })
watch(upstreamProviderTabs, (tabs) => {
  if (!tabs.some((tab) => tab.key === platformFilter.value)) platformFilter.value = 'all'
})
watch(accountPoolProviderTabs, (tabs) => {
  if (!tabs.some((tab) => tab.key === poolPlatform.value)) poolPlatform.value = 'all'
})

function normalizePanel(value: unknown): WorkspaceKey {
  return value === 'upstreams' || value === 'oauth' || value === 'api' ? value : 'overview'
}

function scoreGrade(score: number): string { return score >= 82 ? 'A' : score >= 70 ? 'B' : score >= 58 ? 'C' : 'D' }
function movingAverage(values: number[], windowSize: number): number[] { return values.map((_, index) => { const slice = values.slice(Math.max(0, index - windowSize + 1), index + 1); return slice.reduce((sum, value) => sum + value, 0) / Math.max(1, slice.length) }) }
function formatInteger(value: number): string { return Math.round(Number(value) || 0).toLocaleString() }
function formatPercent(value: number): string { return `${((Number(value) || 0) * 100).toFixed(1)}%` }
function formatCny(value: number, digits = 2): string { return formatMoney(Number(value) || 0, 'CNY', digits) }
function formatUsd(value: number, digits = 2): string { return formatMoney(Number(value) || 0, 'USD', digits) }
function formatUsdPerMillion(value: number | null | undefined): string { return value == null || !Number.isFinite(Number(value)) ? '—' : `${formatUsd(Number(value) * 1_000_000, 4)}/M` }
function formatDuration(value: number): string { return value > 0 ? value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${Math.round(value)} ms` : '—' }
function formatTokens(value: number): string { const number = Number(value) || 0; return number >= 1e9 ? `${(number / 1e9).toFixed(2)}B` : number >= 1e6 ? `${(number / 1e6).toFixed(2)}M` : number >= 1e3 ? `${(number / 1e3).toFixed(1)}K` : formatInteger(number) }
function formatCacheHitRate(inputTokens: number, cacheReadTokens: number): string { const total = (Number(inputTokens) || 0) + (Number(cacheReadTokens) || 0); return total > 0 ? `输入缓存命中 ${formatPercent((Number(cacheReadTokens) || 0) / total)}` : '无输入 Token' }
function resolveRoutePricing(row: ModelRouteRow): ChannelModelPricing | ModelDefaultPricing | undefined { return row.channelPricing ?? modelPricing.value[row.upstreamModel] }
function routePricingSource(row: ModelRouteRow): string { return row.channelPricing ? '渠道自定义价' : modelPricing.value[row.upstreamModel]?.found ? '当前模型目录' : '未找到价格' }
function formatCompactDate(value: string): string { const date = new Date(value); return Number.isFinite(date.getTime()) ? date.toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—' }
function clampUtilization(value: number): number { return Math.max(0, Math.min(100, Number(value) || 0)) }
function formatUtilization(value: number): string { return `${Math.round(clampUtilization(value))}%` }
function hasPrimaryUsageWindow(usage?: AccountUsageInfo): boolean { return Boolean(usage?.five_hour || usage?.seven_day) }
function quotaRemainingRatio(usage?: AccountUsageInfo): number {
  const windows = [usage?.five_hour, usage?.seven_day].filter(Boolean)
  if (!windows.length) return 1
  const mostConstrained = Math.max(...windows.map((window) => clampUtilization(window!.utilization)))
  return Math.max(0, 1 - mostConstrained / 100)
}
function averageQuotaRemaining(usages: Array<AccountUsageInfo | undefined>): number {
  const synced = usages.filter(hasPrimaryUsageWindow)
  return synced.length ? synced.reduce((sum, usage) => sum + quotaRemainingRatio(usage), 0) / synced.length : 0
}
function formatUsageReset(value: string | null): string {
  if (!value) return '重置未知'
  const remainingMs = new Date(value).getTime() - now.value.getTime()
  if (!Number.isFinite(remainingMs) || remainingMs <= 0) return '正在重置'
  const minutes = Math.ceil(remainingMs / 60_000)
  if (minutes < 60) return `${minutes}m 后`
  const hours = Math.floor(minutes / 60)
  const restMinutes = minutes % 60
  if (hours < 24) return `${hours}h ${restMinutes}m 后`
  return `${Math.floor(hours / 24)}d ${hours % 24}h 后`
}
function formatTrendLabel(value: string): string { const date = new Date(value.includes(' ') ? value.replace(' ', 'T') : value); return Number.isFinite(date.getTime()) ? date.toLocaleString([], range.value === '7d' || range.value === '30d' ? { month: '2-digit', day: '2-digit' } : { hour: '2-digit', minute: '2-digit' }) : value }
function donutGradient(items: Array<{ value: number; color: string }>): string { const total = items.reduce((sum, item) => sum + Math.max(0, item.value), 0); if (!total) return 'conic-gradient(#303830 0 100%)'; let cursor = 0; const stops = items.map((item) => { const start = cursor; cursor += Math.max(0, item.value) / total * 100; return `${item.color} ${start}% ${cursor}%` }); return `conic-gradient(${stops.join(', ')})` }
function statusRingGradient(normal: number, limited: number, errors: number): string { const total = Math.max(1, normal + limited + errors); const normalEnd = normal / total * 100; const limitedEnd = normalEnd + limited / total * 100; return `conic-gradient(#b9e55a 0 ${normalEnd}%, #9c8a54 ${normalEnd}% ${limitedEnd}%, #995c50 ${limitedEnd}% 100%)` }
async function reload() {
  if (loading.value) return
  await data.reload(range.value)
  countdown.value = refreshIntervalSeconds.value
}
async function refreshPricingCatalog() {
  if (pricingRefreshing.value) return
  try {
    const status = await data.refreshPricingCatalog()
    appStore.showSuccess(`价格目录已同步，共 ${formatInteger(status.model_count)} 个模型；新价格从后续请求开始生效`)
  } catch (refreshError: any) {
    appStore.showError(`价格目录同步失败：${refreshError?.message || '远程接口不可用'}`)
  }
}
function toggleAutoRefresh() { autoRefresh.value = !autoRefresh.value; countdown.value = refreshIntervalSeconds.value }
function goToAccounts() { router.push('/admin/accounts') }
async function runProbe(account: Account) {
  if (probes.value[String(account.id)]?.loading) return
  appStore.showInfo(`正在检测 ${account.name}，将发送一次真实最小请求`, 2500)
  try {
    const result = await data.probeAccount(account)
    if (result.success) appStore.showSuccess(`${account.name} 连接测试完成，总耗时 ${formatInteger(result.latency_ms || 0)} ms`)
    else appStore.showError(`${account.name} 检测失败：${result.message}`)
  } catch (probeError: any) {
    appStore.showError(`检测 ${account.name} 失败：${probeError?.message || '请求异常'}`)
  }
}
async function saveSelectedCostProfile(profile: CostProfile) {
  if (!selectedAccount.value) return
  try {
    if (bulkCostEdit.value) {
      const ids = [...selectedAccountIds.value]
      await data.bulkSaveCostProfile(ids, profile)
      await data.reload(range.value)
      appStore.showSuccess(`已为 ${ids.length} 个账号保存成本档案`)
      clearSelection()
      closeCostProfileInspector()
      return
    }
    const updated = await data.saveCostProfile(selectedAccount.value, profile)
    selectedAccount.value = updated
    appStore.showSuccess('成本档案已保存，累计成本已重新计算')
  } catch (saveError: any) {
    appStore.showError(saveError?.message || '成本档案保存失败')
  }
}
async function toggleFullscreen() { if ('__TAURI_INTERNALS__' in (window as any)) { const { getCurrentWindow } = await import('@tauri-apps/api/window'); const current = getCurrentWindow(); await current.setFullscreen(!(await current.isFullscreen())) } else if (!document.fullscreenElement) await document.documentElement.requestFullscreen(); else await document.exitFullscreen() }

function handleKeydown(event: KeyboardEvent) {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'r') { event.preventDefault(); reload(); return }
  if (event.key === 'F11') { event.preventDefault(); toggleFullscreen(); return }
  if (event.key === 'Escape' && selectedAccount.value) { selectedAccount.value = null; return }
  if ((event.ctrlKey || event.metaKey) && ['1', '2', '3', '4'].includes(event.key)) { event.preventDefault(); activePanel.value = workspaceItems[Number(event.key) - 1].key }
}

async function refreshOnResume() {
  if (document.visibilityState !== 'visible') return
  countdown.value = refreshIntervalSeconds.value
  await reload()
}

function readDesktopViewport() {
  return {
    cssWidth: window.innerWidth,
    cssHeight: window.innerHeight,
    devicePixelRatio: window.devicePixelRatio || 1,
  }
}

function handleViewportResize() {
  viewport.value = readDesktopViewport()
}

onMounted(async () => {
  window.addEventListener('keydown', handleKeydown)
  window.addEventListener('resize', handleViewportResize)
  window.visualViewport?.addEventListener('resize', handleViewportResize)
  document.addEventListener('visibilitychange', refreshOnResume)
  window.addEventListener('focus', refreshOnResume)
  clockTimer = window.setInterval(() => {
    now.value = new Date()
    if (!autoRefresh.value || document.visibilityState !== 'visible') return
    countdown.value -= 1
    if (countdown.value <= 0) reload()
  }, 1000)
  await reload()
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleKeydown)
  window.removeEventListener('resize', handleViewportResize)
  window.visualViewport?.removeEventListener('resize', handleViewportResize)
  document.removeEventListener('visibilitychange', refreshOnResume)
  window.removeEventListener('focus', refreshOnResume)
  if (clockTimer !== null) window.clearInterval(clockTimer)
})
</script>

<style scoped>
.cost-console {
  --cost-bg: #0d110e;
  --cost-panel: #141915;
  --cost-panel-2: #1b211b;
  --cost-line: #303830;
  --cost-line-strong: #424d43;
  --cost-text: #e9ede6;
  --cost-muted: #7f8b81;
  --cost-lime: #b9e55a;
  --cost-gold: #dfbc4c;
  --cost-blue: #7eb6d8;
  min-height: 100vh;
  width: 100%;
  max-width: 100%;
  overflow-x: hidden;
  color: var(--cost-text);
  background-color: var(--cost-bg);
  background-image: linear-gradient(rgb(76 91 78 / 12%) 1px, transparent 1px), linear-gradient(90deg, rgb(76 91 78 / 12%) 1px, transparent 1px);
  background-size: 36px 36px;
  font-family: 'Segoe UI Variable', 'Microsoft YaHei UI', sans-serif;
  user-select: none;
  -webkit-user-select: none;
  -webkit-touch-callout: none;
}
.cost-console--embedded { min-height: calc(100vh - 64px); margin: -1rem; }
.cost-console input, .cost-console select { user-select: text; -webkit-user-select: text; }
button, select, input { font: inherit; }
button { border-radius: 0; }
button:focus-visible, select:focus-visible, input:focus-visible, [tabindex]:focus-visible { outline: 2px solid var(--cost-lime); outline-offset: -2px; }
button:active { transform: translateY(1px); }

.cost-toolbar { position: sticky; z-index: 45; top: 0; display: grid; min-height: 94px; grid-template-columns: minmax(310px, 1fr) auto minmax(440px, 1fr); align-items: stretch; border-bottom: 1px solid var(--cost-line-strong); background: rgb(13 17 14 / 96%); backdrop-filter: blur(10px); }
.cost-brand-block { align-self: center; padding: 13px 24px; }
.cost-eyebrow, .cost-section-heading > span, .cost-page-heading > div > span, .cost-quality-strip__intro > span { color: var(--cost-lime); font-family: 'Cascadia Mono', Consolas, monospace; font-size: 10px; letter-spacing: .055em; }
.cost-brand-block h1 { margin: 4px 0 0; font-size: 22px; line-height: 1.12; font-weight: 720; }
.cost-brand-block p, .cost-section-heading p, .cost-page-heading p, .cost-quality-strip__intro p { margin: 5px 0 0; color: var(--cost-muted); font-family: 'Cascadia Mono', Consolas, monospace; font-size: 10px; }
.cost-workspaces { display: flex; align-items: center; gap: 0; border-right: 1px solid var(--cost-line); border-left: 1px solid var(--cost-line); }
.cost-workspaces button { display: flex; min-width: 132px; height: 100%; align-items: center; justify-content: center; gap: 8px; color: #8e998f; background: #121713; border: 0; border-right: 1px solid var(--cost-line); }
.cost-workspaces button.active { color: #10140f; background: var(--cost-lime); font-weight: 700; }
.cost-workspaces kbd { padding: 1px 4px; color: inherit; background: rgb(255 255 255 / 8%); border: 1px solid currentColor; font: 9px 'Cascadia Mono', monospace; opacity: .65; }
.cost-toolbar__actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; padding: 14px 20px; }
.cost-select-label { display: flex; align-items: center; gap: 8px; color: var(--cost-muted); font-size: 11px; }
.cost-select-label select { height: 36px; padding: 0 30px 0 10px; color: var(--cost-text); background: #151a15; border: 1px solid var(--cost-line-strong); }
.cost-tool-button, .cost-icon-button, .cost-primary-button { display: inline-flex; height: 36px; align-items: center; justify-content: center; gap: 7px; padding: 0 12px; color: #a8b2aa; background: #151a15; border: 1px solid var(--cost-line-strong); font-size: 11px; }
.cost-icon-button { width: 36px; padding: 0; }
.cost-tool-button.active { color: var(--cost-lime); border-color: #6e8c37; }
.cost-primary-button { height: 40px; color: #11150f; background: var(--cost-lime); border-color: var(--cost-lime); font-size: 13px; font-weight: 720; }
.cost-primary-button--outline { color: var(--cost-lime); background: transparent; border-color: #75993a; }
.cost-error { display: flex; align-items: center; gap: 10px; padding: 10px 20px; color: #f0b6a6; background: #3a201b; border-bottom: 1px solid #78483b; font-size: 12px; }
.cost-error button { margin-left: auto; padding: 4px 8px; color: inherit; background: transparent; border: 1px solid currentColor; }
.cost-spin { animation: cost-spin .8s linear infinite; }
@keyframes cost-spin { to { transform: rotate(360deg); } }

.cost-workspace { padding: 24px; }
.cost-quality-strip { display: grid; min-height: 116px; grid-template-columns: minmax(380px, 1.7fr) repeat(4, minmax(160px, 1fr)); background: var(--cost-panel); border: 1px solid var(--cost-line-strong); }
.cost-quality-strip__intro { padding: 20px; border-right: 1px solid var(--cost-line); }
.cost-quality-strip__intro h2 { margin: 5px 0 0; font-size: 21px; }
.cost-metric-cell { position: relative; display: flex; min-width: 0; flex-direction: column; justify-content: center; padding: 13px 16px; border-right: 1px solid var(--cost-line); border-bottom: 1px solid var(--cost-line); }
.cost-metric-cell:last-child { border-right: 0; }
.cost-metric-cell::before { position: absolute; top: 0; bottom: 0; left: 0; width: 2px; content: ''; background: transparent; }
.cost-metric-cell.accent-lime::before { background: var(--cost-lime); }.cost-metric-cell.accent-gold::before { background: var(--cost-gold); }.cost-metric-cell.accent-blue::before { background: var(--cost-blue); }
.cost-metric-cell > span { color: #849086; font-size: 11px; }
.cost-metric-cell > strong { overflow: hidden; margin-top: 7px; color: #f0f3ed; font: 700 20px 'Cascadia Mono', Consolas, monospace; text-overflow: ellipsis; white-space: nowrap; }
.cost-metric-cell.accent-lime > strong { color: var(--cost-lime); }.cost-metric-cell.accent-gold > strong { color: var(--cost-gold); }.cost-metric-cell.accent-blue > strong { color: var(--cost-blue); }
.cost-metric-cell > small { margin-top: 5px; overflow: hidden; color: #69746b; font: 9px 'Cascadia Mono', monospace; text-overflow: ellipsis; white-space: nowrap; }

.cost-assets-row { display: grid; min-height: 240px; grid-template-columns: 1.1fr .7fr 2.5fr; margin-top: 18px; background: var(--cost-panel); border: 1px solid var(--cost-line); }
.cost-section-heading { align-self: center; padding: 22px; }.cost-section-heading h2 { margin: 8px 0 0; font-size: 22px; }
.cost-donut-wrap { display: grid; place-items: center; }
.cost-donut { display: grid; width: 140px; height: 140px; place-items: center; border-radius: 50%; }
.cost-donut > div { display: grid; width: 102px; height: 102px; place-items: center; align-content: center; border-radius: 50%; background: var(--cost-panel); }
.cost-donut strong { font: 700 17px 'Cascadia Mono', monospace; }.cost-donut span { margin-top: 5px; color: var(--cost-muted); font-size: 10px; }
.cost-metric-grid { display: grid; grid-template-columns: repeat(4, minmax(135px, 1fr)); border-left: 1px solid var(--cost-line); }
.cost-chart-row { display: grid; grid-template-columns: 1fr 1fr; margin-top: 18px; border: 1px solid var(--cost-line); }
.cost-chart-panel { min-width: 0; padding: 14px 18px 12px; background: var(--cost-panel); border-right: 1px solid var(--cost-line); }.cost-chart-panel:last-child { border-right: 0; }
.cost-panel-heading { display: flex; align-items: center; justify-content: space-between; gap: 20px; }.cost-panel-heading strong { font-size: 12px; }.cost-panel-heading span { color: var(--cost-muted); font: 9px 'Cascadia Mono', monospace; }
.cost-bottom-row { display: grid; grid-template-columns: 2.2fr 1fr; margin-top: 18px; border: 1px solid var(--cost-line); }.cost-score-panel { border-right: 1px solid var(--cost-line); }
.cost-distribution-panel { min-width: 0; padding: 14px 18px; background: var(--cost-panel); }
.cost-distribution-panel__body { display: grid; grid-template-columns: 145px 1fr; align-items: center; gap: 14px; margin-top: 12px; }
.cost-donut--small { width: 130px; height: 130px; }.cost-donut--small > div { width: 92px; height: 92px; }
.cost-ranking-list { margin: 0; padding: 0; list-style: none; }.cost-ranking-list li { display: grid; grid-template-columns: 7px 1fr auto; align-items: center; gap: 9px; padding: 7px 0; border-bottom: 1px solid #273027; }.cost-ranking-list li:last-child { border-bottom: 0; }
.cost-swatch { width: 7px; height: 7px; }.cost-ranking-list strong, .cost-ranking-list small { display: block; max-width: 230px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.cost-ranking-list strong { font-size: 10px; }.cost-ranking-list small { margin-top: 3px; color: var(--cost-muted); font: 8px 'Cascadia Mono', monospace; }.cost-ranking-list b { font: 10px 'Cascadia Mono', monospace; }

.cost-page-heading { display: flex; min-height: 108px; align-items: center; justify-content: space-between; gap: 20px; padding: 12px 6px 22px; }.cost-page-heading h2 { margin: 6px 0 0; font-size: 29px; }.cost-page-heading--compact { min-height: auto; padding: 0; }
.cost-page-heading__actions { display: flex; flex: 0 0 auto; align-items: center; gap: 9px; }
.cost-table-tools { display: flex; align-items: center; gap: 16px; min-height: 54px; padding: 0 0 12px; }
.cost-platform-tabs { display: flex; align-items: stretch; border: 1px solid var(--cost-line-strong); }.cost-platform-tabs button { min-width: 110px; height: 38px; padding: 0 18px; color: #89948b; background: #151a15; border: 0; border-right: 1px solid var(--cost-line); }.cost-platform-tabs button:last-child { border-right: 0; }.cost-platform-tabs button.active { color: #10140f; background: var(--cost-lime); font-weight: 700; }
.cost-refresh-stamp { color: #808b82; font: 11px 'Cascadia Mono', monospace; }.cost-search { display: flex; width: 280px; height: 36px; align-items: center; gap: 8px; margin-left: auto; padding: 0 10px; color: var(--cost-muted); background: #151a15; border: 1px solid var(--cost-line-strong); }.cost-search input { width: 100%; color: var(--cost-text); background: transparent; border: 0; outline: 0; font-size: 11px; }
.cost-data-table-wrap { max-height: calc(100vh - 260px); overflow: auto; border: 1px solid var(--cost-line); background: rgb(13 17 14 / 72%); }
.cost-data-table { width: 100%; min-width: 1740px; border-collapse: collapse; table-layout: fixed; font-size: 11px; }.cost-data-table th { position: sticky; z-index: 3; top: 0; height: 45px; padding: 0 10px; color: #77847a; background: #202720; border-right: 1px solid #2d352e; text-align: left; font-weight: 500; }.cost-data-table td { height: 72px; padding: 8px 10px; vertical-align: middle; border-right: 1px solid #222a23; border-bottom: 1px solid #293129; }.cost-data-table tbody tr { background: rgb(13 17 14 / 76%); }.cost-data-table tbody tr:nth-child(3n) { background: rgb(19 25 18 / 82%); }.cost-data-table tbody tr.status-error { background: rgb(74 39 30 / 54%); box-shadow: inset 3px 0 #d36f4c; }.cost-data-table tbody tr.selected { box-shadow: inset 3px 0 var(--cost-lime); }
.cost-data-table th:nth-child(1) { width: 60px; }.cost-data-table th:nth-child(2) { width: 250px; }.cost-data-table th:nth-child(3) { width: 135px; }.cost-data-table th:nth-child(4) { width: 172px; }.cost-data-table th:nth-child(5) { width: 115px; }.cost-data-table th:nth-child(6) { width: 76px; }.cost-data-table th:nth-child(7) { width: 132px; }.cost-data-table th:nth-child(8), .cost-data-table th:nth-child(9), .cost-data-table th:nth-child(10), .cost-data-table th:nth-child(11) { width: 130px; }.cost-data-table th:nth-child(12) { width: 130px; }.cost-data-table th:nth-child(13) { width: 112px; }.cost-data-table th:nth-child(14) { width: 150px; }.cost-data-table th:nth-child(15) { width: 190px; }.cost-data-table th:nth-child(16) { width: 86px; }
.cost-data-table td strong, .cost-data-table td small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.cost-data-table td strong { color: #e4e9e1; font: 600 11px 'Cascadia Mono', Consolas, monospace; }.cost-data-table td small { margin-top: 5px; color: #69756b; font: 9px 'Cascadia Mono', monospace; }.cost-account-cell strong { font-size: 12px !important; }.cost-lime { color: var(--cost-lime) !important; }
.cost-score { display: inline-block; padding: 6px 8px; color: var(--cost-lime); background: rgb(185 229 90 / 10%); font: 700 12px 'Cascadia Mono', monospace; }.cost-score[data-grade='C'], .cost-score[data-grade='D'] { color: #dc8664; background: rgb(184 87 55 / 12%); }
.cost-status-label { display: grid; grid-template-columns: 7px 1fr; align-items: center; gap: 7px; }.cost-status-label i { width: 7px; height: 7px; border-radius: 50%; background: var(--cost-lime); }.cost-status-label strong { color: var(--cost-lime) !important; }.cost-status-label small { grid-column: 2; margin-top: 0 !important; }.cost-status-label.status-error i { background: #e07758; }.cost-status-label.status-error strong { color: #e07758 !important; }
.cost-group-cell { white-space: normal; }.cost-tag { display: inline-block; margin: 2px 3px 2px 0; padding: 3px 6px; color: #a3ada5; border: 1px solid #39423a; font: 9px 'Cascadia Mono', monospace; }.cost-actions-cell { white-space: nowrap; }.cost-actions-cell button { display: inline-grid; width: 29px; height: 29px; margin-right: 4px; place-items: center; color: var(--cost-lime); background: #151a15; border: 1px solid #3e483f; }.cost-empty-row { height: 180px !important; color: var(--cost-muted); text-align: center; }

.cost-oauth-header { display: grid; grid-template-columns: 360px 1fr; gap: 20px; align-items: stretch; }.cost-oauth-kpis { display: grid; grid-template-columns: repeat(6, minmax(135px, 1fr)); border: 1px solid var(--cost-line); }
.cost-pool-heading-row { display: grid; grid-template-columns: 1fr auto auto; align-items: center; gap: 22px; margin-top: 18px; }.cost-pool-heading-row .cost-section-heading { padding-left: 0; }
.cost-pool-heading-row > .cost-platform-tabs { min-width: 0; max-width: min(58vw, 920px); overflow-x: auto; overscroll-behavior-x: contain; scrollbar-width: thin; }
.cost-pool-heading-row > .cost-platform-tabs button { flex: 0 0 auto; }
.cost-pool-summary { display: grid; min-height: 160px; grid-template-columns: repeat(5, 1fr) 1.35fr; border: 1px solid var(--cost-line); background: var(--cost-panel); }.cost-pool-output { padding: 18px; }.cost-pool-output > span { color: var(--cost-muted); font-size: 11px; }.cost-pool-output > strong { display: block; margin-top: 12px; color: var(--cost-lime); font: 700 17px 'Cascadia Mono', monospace; }.cost-pool-output > div, .cost-pool-progress { height: 6px; margin-top: 16px; background: #30372e; overflow: hidden; }.cost-pool-output i, .cost-pool-progress i { display: block; height: 100%; background: var(--cost-lime); }.cost-pool-output small { display: block; margin-top: 10px; color: var(--cost-muted); font: 9px 'Cascadia Mono', monospace; }
.cost-pool-table-wrap { margin-top: 18px; overflow: auto; border: 1px solid var(--cost-line); }.cost-pool-table { width: 100%; min-width: 1450px; border-collapse: collapse; background: rgb(13 17 14 / 76%); font-size: 11px; }.cost-pool-table th { height: 44px; padding: 0 14px; color: #78847a; background: #202720; border-right: 1px solid #313a32; text-align: left; font-weight: 500; }.cost-pool-table td { min-height: 88px; padding: 14px; border-right: 1px solid #273027; border-bottom: 1px solid #303830; }.cost-pool-table td strong, .cost-pool-table td small { display: block; }.cost-pool-table td strong { font: 600 12px 'Cascadia Mono', monospace; }.cost-pool-table td small { margin-top: 7px; color: #707c72; font: 9px 'Cascadia Mono', monospace; }.cost-status-ring { display: inline-grid; width: 38px; height: 38px; place-items: center; border-radius: 50%; }.cost-status-ring span { width: 22px; height: 22px; border-radius: 50%; background: var(--cost-bg); }.cost-pool-progress { width: 100%; margin-top: 8px; }

@media (max-width: 1450px) { .cost-toolbar { grid-template-columns: 300px auto 1fr; }.cost-workspaces button { min-width: 110px; }.cost-quality-strip { grid-template-columns: minmax(320px, 1.4fr) repeat(4, 1fr); }.cost-assets-row { grid-template-columns: 1fr .75fr 2.2fr; }.cost-metric-grid { grid-template-columns: repeat(2, 1fr); }.cost-oauth-header { grid-template-columns: 280px 1fr; }.cost-oauth-kpis { grid-template-columns: repeat(3, 1fr); } }
@media (max-width: 1180px) { .cost-toolbar { position: relative; grid-template-columns: 1fr; }.cost-workspaces { min-height: 56px; order: 3; border-top: 1px solid var(--cost-line); }.cost-toolbar__actions { position: absolute; top: 10px; right: 0; }.cost-quality-strip { grid-template-columns: repeat(2, 1fr); }.cost-quality-strip__intro { grid-column: 1 / -1; }.cost-assets-row { grid-template-columns: 1fr 1fr; }.cost-metric-grid { grid-column: 1 / -1; border-top: 1px solid var(--cost-line); border-left: 0; }.cost-bottom-row, .cost-chart-row { grid-template-columns: 1fr; }.cost-chart-panel { border-right: 0; border-bottom: 1px solid var(--cost-line); }.cost-oauth-header { grid-template-columns: 1fr; }.cost-pool-summary { grid-template-columns: repeat(3, 1fr); } }
@media (max-width: 760px) { .cost-workspace { padding: 12px; }.cost-brand-block { padding-right: 12px; }.cost-toolbar__actions { position: static; flex-wrap: wrap; justify-content: flex-start; border-top: 1px solid var(--cost-line); }.cost-workspaces { overflow: auto; }.cost-workspaces button { flex: 1; min-width: 120px; }.cost-quality-strip, .cost-assets-row, .cost-metric-grid, .cost-bottom-row, .cost-oauth-kpis, .cost-pool-summary { grid-template-columns: 1fr; }.cost-quality-strip__intro, .cost-metric-grid { grid-column: auto; }.cost-donut-wrap { padding: 20px; }.cost-table-tools { flex-wrap: wrap; }.cost-search { width: 100%; margin-left: 0; }.cost-refresh-stamp { order: 3; width: 100%; }.cost-pool-heading-row { grid-template-columns: 1fr; }.cost-pool-heading-row > .cost-platform-tabs { width: 100%; max-width: 100%; }.cost-page-heading { align-items: flex-start; flex-direction: column; }.cost-page-heading__actions { width: 100%; flex-wrap: wrap; }.cost-distribution-panel__body { grid-template-columns: 1fr; }.cost-donut--small { margin: 0 auto; } }
@media (prefers-reduced-motion: reduce) { *, *::before, *::after { scroll-behavior: auto !important; animation-duration: .01ms !important; animation-iteration-count: 1 !important; transition-duration: .01ms !important; } }

/* Apple-inspired spatial tuning: preserve the dark/lime palette while making
   the console calmer, denser and easier to scan at desktop sizes. */
.cost-console {
  --cost-radius-sm: 9px;
  --cost-radius-md: 13px;
  --cost-radius-lg: 16px;
}

.cost-console main {
  width: min(100%, 2048px);
  margin: 0 auto;
}

.cost-console button,
.cost-console select,
.cost-console input,
.cost-console .cost-data-table-wrap,
.cost-console .cost-pool-table-wrap,
.cost-console .cost-quality-strip,
.cost-console .cost-assets-row,
.cost-console .cost-chart-row,
.cost-console .cost-bottom-row,
.cost-console .cost-oauth-kpis,
.cost-console .cost-pool-summary {
  border-radius: var(--cost-radius-md);
}

.cost-console button {
  transition: transform 160ms ease, background-color 160ms ease, border-color 160ms ease, color 160ms ease, box-shadow 160ms ease;
}

.cost-console button:hover:not(:disabled) {
  border-color: #60715f;
  box-shadow: 0 5px 16px rgb(0 0 0 / 18%);
}

.cost-console button:active:not(:disabled) {
  transform: translateY(1px) scale(.985);
  box-shadow: none;
}

.cost-toolbar {
  min-height: 82px;
  grid-template-columns: minmax(280px, .95fr) auto minmax(360px, 1fr);
  gap: 12px;
  padding: 8px 14px 8px 18px;
  border-bottom-color: rgb(66 77 67 / 82%);
  box-shadow: 0 10px 28px rgb(0 0 0 / 16%);
}

.cost-brand-block {
  padding: 10px 4px;
}

.cost-brand-block h1 {
  font-size: clamp(20px, 1.45vw, 25px);
  letter-spacing: -.025em;
}

.cost-workspaces {
  align-self: center;
  height: 52px;
  gap: 4px;
  padding: 4px;
  border: 1px solid var(--cost-line-strong);
  border-radius: var(--cost-radius-md);
  background: rgb(18 23 19 / 84%);
}

.cost-workspaces button {
  min-width: 116px;
  height: 42px;
  gap: 7px;
  padding: 0 14px;
  border: 0;
  border-radius: var(--cost-radius-sm);
  background: transparent;
}

.cost-workspaces button:last-child { border-right: 0; }
.cost-workspaces button.active { box-shadow: 0 4px 14px rgb(185 229 90 / 14%); }

.cost-workspaces kbd {
  border-radius: 5px;
}

.cost-toolbar__actions {
  gap: 7px;
  padding: 8px 0 8px 8px;
}

.cost-select-label select,
.cost-tool-button,
.cost-icon-button,
.cost-primary-button {
  height: 40px;
  border-radius: var(--cost-radius-sm);
}

.cost-select-label select { padding-right: 28px; }
.cost-icon-button { width: 40px; }
.cost-primary-button { padding-inline: 15px; }

.cost-workspace {
  padding: 20px 24px 32px;
}

.cost-quality-strip {
  min-height: 102px;
  overflow: hidden;
  box-shadow: 0 8px 22px rgb(0 0 0 / 12%);
}

.cost-quality-strip__intro {
  padding: 16px 18px;
}

.cost-quality-strip__intro h2 {
  font-size: clamp(18px, 1.35vw, 22px);
  letter-spacing: -.018em;
}

.cost-metric-cell {
  padding: 12px 14px;
}

.cost-metric-cell > strong {
  margin-top: 5px;
  font-size: clamp(17px, 1.25vw, 20px);
}

.cost-assets-row {
  min-height: 216px;
  margin-top: 16px;
  overflow: hidden;
  box-shadow: 0 8px 22px rgb(0 0 0 / 10%);
}

.cost-section-heading {
  padding: 18px;
}

.cost-section-heading h2 {
  margin-top: 6px;
  font-size: clamp(20px, 1.5vw, 25px);
  letter-spacing: -.02em;
}

.cost-donut {
  width: 126px;
  height: 126px;
}

.cost-donut > div {
  width: 92px;
  height: 92px;
}

.cost-metric-grid { overflow: hidden; }

.cost-chart-row,
.cost-bottom-row {
  margin-top: 16px;
  overflow: hidden;
  box-shadow: 0 8px 22px rgb(0 0 0 / 9%);
}

.cost-chart-panel,
.cost-distribution-panel {
  padding: 15px 16px 13px;
}

.cost-panel-heading strong { font-size: 12px; letter-spacing: -.01em; }

.cost-page-heading {
  min-height: 94px;
  padding: 8px 4px 18px;
}

.cost-page-heading h2 {
  font-size: clamp(24px, 2vw, 31px);
  letter-spacing: -.03em;
}

.cost-platform-tabs {
  gap: 3px;
  padding: 3px;
  border-radius: var(--cost-radius-md);
  background: rgb(18 23 19 / 84%);
}

.cost-platform-tabs button {
  display: inline-flex;
  min-width: 94px;
  height: 36px;
  align-items: center;
  justify-content: center;
  gap: 7px;
  padding-inline: 14px;
  border: 0;
  border-radius: 7px;
  white-space: nowrap;
}

.cost-platform-tabs button:last-child { border-right: 0; }

.cost-platform-tabs button small {
  display: inline-grid;
  min-width: 18px;
  height: 18px;
  place-items: center;
  padding: 0 5px;
  color: #96a198;
  background: #222b23;
  border-radius: 9px;
  font: 9px 'Cascadia Mono', monospace;
}

.cost-platform-tabs button.active small {
  color: #182014;
  background: rgb(16 20 15 / 16%);
}

.cost-table-tools {
  flex-wrap: wrap;
}

.cost-table-tools > .cost-platform-tabs {
  min-width: 0;
  max-width: 100%;
  flex: 1 1 560px;
  overflow-x: auto;
  overflow-y: hidden;
  overscroll-behavior-x: contain;
  scrollbar-width: thin;
}

.cost-table-tools > .cost-platform-tabs button {
  flex: 0 0 auto;
}

.cost-search,
.cost-error button,
.cost-tag,
.cost-score,
.cost-actions-cell button {
  border-radius: var(--cost-radius-sm);
}

.cost-data-table-wrap,
.cost-pool-table-wrap {
  overflow: auto;
  box-shadow: 0 8px 22px rgb(0 0 0 / 9%);
}

.cost-data-table th,
.cost-pool-table th {
  height: 42px;
}

.cost-data-table td { height: 66px; }
.cost-data-table tbody tr { transition: background-color 160ms ease; }
.cost-data-table tbody tr:hover { background: rgb(35 46 34 / 84%); }

.cost-tag {
  padding: 4px 7px;
}

.cost-ranking-bar {
  display: flex;
  min-height: 52px;
  align-items: center;
  gap: 18px;
  margin: -2px 0 10px;
  padding: 8px 12px;
  border: 1px solid #354136;
  border-radius: 12px;
  background: rgb(20 27 21 / 82%);
}

.cost-ranking-bar__title {
  display: flex;
  min-width: 165px;
  align-items: center;
  gap: 9px;
  color: var(--cost-lime);
}

.cost-ranking-bar__title strong,
.cost-ranking-bar__title span {
  display: block;
}

.cost-ranking-bar__title strong { color: #e3ebdf; font-size: 12px; }
.cost-ranking-bar__title span { margin-top: 3px; color: #77847a; font: 9px 'Cascadia Mono', monospace; }

.cost-ranking-select {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #8a978d;
  font-size: 10px;
}

.cost-ranking-select select {
  height: 32px;
  min-width: 132px;
  padding: 0 28px 0 9px;
  color: #dce7dc;
  background: #161d17;
  border: 1px solid #465348;
  border-radius: 8px;
  font-size: 11px;
}

.cost-ranking-bar__summary {
  margin-left: auto;
  color: #8c988e;
  font: 10px 'Cascadia Mono', monospace;
  white-space: nowrap;
}

.cost-account-primary {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 7px;
}

.cost-account-primary strong {
  min-width: 0;
  flex: 1;
}

.cost-rank-badge {
  display: inline-grid;
  flex: 0 0 27px;
  height: 22px;
  place-items: center;
  color: #9aaa9d;
  background: #1b241c;
  border: 1px solid #3e4b40;
  border-radius: 6px;
  font: 700 9px 'Cascadia Mono', monospace;
}

.cost-rank-badge[data-rank='1'] { color: #10150e; background: var(--cost-lime); border-color: var(--cost-lime); }
.cost-rank-badge[data-rank='2'] { color: #132019; background: #8bb9d0; border-color: #8bb9d0; }
.cost-rank-badge[data-rank='3'] { color: #1c170c; background: #d3ad4e; border-color: #d3ad4e; }

@media (max-width: 900px) {
  .cost-ranking-bar {
    align-items: stretch;
    flex-direction: column;
    gap: 9px;
  }

  .cost-ranking-bar__title { min-width: 0; }
  .cost-ranking-select { justify-content: space-between; }
  .cost-ranking-select select { flex: 1; }
  .cost-ranking-bar__summary { margin-left: 0; white-space: normal; }
}

.cost-actions-cell button {
  width: 32px;
  height: 32px;
}

.cost-oauth-header { gap: 16px; }
.cost-pool-heading-row { gap: 16px; margin-top: 16px; }

.cost-pool-summary {
  min-height: 146px;
  overflow: hidden;
  box-shadow: 0 8px 22px rgb(0 0 0 / 9%);
}

.cost-pool-output { padding: 15px 16px; }
.cost-pool-table-wrap { margin-top: 16px; }

@media (max-width: 1180px) {
  .cost-toolbar { padding: 8px 14px; }
  .cost-toolbar__actions { padding-left: 0; }
}

@media (max-width: 760px) {
  .cost-workspace { padding: 14px 12px 24px; }
  .cost-toolbar { gap: 8px; padding: 8px 12px; }
  .cost-workspaces { height: 50px; }
  .cost-workspaces button { min-width: 108px; height: 40px; }
}

@media (prefers-reduced-motion: reduce) {
  .cost-console button,
  .cost-console tbody tr {
    transition: none !important;
  }
}

/* Keep wide operational tables usable on narrow windows without changing the palette. */
.cost-upstreams {
  min-width: 0;
}

.cost-table-scroll-hint {
  display: none;
  align-items: center;
  gap: 6px;
  margin: -4px 0 8px;
  color: #9aa69c;
  font-size: 11px;
}

.cost-data-table-wrap {
  width: 100%;
  max-width: 100%;
  overflow-x: scroll;
  overflow-y: auto;
  scrollbar-gutter: stable;
  overscroll-behavior: contain;
}

.cost-data-table th:first-child,
.cost-data-table td:first-child {
  position: sticky;
  z-index: 4;
  left: 0;
  background: #111711;
  box-shadow: 8px 0 14px rgb(0 0 0 / 16%);
}

.cost-data-table th:first-child {
  background: #202720;
}

.cost-data-table th:last-child,
.cost-data-table td:last-child {
  position: sticky;
  z-index: 4;
  right: 0;
  background: #111711;
  box-shadow: -8px 0 14px rgb(0 0 0 / 16%);
}

.cost-data-table th:last-child {
  background: #202720;
}

@media (max-width: 1800px) {
  .cost-table-scroll-hint {
    display: flex;
  }
}

@media (max-width: 1600px) {
  .cost-toolbar {
    grid-template-columns: minmax(260px, .78fr) minmax(0, 1.22fr);
    grid-template-rows: auto auto;
    align-items: center;
  }

  .cost-brand-block {
    grid-row: 1 / span 2;
    min-width: 0;
  }

  .cost-workspaces {
    grid-column: 2;
    grid-row: 1;
    justify-self: stretch;
    max-width: 100%;
    overflow-x: auto;
    overflow-y: hidden;
  }

  .cost-toolbar__actions {
    grid-column: 2;
    grid-row: 2;
    min-width: 0;
    justify-content: flex-end;
  }
}

@media (max-width: 1180px) {
  .cost-toolbar {
    grid-template-columns: 1fr;
    grid-template-rows: auto auto auto;
    padding: 8px 14px;
  }

  .cost-brand-block {
    grid-column: 1;
    grid-row: 1;
  }

  .cost-workspaces {
    grid-column: 1;
    grid-row: 2;
    justify-self: stretch;
  }

  .cost-toolbar__actions {
    grid-column: 1;
    grid-row: 3;
    justify-content: flex-start;
    padding-left: 0;
  }
}

.cost-provenance-strip {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1px;
  margin-top: 12px;
  overflow: hidden;
  border: 1px solid var(--cost-line);
  border-radius: var(--cost-radius-md);
  background: var(--cost-line);
}

.cost-provenance-strip.has-default-warning {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.cost-provenance-strip > div {
  display: grid;
  min-width: 0;
  grid-template-columns: 20px 1fr;
  align-items: center;
  padding: 10px 12px;
  background: rgb(20 25 21 / 96%);
}

.cost-provenance-strip svg { grid-row: 1 / span 2; color: var(--cost-lime); }
.cost-provenance-strip strong { font-size: 10px; }
.cost-provenance-strip span { overflow: hidden; margin-top: 3px; color: var(--cost-muted); font-size: 9px; text-overflow: ellipsis; white-space: nowrap; }
.cost-provenance-strip .is-warning svg,
.cost-provenance-strip .is-warning strong { color: #e0bd4e; }

.cost-scope-notice {
  display: grid;
  grid-template-columns: auto 1fr;
  align-items: center;
  gap: 5px 12px;
  margin-top: 14px;
  padding: 12px 14px;
  border: 1px solid #3d4c3d;
  border-left: 3px solid var(--cost-lime);
  border-radius: var(--cost-radius-md);
  background: rgb(24 33 24 / 88%);
}

.cost-scope-notice strong { color: var(--cost-lime); font-size: 11px; }
.cost-scope-notice span { color: #c4ccc3; font-size: 11px; }
.cost-scope-notice small { grid-column: 2; color: var(--cost-muted); font: 9px 'Cascadia Mono', monospace; }

.cost-actions-cell button {
  position: relative;
  cursor: pointer;
}

.cost-actions-cell button:disabled { cursor: wait; opacity: .72; }
.cost-actions-cell button.is-success { color: #b9e55a; border-color: #6e873b; background: rgb(185 229 90 / 10%); }
.cost-actions-cell button.is-error { color: #e48a72; border-color: #885243; background: rgb(228 138 114 / 10%); }

.cost-action-tooltip {
  position: absolute;
  z-index: 30;
  top: 50%;
  right: calc(100% + 9px);
  width: max-content;
  max-width: 260px;
  padding: 7px 9px;
  color: #eef3ea;
  background: rgb(27 34 28 / 98%);
  border: 1px solid #536053;
  border-radius: 8px;
  box-shadow: 0 8px 24px rgb(0 0 0 / 32%);
  opacity: 0;
  pointer-events: none;
  transform: translate(4px, -50%);
  transition: opacity 120ms ease, transform 120ms ease;
  font: 10px/1.45 system-ui, sans-serif;
  white-space: normal;
}

.cost-usage-windows {
  display: grid;
  min-width: 145px;
  gap: 5px;
}

.cost-usage-windows > span {
  display: grid;
  grid-template-columns: 22px 1fr 33px;
  align-items: center;
  gap: 5px;
}

.cost-usage-windows b,
.cost-usage-windows strong {
  color: #bdc8bd;
  font: 600 10px 'Cascadia Mono', Consolas, monospace;
}

.cost-usage-windows strong { text-align: right; }
.cost-usage-windows i { display: block; height: 6px; overflow: hidden; background: #3a423b; border-radius: 999px; }
.cost-usage-windows em { display: block; height: 100%; background: var(--cost-lime); border-radius: inherit; }
.cost-usage-windows > span:nth-child(2) em { background: #79d9ad; }
.cost-usage-windows small { grid-column: 2 / -1; margin-top: -2px !important; color: #78857a; font: 8px 'Cascadia Mono', Consolas, monospace; }

.cost-actions-cell button:hover .cost-action-tooltip,
.cost-actions-cell button:focus-visible .cost-action-tooltip {
  opacity: 1;
  transform: translate(0, -50%);
}

@media (max-width: 1180px) {
  .cost-provenance-strip,
  .cost-provenance-strip.has-default-warning { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}

@media (max-width: 760px) {
  .cost-provenance-strip,
  .cost-provenance-strip.has-default-warning { grid-template-columns: 1fr; }
  .cost-scope-notice { grid-template-columns: 1fr; }
  .cost-scope-notice small { grid-column: 1; }
}

/* Wide desktop windows use the full toolbar layout. WebView2 owns Windows DPI
   scaling; this class is selected from the current CSS viewport only. */
.cost-console--physical-wide .cost-toolbar {
  grid-template-columns: minmax(280px, .95fr) auto minmax(360px, 1fr);
  grid-template-rows: none;
  align-items: stretch;
  padding: 8px 14px 8px 18px;
}

.cost-console--physical-wide .cost-brand-block { grid-column: auto; grid-row: auto; }
.cost-console--physical-wide .cost-workspaces { grid-column: auto; grid-row: auto; justify-self: auto; max-width: none; overflow: visible; }
.cost-console--physical-wide .cost-toolbar__actions { grid-column: auto; grid-row: auto; min-width: 0; justify-content: flex-end; padding-left: 8px; }
.cost-selection-bar { display: flex; min-height: 48px; align-items: center; gap: 14px; margin: 10px 0; padding: 0 14px; color: #aeb9ae; background: #141b15; border: 1px solid var(--cost-line); border-radius: var(--cost-radius-md); }
.cost-check-label { display: inline-flex; align-items: center; gap: 8px; font-size: 11px; }
.cost-check-label input, .cost-select-column input { width: 15px; height: 15px; accent-color: var(--cost-lime); }
.cost-selection-count { color: var(--cost-muted); font: 10px 'Cascadia Mono', monospace; }
.cost-selection-bar .cost-primary-button { margin-left: auto; }
.cost-selection-bar .cost-text-button { padding: 4px 6px; color: var(--cost-muted); background: transparent; border: 0; font-size: 11px; }
.cost-select-column { text-align: center !important; }
.cost-data-table tbody tr.row-selected { background: rgb(42 65 31 / 48%); }
.cost-refresh-interval-label span { white-space: nowrap; }
.cost-model-panel {
  min-width: 0;
  margin-top: 16px;
  overflow: hidden;
  background: rgb(13 17 14 / 78%);
  border: 1px solid var(--cost-line);
  border-radius: var(--cost-radius-md);
  box-shadow: 0 8px 22px rgb(0 0 0 / 9%);
}
.cost-model-panel__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 20px;
  padding: 17px 18px 14px;
  border-bottom: 1px solid var(--cost-line);
}
.cost-model-panel__header > div:first-child { min-width: 0; }
.cost-model-panel__header span { color: #7f8b81; font: 9px 'Cascadia Mono', monospace; letter-spacing: .08em; }
.cost-model-panel__header h2 { margin: 5px 0 0; color: #e3ebdf; font-size: 17px; }
.cost-model-panel__header p { margin: 5px 0 0; color: #77847a; font-size: 10px; }
.cost-model-controls { display: flex; flex: 0 1 820px; justify-content: flex-end; gap: 10px; }
.cost-model-controls label { display: grid; min-width: 150px; gap: 5px; }
.cost-model-controls .cost-model-controls__account { flex: 1; min-width: 250px; }
.cost-model-controls select {
  width: 100%;
  height: 34px;
  padding: 0 28px 0 9px;
  color: #dce7dc;
  background: #161d17;
  border: 1px solid #3b463c;
  border-radius: 7px;
  font-size: 11px;
}
.cost-model-summary { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); }
.cost-model-summary .cost-metric-cell { min-width: 0; border-bottom: 1px solid var(--cost-line); }
.cost-pricing-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 16px;
  color: #9eaa9f;
  background: rgb(31 43 31 / 76%);
  border-bottom: 1px solid var(--cost-line);
}
.cost-pricing-status > div { min-width: 0; }
.cost-pricing-status span,
.cost-pricing-status strong,
.cost-pricing-status small { display: block; }
.cost-pricing-status strong { margin-top: 3px; color: #dce8d9; font-size: 11px; }
.cost-pricing-status small { margin-top: 3px; color: #738077; font: 9px 'Cascadia Mono', monospace; overflow-wrap: anywhere; }
.cost-pricing-status button {
  display: inline-flex;
  height: 32px;
  flex: 0 0 auto;
  align-items: center;
  gap: 7px;
  padding: 0 11px;
  color: var(--cost-lime);
  background: #141b15;
  border: 1px solid #54712e;
  border-radius: 8px;
  font-size: 10px;
}
.cost-pricing-status button:disabled { cursor: wait; opacity: .65; }
.cost-pricing-status.is-unavailable { border-left: 2px solid #b8785e; }
.cost-model-note { margin: 0; padding: 10px 16px; color: #89958b; background: rgb(38 47 39 / 42%); border-bottom: 1px solid var(--cost-line); font-size: 10px; line-height: 1.55; }
.cost-route-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 12px 16px; background: #151c16; border-top: 1px solid #3a443b; border-bottom: 1px solid var(--cost-line); }
.cost-route-heading strong, .cost-route-heading span { display: block; }
.cost-route-heading strong { color: #dce5da; font-size: 12px; }
.cost-route-heading span, .cost-route-heading small { margin-top: 4px; color: #778379; font-size: 9px; }
.cost-model-table-wrap { max-width: 100%; overflow: auto; }
.cost-model-table { width: 100%; min-width: 1050px; border-collapse: collapse; font-size: 11px; }
.cost-route-table { min-width: 1500px; }
.cost-model-table th { height: 40px; padding: 0 12px; color: #78847a; background: #202720; border-right: 1px solid #313a32; text-align: left; font-weight: 500; white-space: nowrap; }
.cost-model-table td { padding: 12px; color: #c8d1c8; border-right: 1px solid #273027; border-bottom: 1px solid #303830; font-family: 'Cascadia Mono', Consolas, monospace; }
.cost-model-table td strong, .cost-model-table td small { display: block; }
.cost-model-table td strong { color: #e1e8df; font-size: 11px; }
.cost-model-table td small { margin-top: 5px; color: #707c72; font-size: 9px; }
.cost-model-table .cost-warning-text { color: #e69a7d; }

@media (max-width: 1180px) {
  .cost-model-panel__header { align-items: stretch; flex-direction: column; }
  .cost-model-controls { flex: none; justify-content: flex-start; }
  .cost-model-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}

@media (max-width: 760px) {
  .cost-model-controls { flex-direction: column; }
  .cost-model-controls label,
  .cost-model-controls .cost-model-controls__account { min-width: 0; }
  .cost-pricing-status { align-items: stretch; flex-direction: column; }
  .cost-pricing-status button { align-self: flex-start; }
  .cost-model-summary { grid-template-columns: 1fr; }
}

/* Window-resize contract: these final overrides intentionally win over the
   older density rules above. WebView2 owns DPI scaling; CSS owns reflow. */
@media (max-width: 1600px) and (min-width: 1181px) {
  .cost-toolbar__actions {
    position: static;
    inset: auto;
    width: auto;
    flex-wrap: wrap;
  }
}

@media (max-width: 1180px) {
  .cost-toolbar {
    position: sticky;
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: auto auto auto;
    height: auto;
  }
  .cost-brand-block,
  .cost-workspaces,
  .cost-toolbar__actions {
    position: static;
    inset: auto;
    grid-column: 1;
    width: 100%;
    max-width: 100%;
  }
  .cost-brand-block { grid-row: 1; }
  .cost-workspaces { grid-row: 2; order: initial; }
  .cost-toolbar__actions {
    grid-row: 3;
    flex-wrap: wrap;
    justify-content: flex-start;
    overflow: visible;
    padding: 8px 0 4px;
  }
}
</style>
