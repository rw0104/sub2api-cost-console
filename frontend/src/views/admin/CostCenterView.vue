<template>
  <component :is="desktopMode ? 'div' : AppLayout">
    <div class="cost-console" :class="{ 'cost-console--embedded': !desktopMode }">
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
              <option value="1h">最近 1 小时</option>
              <option value="6h">最近 6 小时</option>
              <option value="24h">最近 24 小时</option>
              <option value="7d">最近 7 天</option>
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
            <MetricCell label="综合评分" :value="qualityScore.toFixed(1)" :note="qualityGrade" accent="lime" />
            <MetricCell label="成功 / 失败" :value="`${formatInteger(successCount)} / ${formatInteger(errorCount)}`" :note="`失败率 ${formatPercent(errorRate)}`" />
            <MetricCell label="切号恢复" :value="`${formatInteger(switchCount)} / ${formatInteger(recoveredCount)}`" note="切换 / 恢复" />
            <MetricCell label="TTFT P95" :value="formatDuration(ttftP95)" :note="opsOverview ? '真实首 token 样本' : '运维监控未开启'" accent="blue" />
          </div>

          <div class="cost-assets-row">
            <div class="cost-section-heading">
              <span>LIVE QUOTA / JOINED COST</span>
              <h2>资产与成本总览</h2>
              <p>{{ lastUpdatedLabel }} · {{ accounts.length }} 个号码 · 成本从账号加入时刻起算</p>
            </div>

            <div class="cost-donut-wrap">
              <div class="cost-donut" :style="{ background: platformDonutBackground }" role="img" :aria-label="platformDonutLabel">
                <div><strong>{{ formatCny(totalAccruedCny, 2) }}</strong><span>累计采购</span></div>
              </div>
            </div>

            <div class="cost-metric-grid">
              <MetricCell label="当前 API 产出速率" :value="formatUsd(apiOutputHourlyUsd, 2)" note="API 美元 / 小时" accent="gold" />
              <MetricCell label="一小时滚动产出" :value="formatUsd(rollingOutputUsd, 2)" note="API 美元 / 小时" accent="gold" />
              <MetricCell label="当前采购成本" :value="`${formatCny(procurementHourlyCny, 4)}/h`" note="号码采购折算" accent="blue" />
              <MetricCell label="一小时综合成本" :value="formatCny(combinedHourlyCny, 4)" note="采购 + API 成本" accent="blue" />
              <MetricCell label="今日 API 账号成本" :value="formatUsd(todayAccountCostUsd, 3)" note="上游账号实际成本" />
              <MetricCell label="最近窗口 API 产出" :value="formatUsd(windowActualOutputUsd, 3)" note="用户实际计费" />
              <MetricCell label="预计月度采购" :value="formatCny(monthlyProcurementForecastCny, 2)" note="当前号码结构 × 730h" />
              <MetricCell label="可用账号" :value="`${activeAccounts.length} / ${accounts.length}`" note="active / total" />
            </div>
          </div>

          <div class="cost-chart-row">
            <ChartPanel title="API 消耗速率" :caption="`${rangeLabel} · 真实账单趋势`">
              <CostLineChart
                :labels="trendLabels"
                :series="[
                  { label: '当前采样', data: trendActualCost, color: '#e0bd4e' },
                  { label: '一小时滚动', data: rollingTrendActualCost, color: '#b9e55a', dashed: true },
                ]"
                value-prefix="$"
              />
            </ChartPanel>
            <ChartPanel title="实时成本" :caption="`${rangeLabel} · API 账号成本 / 采购折算`">
              <CostLineChart
                :labels="trendLabels"
                :series="[
                  { label: 'API 账号成本', data: trendStandardCost, color: '#d8b94d' },
                  { label: '采购基线', data: procurementBaseline, color: '#7eb6d8', dashed: true },
                ]"
                value-prefix="¥"
              />
            </ChartPanel>
          </div>

          <div class="cost-bottom-row">
            <ChartPanel title="综合评分趋势" :caption="`${rangeLabel} · 可用性与失败率派生`" class="cost-score-panel">
              <CostLineChart
                :labels="qualityTrendLabels"
                :series="[
                  { label: '当前采样', data: qualityTrend, color: '#b9e55a' },
                  { label: '滚动评分', data: movingAverage(qualityTrend, 4), color: '#83b7d3', dashed: true },
                ]"
                value-suffix=" / 100"
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
            <button type="button" class="cost-primary-button" @click="goToAccounts"><Plus :size="17" /> 新增上游</button>
          </div>

          <div class="cost-table-tools">
            <div class="cost-platform-tabs" role="tablist" aria-label="平台筛选">
              <button v-for="item in platformTabs" :key="item.key" type="button" :class="{ active: platformFilter === item.key }" @click="platformFilter = item.key">{{ item.label }}</button>
            </div>
            <span class="cost-refresh-stamp">上次刷新：{{ lastUpdatedLabel }} · 最近 {{ formatInteger(totalObservedRequests) }} 次</span>
            <label class="cost-search"><Search :size="15" /><input v-model.trim="searchQuery" type="search" placeholder="查找账号、分组或备注" /></label>
          </div>

          <div class="cost-data-table-wrap" tabindex="0" aria-label="上游资产表，可横向滚动">
            <table class="cost-data-table">
              <thead>
                <tr>
                  <th>账号</th><th>状态</th><th>评分 ↓</th><th>优先级</th><th>加入时间</th><th>采购费率</th><th>已累计成本</th><th>今日账号成本</th><th>API 产出</th><th>请求 / Token</th><th>探测延迟</th><th>失败 / 切号 / 恢复</th><th>分组</th><th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in upstreamRows" :key="row.account.id" :class="[`status-${row.account.status}`, { selected: selectedAccount?.id === row.account.id }]">
                  <td class="cost-account-cell"><strong>{{ row.account.name }}</strong><small>#{{ row.account.id }} · {{ row.account.platform }} / {{ row.plan }}</small></td>
                  <td><StatusLabel :status="row.account.status" :schedulable="row.account.schedulable" /></td>
                  <td><span class="cost-score" :data-grade="scoreGrade(row.score)">{{ row.score.toFixed(1) }}</span><small>{{ scoreGrade(row.score) }} · {{ row.account.scheduler_score?.sticky_weighted_enabled ? 'sticky' : 'base' }}</small></td>
                  <td><strong>{{ row.account.priority }}</strong><small>当前</small></td>
                  <td><strong>{{ formatCompactDate(row.account.created_at) }}</strong><small>{{ row.elapsedHours.toFixed(1) }}h 已计费</small></td>
                  <td><strong class="cost-lime">{{ formatCny(row.hourlyCost, 5) }}/h</strong><small>{{ row.profile.source === 'custom' ? '自定义' : '套餐默认' }}</small></td>
                  <td><strong class="cost-lime">{{ formatCny(row.accrued, 3) }}</strong><small>{{ row.profile.billing_cycle }}</small></td>
                  <td><strong>{{ formatUsd(row.today.cost, 4) }}</strong><small>标准 {{ formatUsd(row.today.standard_cost || 0, 4) }}</small></td>
                  <td><strong class="cost-lime">{{ formatUsd(row.today.user_cost || row.today.standard_cost || 0, 3) }}</strong><small>今日用户计费</small></td>
                  <td><strong>{{ formatInteger(row.today.requests) }}</strong><small>{{ formatTokens(row.today.tokens) }} Token</small></td>
                  <td><strong>{{ formatProbeLatency(row.account.id) }}</strong><small>{{ probeLabel(row.account.id) }}</small></td>
                  <td><strong>{{ row.account.status === 'error' ? '1' : '0' }} / {{ row.switches }} / {{ row.recoveries }}</strong><small>{{ row.account.error_message || '未触发' }}</small></td>
                  <td class="cost-group-cell"><span v-for="group in row.groups" :key="group" class="cost-tag">{{ group }}</span><span v-if="row.groups.length === 0" class="cost-tag">自用</span></td>
                  <td class="cost-actions-cell">
                    <button type="button" title="检测上游" aria-label="检测上游" :disabled="probes[String(row.account.id)]?.loading" @click="runProbe(row.account)"><FlaskConical :size="15" /></button>
                    <button type="button" title="配置成本" aria-label="配置成本" @click="selectedAccount = row.account"><Settings2 :size="15" /></button>
                  </td>
                </tr>
                <tr v-if="upstreamRows.length === 0"><td colspan="14" class="cost-empty-row">没有匹配的上游账号</td></tr>
              </tbody>
            </table>
          </div>
        </section>

        <section v-else class="cost-workspace cost-oauth" aria-labelledby="oauth-title">
          <div class="cost-oauth-header">
            <div class="cost-page-heading cost-page-heading--compact">
              <div><span>OAUTH / LIVE ECONOMICS</span><h2 id="oauth-title">OAuth 实时成本</h2><p>{{ lastUpdatedLabel }}</p></div>
            </div>
            <div class="cost-oauth-kpis">
              <MetricCell label="当前 API 产出速率" :value="formatUsd(apiOutputHourlyUsd, 2)" note="API 美元 / 小时" accent="gold" />
              <MetricCell label="一小时综合成本" :value="`${formatCny(combinedHourlyCny, 2)}/小时`" note="API + 号码采购" accent="gold" />
              <MetricCell label="最近窗口消耗" :value="formatUsd(windowActualOutputUsd, 3)" note="真实 API 账号成本" />
              <MetricCell label="实时剩余预期" :value="formatUsd(estimatedRemainingOutputUsd, 2)" note="按当前产出与成本" />
              <MetricCell label="预计月采购" :value="formatCny(monthlyProcurementForecastCny, 1)" note="当前号码结构" />
              <MetricCell label="下次自动刷新" :value="autoRefresh ? `${countdown} 秒` : '已暂停'" :note="lastUpdatedLabel" accent="lime" />
            </div>
          </div>

          <div class="cost-pool-heading-row">
            <div class="cost-section-heading"><span>POOL / JOINED COST</span><h2>{{ selectedPlatformLabel }} 当前号池实时成本</h2><p>全历史采购累计 · 有效账号 {{ oauthAccounts.length }} · 缺少自定义成本 {{ defaultCostAccountCount }} 个</p></div>
            <div class="cost-platform-tabs" role="tablist"><button v-for="item in platformTabs.filter(item => item.key !== 'all')" :key="item.key" type="button" :class="{ active: poolPlatform === item.key }" @click="poolPlatform = item.key">{{ item.label }}</button></div>
            <button type="button" class="cost-primary-button cost-primary-button--outline" @click="reload"><RefreshCcw :size="16" /> 刷新号池核算</button>
          </div>

          <div class="cost-pool-summary">
            <MetricCell label="OAuth 账号" :value="formatInteger(oauthAccounts.length)" :note="`${oauthActiveCount} 个已产生请求`" />
            <MetricCell label="净采购成本" :value="formatCny(oauthAccruedCny, 2)" :note="`小时成本 ${formatCny(oauthHourlyCny, 4)}`" />
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
            <ChartPanel title="API 产出速度" :caption="rangeLabel">
              <CostLineChart :labels="trendLabels" :series="[{ label: '当前产出', data: trendActualCost, color: '#e0bd4e' }, { label: '一小时滚动', data: rollingTrendActualCost, color: '#b9e55a', dashed: true }]" value-prefix="$" />
            </ChartPanel>
            <ChartPanel title="实时剩余预期" :caption="rangeLabel">
              <CostLineChart :labels="trendLabels" :series="[{ label: '剩余产出预期', data: remainingForecastTrend, color: '#b9e55a' }]" value-prefix="$" />
            </ChartPanel>
          </div>

          <div class="cost-pool-table-wrap">
            <table class="cost-pool-table">
              <thead><tr><th>核算范围</th><th>账号类型</th><th>账号数</th><th>有产出</th><th>状态分布</th><th>采购成本</th><th>平均单价</th><th>当前产出 / 实时预期 / 月预期</th><th>成本计算</th><th>请求</th><th>Token</th></tr></thead>
              <tbody>
                <tr v-for="group in poolGroups" :key="group.plan">
                  <td><strong>当前号池</strong></td><td><strong>{{ group.planLabel }}</strong></td><td>{{ group.count }}</td><td>{{ group.productive }}</td>
                  <td><div class="cost-status-ring" :style="{ background: group.statusRing }"><span></span></div><small>正常 {{ group.normal }} · 限流 {{ group.limited }} · 错误 {{ group.errors }}</small></td>
                  <td><strong>{{ formatCny(group.accruedCny, 2) }}</strong><small>小时 {{ formatCny(group.hourlyCny, 4) }}</small></td>
                  <td><strong>{{ formatCny(group.averageCostCny, 2) }}</strong><small>累计采购成本 / 号</small></td>
                  <td><strong class="cost-lime">{{ formatUsd(group.outputUsd, 2) }} / {{ formatUsd(group.outputForecastUsd, 2) }} / {{ formatUsd(group.monthForecastUsd, 2) }}</strong><div class="cost-pool-progress"><i :style="{ width: `${group.progress}%` }"></i></div><small>当前产出 / 实时预期 / 月预期</small></td>
                  <td><strong class="cost-lime">{{ formatCny(group.hourlyCny, 5) }} / {{ formatUsd(group.apiCostUsd, 5) }}</strong><small>采购小时成本 / API 账号成本</small></td>
                  <td>{{ formatInteger(group.requests) }}</td><td>{{ formatTokens(group.tokens) }}</td>
                </tr>
                <tr v-if="poolGroups.length === 0"><td colspan="11" class="cost-empty-row">当前平台没有 OAuth 账号</td></tr>
              </tbody>
            </table>
          </div>
        </section>
      </main>

      <CostProfileInspector :account="selectedAccount" :saving="saving" :now="now" @close="selectedAccount = null" @save="saveSelectedCostProfile" />
    </div>
  </component>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Activity,
  BarChart3,
  Database,
  FlaskConical,
  Gauge,
  Maximize2,
  Plus,
  RefreshCcw,
  Search,
  Settings2,
  TriangleAlert,
} from '@lucide/vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores'
import type { Account } from '@/types'
import CostLineChart from '@/features/cost-center/components/CostLineChart.vue'
import CostProfileInspector from '@/features/cost-center/components/CostProfileInspector.vue'
import {
  accruedCost,
  COST_ALGORITHM_VERSION,
  convertCurrency,
  elapsedHours,
  formatMoney,
  hourlyRate,
  inferPlan,
  resolveCostProfile,
  type CostProfile,
} from '@/features/cost-center/model'
import { useCostCenterData, type CostCenterRange } from '@/features/cost-center/useCostCenterData'

type WorkspaceKey = 'overview' | 'upstreams' | 'oauth'
type PlatformFilter = 'all' | 'codex' | 'grok'

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
  accounts, stats, trend, opsOverview, opsTrend, probes, loading, saving, error, lastUpdated,
} = data

const workspaceItems = [
  { key: 'overview' as const, label: '资产总览', shortcut: '1', icon: Gauge },
  { key: 'upstreams' as const, label: '上游排行', shortcut: '2', icon: Database },
  { key: 'oauth' as const, label: 'OAuth 号池', shortcut: '3', icon: BarChart3 },
]
const platformTabs = [
  { key: 'all' as const, label: '全部' },
  { key: 'codex' as const, label: 'Codex' },
  { key: 'grok' as const, label: 'Grok' },
]
const distributionColors = ['#b9e55a', '#79b6d9', '#d6aa47', '#d58473', '#8a91bf', '#72a68d', '#b78db7']

const activePanel = ref<WorkspaceKey>(normalizePanel(route.query.panel))
const range = ref<CostCenterRange>('1h')
const platformFilter = ref<PlatformFilter>('all')
const poolPlatform = ref<Exclude<PlatformFilter, 'all'>>('codex')
const searchQuery = ref('')
const selectedAccount = ref<Account | null>(null)
const now = ref(new Date())
const autoRefresh = ref(false)
const countdown = ref(60)
let clockTimer: number | null = null

const desktopMode = computed(() => route.query.desktop === '1' || '__TAURI_INTERNALS__' in (window as any))
const panelTitle = computed(() => activePanel.value === 'overview' ? '上游资产与实时成本' : activePanel.value === 'upstreams' ? '上游运行矩阵' : 'OAuth 实时成本')
const panelDescription = computed(() => activePanel.value === 'overview' ? '评分、调度、采购与 API 产出统一视图' : activePanel.value === 'upstreams' ? '桌面级密集账号巡检与成本操作台' : '号码加入即起算的号池经济模型')
const rangeLabel = computed(() => ({ '1h': '最近 1 小时', '6h': '最近 6 小时', '24h': '最近 24 小时', '7d': '最近 7 天' })[range.value])
const lastUpdatedLabel = computed(() => lastUpdated.value ? lastUpdated.value.toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '等待首次刷新')

const activeAccounts = computed(() => accounts.value.filter((account) => account.status === 'active' && account.schedulable))
const totalObservedRequests = computed(() => opsOverview.value?.request_count_total ?? stats.value?.today_requests ?? 0)
const successCount = computed(() => opsOverview.value?.success_count ?? Math.max(0, totalObservedRequests.value - errorCount.value))
const errorCount = computed(() => opsOverview.value?.error_count_total ?? accounts.value.filter((account) => account.status === 'error').length)
const errorRate = computed(() => opsOverview.value?.error_rate ?? (totalObservedRequests.value > 0 ? errorCount.value / totalObservedRequests.value : 0))
const switchCount = computed(() => opsTrend.value.reduce((sum, point) => sum + Number(point.switch_count || 0), 0))
const recoveredCount = computed(() => accounts.value.filter((account) => account.status === 'active' && account.last_used_at).length)
const ttftP95 = computed(() => Number(opsOverview.value?.ttft?.p95_ms || 0))
const qualityScore = computed(() => {
  const server = Number(opsOverview.value?.health_score)
  if (Number.isFinite(server) && server > 0) return Math.min(100, server)
  const availability = accounts.value.length ? activeAccounts.value.length / accounts.value.length : 0
  return Math.max(0, Math.min(100, availability * 82 + (1 - errorRate.value) * 18))
})
const qualityGrade = computed(() => scoreGrade(qualityScore.value))

const accountLedgers = computed(() => accounts.value.map((account) => {
  const profile = resolveCostProfile(account)
  const today = data.accountStats.value(account)
  return {
    account,
    profile,
    plan: inferPlan(account),
    hourlyCny: convertCurrency(hourlyRate(profile), profile.currency, 'CNY'),
    accruedCny: convertCurrency(accruedCost(profile, now.value), profile.currency, 'CNY'),
    elapsedHours: elapsedHours(profile.started_at, now.value),
    today,
  }
}))
const totalAccruedCny = computed(() => accountLedgers.value.reduce((sum, row) => sum + row.accruedCny, 0))
const procurementHourlyCny = computed(() => accountLedgers.value.reduce((sum, row) => sum + row.hourlyCny, 0))
const monthlyProcurementForecastCny = computed(() => procurementHourlyCny.value * 730)
const todayAccountCostUsd = computed(() => Number(stats.value?.today_account_cost || accountLedgers.value.reduce((sum, row) => sum + Number(row.today.cost || 0), 0)))
const todayOutputUsd = computed(() => Number(stats.value?.today_actual_cost || accountLedgers.value.reduce((sum, row) => sum + Number(row.today.user_cost || 0), 0)))
const dayElapsedHours = computed(() => Math.max(1 / 60, now.value.getHours() + now.value.getMinutes() / 60))
const apiOutputHourlyUsd = computed(() => todayOutputUsd.value / dayElapsedHours.value)
const rollingOutputUsd = computed(() => trend.value.length ? Number(trend.value.at(-1)?.actual_cost || 0) : apiOutputHourlyUsd.value)
const combinedHourlyCny = computed(() => procurementHourlyCny.value + todayAccountCostUsd.value * 7.2 / dayElapsedHours.value)
const windowActualOutputUsd = computed(() => trend.value.reduce((sum, point) => sum + Number(point.actual_cost || 0), 0))
const estimatedRemainingOutputUsd = computed(() => Math.max(0, apiOutputHourlyUsd.value * Math.max(0, 24 - dayElapsedHours.value)))
const todayRequests = computed(() => Number(stats.value?.today_requests || accountLedgers.value.reduce((sum, row) => sum + row.today.requests, 0)))

const trendLabels = computed(() => trend.value.map((point) => formatTrendLabel(point.date)))
const trendActualCost = computed(() => trend.value.map((point) => Number(point.actual_cost || 0)))
const rollingTrendActualCost = computed(() => movingAverage(trendActualCost.value, range.value === '7d' ? 3 : 4))
const trendStandardCost = computed(() => trend.value.map((point) => Number(point.cost || 0) * 7.2))
const procurementBaseline = computed(() => trend.value.map(() => procurementHourlyCny.value))
const remainingForecastTrend = computed(() => {
  const values = trendActualCost.value
  const total = values.reduce((sum, value) => sum + value, 0)
  let consumed = 0
  return values.map((value) => { consumed += value; return Math.max(0, total * 1.15 - consumed) })
})
const qualityTrendLabels = computed(() => opsTrend.value.length ? opsTrend.value.map((point) => formatTrendLabel(point.bucket_start)) : trendLabels.value)
const qualityTrend = computed(() => {
  const source = opsTrend.value.length ? opsTrend.value.map((point) => Number(point.request_count || 0)) : trend.value.map((point) => Number(point.requests || 0))
  const max = Math.max(1, ...source)
  return source.map((value) => Math.max(0, Math.min(100, qualityScore.value - (1 - value / max) * 7)))
})

const platformDistribution = computed(() => {
  const totals = new Map<string, number>()
  for (const row of accountLedgers.value) totals.set(row.account.platform, (totals.get(row.account.platform) || 0) + row.accruedCny)
  return [...totals.entries()].sort((a, b) => b[1] - a[1])
})
const platformDonutBackground = computed(() => donutGradient(platformDistribution.value.map(([label, value], index) => ({ label, value, color: distributionColors[index % distributionColors.length] }))))
const platformDonutLabel = computed(() => platformDistribution.value.map(([name, value]) => `${name} ${formatCny(value, 2)}`).join('，'))

const accountDistribution = computed(() => {
  const rows = accountLedgers.value.filter((row) => row.today.requests > 0).sort((a, b) => b.today.requests - a.today.requests).slice(0, 6)
  const total = Math.max(1, rows.reduce((sum, row) => sum + row.today.requests, 0))
  return rows.map((row, index) => ({
    id: row.account.id, name: row.account.name, platform: row.account.platform, hourlyCost: row.hourlyCny,
    share: row.today.requests / total, value: row.today.requests, color: distributionColors[index % distributionColors.length],
  }))
})
const accountDonutBackground = computed(() => donutGradient(accountDistribution.value))

const upstreamRows = computed(() => {
  const query = searchQuery.value.toLowerCase()
  return accountLedgers.value
    .filter((row) => matchesPlatform(row.account, platformFilter.value))
    .filter((row) => !query || [row.account.name, row.account.notes, row.account.platform, ...(row.account.groups?.map((group) => group.name) || [])].some((value) => String(value || '').toLowerCase().includes(query)))
    .map((row) => ({
      ...row,
      score: normalizedSchedulerScore(row.account),
      hourlyCost: row.hourlyCny,
      accrued: row.accruedCny,
      switches: row.account.rate_limited_at ? 1 : 0,
      recoveries: row.account.status === 'active' && row.account.last_used_at ? 1 : 0,
      groups: row.account.groups?.map((group) => group.name) ?? [],
    }))
    .sort((a, b) => b.score - a.score)
})

const oauthAccounts = computed(() => accountLedgers.value.filter((row) => ['oauth', 'setup-token'].includes(row.account.type) && matchesPlatform(row.account, poolPlatform.value)))
const selectedPlatformLabel = computed(() => poolPlatform.value === 'grok' ? 'Grok' : 'Codex')
const defaultCostAccountCount = computed(() => oauthAccounts.value.filter((row) => row.profile.source === 'default').length)
const oauthAccruedCny = computed(() => oauthAccounts.value.reduce((sum, row) => sum + row.accruedCny, 0))
const oauthHourlyCny = computed(() => oauthAccounts.value.reduce((sum, row) => sum + row.hourlyCny, 0))
const oauthTodayCostUsd = computed(() => oauthAccounts.value.reduce((sum, row) => sum + Number(row.today.cost || 0), 0))
const oauthTodayOutputUsd = computed(() => oauthAccounts.value.reduce((sum, row) => sum + Number(row.today.user_cost || row.today.standard_cost || 0), 0))
const oauthRequests = computed(() => oauthAccounts.value.reduce((sum, row) => sum + row.today.requests, 0))
const oauthTokens = computed(() => oauthAccounts.value.reduce((sum, row) => sum + row.today.tokens, 0))
const oauthActiveCount = computed(() => oauthAccounts.value.filter((row) => row.today.requests > 0).length)
const oauthNormalCount = computed(() => oauthAccounts.value.filter((row) => row.account.status === 'active' && !row.account.rate_limited_at).length)
const oauthLimitedCount = computed(() => oauthAccounts.value.filter((row) => Boolean(row.account.rate_limited_at)).length)
const oauthErrorCount = computed(() => oauthAccounts.value.filter((row) => row.account.status === 'error').length)
const poolOutputProgress = computed(() => oauthTodayOutputUsd.value > 0 ? oauthTodayOutputUsd.value / Math.max(oauthTodayOutputUsd.value, oauthTodayCostUsd.value + oauthHourlyCny.value / 7.2 * dayElapsedHours.value) * 100 : 0)
const poolGroups = computed(() => {
  const planOrder = ['free', 'k12', 'plus', 'pro', 'team', 'business', 'unknown']
  return planOrder.map((plan) => {
    const rows = oauthAccounts.value.filter((row) => row.plan === plan)
    if (!rows.length) return null
    const accruedCny = rows.reduce((sum, row) => sum + row.accruedCny, 0)
    const hourlyCny = rows.reduce((sum, row) => sum + row.hourlyCny, 0)
    const outputUsd = rows.reduce((sum, row) => sum + Number(row.today.user_cost || row.today.standard_cost || 0), 0)
    const apiCostUsd = rows.reduce((sum, row) => sum + Number(row.today.cost || 0), 0)
    const requests = rows.reduce((sum, row) => sum + row.today.requests, 0)
    const tokens = rows.reduce((sum, row) => sum + row.today.tokens, 0)
    const normal = rows.filter((row) => row.account.status === 'active' && !row.account.rate_limited_at).length
    const limited = rows.filter((row) => Boolean(row.account.rate_limited_at)).length
    const errors = rows.filter((row) => row.account.status === 'error').length
    const outputForecastUsd = outputUsd / dayElapsedHours.value * 24
    const monthForecastUsd = outputUsd / dayElapsedHours.value * 730
    return {
      plan, planLabel: plan === 'unknown' ? 'Other' : plan === 'k12' ? 'K12' : plan[0].toUpperCase() + plan.slice(1),
      count: rows.length, productive: rows.filter((row) => row.today.requests > 0).length,
      normal, limited, errors, accruedCny, hourlyCny, averageCostCny: accruedCny / rows.length,
      outputUsd, outputForecastUsd, monthForecastUsd, apiCostUsd, requests, tokens,
      progress: Math.min(100, outputForecastUsd > 0 ? outputUsd / outputForecastUsd * 100 : 0),
      statusRing: statusRingGradient(normal, limited, errors),
    }
  }).filter(Boolean) as Array<any>
})

watch(activePanel, (panel) => router.replace({ query: { ...route.query, panel } }))
watch(range, () => reload())

function normalizePanel(value: unknown): WorkspaceKey {
  return value === 'upstreams' || value === 'oauth' ? value : 'overview'
}

function matchesPlatform(account: Account, filter: PlatformFilter): boolean {
  if (filter === 'all') return true
  return filter === 'codex' ? account.platform === 'openai' : account.platform === 'grok'
}

function normalizedSchedulerScore(account: Account): number {
  const raw = Number(account.scheduler_score?.base_score)
  if (!Number.isFinite(raw)) return account.status === 'active' ? qualityScore.value : Math.max(0, qualityScore.value - 18)
  if (raw <= 1) return raw * 100
  return Math.max(0, Math.min(100, raw))
}

function scoreGrade(score: number): string { return score >= 82 ? 'A' : score >= 70 ? 'B' : score >= 58 ? 'C' : 'D' }
function movingAverage(values: number[], windowSize: number): number[] { return values.map((_, index) => { const slice = values.slice(Math.max(0, index - windowSize + 1), index + 1); return slice.reduce((sum, value) => sum + value, 0) / Math.max(1, slice.length) }) }
function formatInteger(value: number): string { return Math.round(Number(value) || 0).toLocaleString() }
function formatPercent(value: number): string { return `${((Number(value) || 0) * 100).toFixed(1)}%` }
function formatCny(value: number, digits = 2): string { return formatMoney(Number(value) || 0, 'CNY', digits) }
function formatUsd(value: number, digits = 2): string { return formatMoney(Number(value) || 0, 'USD', digits) }
function formatDuration(value: number): string { return value > 0 ? value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${Math.round(value)} ms` : '—' }
function formatTokens(value: number): string { const number = Number(value) || 0; return number >= 1e9 ? `${(number / 1e9).toFixed(2)}B` : number >= 1e6 ? `${(number / 1e6).toFixed(2)}M` : number >= 1e3 ? `${(number / 1e3).toFixed(1)}K` : formatInteger(number) }
function formatCompactDate(value: string): string { const date = new Date(value); return Number.isFinite(date.getTime()) ? date.toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—' }
function formatTrendLabel(value: string): string { const date = new Date(value); return Number.isFinite(date.getTime()) ? date.toLocaleString([], range.value === '7d' ? { month: '2-digit', day: '2-digit' } : { hour: '2-digit', minute: '2-digit' }) : value }
function donutGradient(items: Array<{ value: number; color: string }>): string { const total = items.reduce((sum, item) => sum + Math.max(0, item.value), 0); if (!total) return 'conic-gradient(#303830 0 100%)'; let cursor = 0; const stops = items.map((item) => { const start = cursor; cursor += Math.max(0, item.value) / total * 100; return `${item.color} ${start}% ${cursor}%` }); return `conic-gradient(${stops.join(', ')})` }
function statusRingGradient(normal: number, limited: number, errors: number): string { const total = Math.max(1, normal + limited + errors); const normalEnd = normal / total * 100; const limitedEnd = normalEnd + limited / total * 100; return `conic-gradient(#b9e55a 0 ${normalEnd}%, #9c8a54 ${normalEnd}% ${limitedEnd}%, #995c50 ${limitedEnd}% 100%)` }
function formatProbeLatency(id: number): string { const state = probes.value[String(id)]; if (!state) return '—'; if (state.loading) return '检测中'; return state.latency_ms ? `${formatInteger(state.latency_ms)} ms` : state.success ? '可用' : '失败' }
function probeLabel(id: number): string { const state = probes.value[String(id)]; return state?.message || '点击检测' }

async function reload() { await data.reload(range.value) }
function toggleAutoRefresh() { autoRefresh.value = !autoRefresh.value; countdown.value = 60 }
function goToAccounts() { router.push('/admin/accounts') }
async function runProbe(account: Account) { try { await data.probeAccount(account); appStore.showSuccess(`已完成 ${account.name} 上游检测`) } catch { appStore.showError(`检测 ${account.name} 失败`) } }
async function saveSelectedCostProfile(profile: CostProfile) { if (!selectedAccount.value) return; try { const updated = await data.saveCostProfile(selectedAccount.value, profile); selectedAccount.value = updated; appStore.showSuccess('成本档案已保存，累计成本已重新计算') } catch (saveError: any) { appStore.showError(saveError?.message || '成本档案保存失败') } }
async function toggleFullscreen() { if ('__TAURI_INTERNALS__' in (window as any)) { const { getCurrentWindow } = await import('@tauri-apps/api/window'); const current = getCurrentWindow(); await current.setFullscreen(!(await current.isFullscreen())) } else if (!document.fullscreenElement) await document.documentElement.requestFullscreen(); else await document.exitFullscreen() }

function handleKeydown(event: KeyboardEvent) {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'r') { event.preventDefault(); reload(); return }
  if (event.key === 'F11') { event.preventDefault(); toggleFullscreen(); return }
  if (event.key === 'Escape' && selectedAccount.value) { selectedAccount.value = null; return }
  if ((event.ctrlKey || event.metaKey) && ['1', '2', '3'].includes(event.key)) { event.preventDefault(); activePanel.value = workspaceItems[Number(event.key) - 1].key }
}

onMounted(async () => {
  window.addEventListener('keydown', handleKeydown)
  clockTimer = window.setInterval(() => {
    now.value = new Date()
    if (!autoRefresh.value) return
    countdown.value -= 1
    if (countdown.value <= 0) { countdown.value = 60; reload() }
  }, 1000)
  await reload()
})
onBeforeUnmount(() => { window.removeEventListener('keydown', handleKeydown); if (clockTimer !== null) window.clearInterval(clockTimer) })
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
.cost-table-tools { display: flex; align-items: center; gap: 16px; min-height: 54px; padding: 0 0 12px; }
.cost-platform-tabs { display: flex; align-items: stretch; border: 1px solid var(--cost-line-strong); }.cost-platform-tabs button { min-width: 110px; height: 38px; padding: 0 18px; color: #89948b; background: #151a15; border: 0; border-right: 1px solid var(--cost-line); }.cost-platform-tabs button:last-child { border-right: 0; }.cost-platform-tabs button.active { color: #10140f; background: var(--cost-lime); font-weight: 700; }
.cost-refresh-stamp { color: #808b82; font: 11px 'Cascadia Mono', monospace; }.cost-search { display: flex; width: 280px; height: 36px; align-items: center; gap: 8px; margin-left: auto; padding: 0 10px; color: var(--cost-muted); background: #151a15; border: 1px solid var(--cost-line-strong); }.cost-search input { width: 100%; color: var(--cost-text); background: transparent; border: 0; outline: 0; font-size: 11px; }
.cost-data-table-wrap { max-height: calc(100vh - 260px); overflow: auto; border: 1px solid var(--cost-line); background: rgb(13 17 14 / 72%); }
.cost-data-table { width: 100%; min-width: 1740px; border-collapse: collapse; table-layout: fixed; font-size: 11px; }.cost-data-table th { position: sticky; z-index: 3; top: 0; height: 45px; padding: 0 10px; color: #77847a; background: #202720; border-right: 1px solid #2d352e; text-align: left; font-weight: 500; }.cost-data-table td { height: 72px; padding: 8px 10px; vertical-align: middle; border-right: 1px solid #222a23; border-bottom: 1px solid #293129; }.cost-data-table tbody tr { background: rgb(13 17 14 / 76%); }.cost-data-table tbody tr:nth-child(3n) { background: rgb(19 25 18 / 82%); }.cost-data-table tbody tr.status-error { background: rgb(74 39 30 / 54%); box-shadow: inset 3px 0 #d36f4c; }.cost-data-table tbody tr.selected { box-shadow: inset 3px 0 var(--cost-lime); }
.cost-data-table th:nth-child(1) { width: 250px; }.cost-data-table th:nth-child(2) { width: 135px; }.cost-data-table th:nth-child(3) { width: 115px; }.cost-data-table th:nth-child(4) { width: 76px; }.cost-data-table th:nth-child(5) { width: 132px; }.cost-data-table th:nth-child(6), .cost-data-table th:nth-child(7), .cost-data-table th:nth-child(8), .cost-data-table th:nth-child(9) { width: 130px; }.cost-data-table th:nth-child(10) { width: 130px; }.cost-data-table th:nth-child(11) { width: 112px; }.cost-data-table th:nth-child(12) { width: 150px; }.cost-data-table th:nth-child(13) { width: 190px; }.cost-data-table th:nth-child(14) { width: 86px; }
.cost-data-table td strong, .cost-data-table td small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.cost-data-table td strong { color: #e4e9e1; font: 600 11px 'Cascadia Mono', Consolas, monospace; }.cost-data-table td small { margin-top: 5px; color: #69756b; font: 9px 'Cascadia Mono', monospace; }.cost-account-cell strong { font-size: 12px !important; }.cost-lime { color: var(--cost-lime) !important; }
.cost-score { display: inline-block; padding: 6px 8px; color: var(--cost-lime); background: rgb(185 229 90 / 10%); font: 700 12px 'Cascadia Mono', monospace; }.cost-score[data-grade='C'], .cost-score[data-grade='D'] { color: #dc8664; background: rgb(184 87 55 / 12%); }
.cost-status-label { display: grid; grid-template-columns: 7px 1fr; align-items: center; gap: 7px; }.cost-status-label i { width: 7px; height: 7px; border-radius: 50%; background: var(--cost-lime); }.cost-status-label strong { color: var(--cost-lime) !important; }.cost-status-label small { grid-column: 2; margin-top: 0 !important; }.cost-status-label.status-error i { background: #e07758; }.cost-status-label.status-error strong { color: #e07758 !important; }
.cost-group-cell { white-space: normal; }.cost-tag { display: inline-block; margin: 2px 3px 2px 0; padding: 3px 6px; color: #a3ada5; border: 1px solid #39423a; font: 9px 'Cascadia Mono', monospace; }.cost-actions-cell { white-space: nowrap; }.cost-actions-cell button { display: inline-grid; width: 29px; height: 29px; margin-right: 4px; place-items: center; color: var(--cost-lime); background: #151a15; border: 1px solid #3e483f; }.cost-empty-row { height: 180px !important; color: var(--cost-muted); text-align: center; }

.cost-oauth-header { display: grid; grid-template-columns: 360px 1fr; gap: 20px; align-items: stretch; }.cost-oauth-kpis { display: grid; grid-template-columns: repeat(6, minmax(135px, 1fr)); border: 1px solid var(--cost-line); }
.cost-pool-heading-row { display: grid; grid-template-columns: 1fr auto auto; align-items: center; gap: 22px; margin-top: 18px; }.cost-pool-heading-row .cost-section-heading { padding-left: 0; }
.cost-pool-summary { display: grid; min-height: 160px; grid-template-columns: repeat(5, 1fr) 1.35fr; border: 1px solid var(--cost-line); background: var(--cost-panel); }.cost-pool-output { padding: 18px; }.cost-pool-output > span { color: var(--cost-muted); font-size: 11px; }.cost-pool-output > strong { display: block; margin-top: 12px; color: var(--cost-lime); font: 700 17px 'Cascadia Mono', monospace; }.cost-pool-output > div, .cost-pool-progress { height: 6px; margin-top: 16px; background: #30372e; overflow: hidden; }.cost-pool-output i, .cost-pool-progress i { display: block; height: 100%; background: var(--cost-lime); }.cost-pool-output small { display: block; margin-top: 10px; color: var(--cost-muted); font: 9px 'Cascadia Mono', monospace; }
.cost-pool-table-wrap { margin-top: 18px; overflow: auto; border: 1px solid var(--cost-line); }.cost-pool-table { width: 100%; min-width: 1450px; border-collapse: collapse; background: rgb(13 17 14 / 76%); font-size: 11px; }.cost-pool-table th { height: 44px; padding: 0 14px; color: #78847a; background: #202720; border-right: 1px solid #313a32; text-align: left; font-weight: 500; }.cost-pool-table td { min-height: 88px; padding: 14px; border-right: 1px solid #273027; border-bottom: 1px solid #303830; }.cost-pool-table td strong, .cost-pool-table td small { display: block; }.cost-pool-table td strong { font: 600 12px 'Cascadia Mono', monospace; }.cost-pool-table td small { margin-top: 7px; color: #707c72; font: 9px 'Cascadia Mono', monospace; }.cost-status-ring { display: inline-grid; width: 38px; height: 38px; place-items: center; border-radius: 50%; }.cost-status-ring span { width: 22px; height: 22px; border-radius: 50%; background: var(--cost-bg); }.cost-pool-progress { width: 100%; margin-top: 8px; }

@media (max-width: 1450px) { .cost-toolbar { grid-template-columns: 300px auto 1fr; }.cost-workspaces button { min-width: 110px; }.cost-quality-strip { grid-template-columns: minmax(320px, 1.4fr) repeat(4, 1fr); }.cost-assets-row { grid-template-columns: 1fr .75fr 2.2fr; }.cost-metric-grid { grid-template-columns: repeat(2, 1fr); }.cost-oauth-header { grid-template-columns: 280px 1fr; }.cost-oauth-kpis { grid-template-columns: repeat(3, 1fr); } }
@media (max-width: 1180px) { .cost-toolbar { position: relative; grid-template-columns: 1fr; }.cost-workspaces { min-height: 56px; order: 3; border-top: 1px solid var(--cost-line); }.cost-toolbar__actions { position: absolute; top: 10px; right: 0; }.cost-quality-strip { grid-template-columns: repeat(2, 1fr); }.cost-quality-strip__intro { grid-column: 1 / -1; }.cost-assets-row { grid-template-columns: 1fr 1fr; }.cost-metric-grid { grid-column: 1 / -1; border-top: 1px solid var(--cost-line); border-left: 0; }.cost-bottom-row, .cost-chart-row { grid-template-columns: 1fr; }.cost-chart-panel { border-right: 0; border-bottom: 1px solid var(--cost-line); }.cost-oauth-header { grid-template-columns: 1fr; }.cost-pool-summary { grid-template-columns: repeat(3, 1fr); } }
@media (max-width: 760px) { .cost-workspace { padding: 12px; }.cost-brand-block { padding-right: 12px; }.cost-toolbar__actions { position: static; flex-wrap: wrap; justify-content: flex-start; border-top: 1px solid var(--cost-line); }.cost-workspaces { overflow: auto; }.cost-workspaces button { flex: 1; min-width: 120px; }.cost-quality-strip, .cost-assets-row, .cost-metric-grid, .cost-bottom-row, .cost-oauth-kpis, .cost-pool-summary { grid-template-columns: 1fr; }.cost-quality-strip__intro, .cost-metric-grid { grid-column: auto; }.cost-donut-wrap { padding: 20px; }.cost-table-tools { flex-wrap: wrap; }.cost-search { width: 100%; margin-left: 0; }.cost-refresh-stamp { order: 3; width: 100%; }.cost-pool-heading-row { grid-template-columns: 1fr; }.cost-page-heading { align-items: flex-start; flex-direction: column; }.cost-distribution-panel__body { grid-template-columns: 1fr; }.cost-donut--small { margin: 0 auto; } }
@media (prefers-reduced-motion: reduce) { *, *::before, *::after { scroll-behavior: auto !important; animation-duration: .01ms !important; animation-iteration-count: 1 !important; transition-duration: .01ms !important; } }
</style>
