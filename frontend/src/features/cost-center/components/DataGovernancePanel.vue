<template>
  <section class="governance" aria-labelledby="governance-title">
    <header class="governance__header">
      <div>
        <span>DATA GOVERNANCE / TRUTHFUL HISTORY</span>
        <h2 id="governance-title">数据来源与历史控制</h2>
        <p>明确区分自然日、滚动窗口、生命周期累计与派生采样；接口失败不会显示成 0。当前页自动刷新是 5/10/15/30 秒间隔，没有 20 小时或每天 20:00 的定时刷新规则。</p>
      </div>
      <button type="button" data-aui-component="button" data-aui-pressable :disabled="loading" @click="$emit('refresh')">
        <RefreshCcw :size="15" :class="{ spin: loading }" />
        {{ loading ? '刷新中' : '刷新全部来源' }}
      </button>
    </header>

    <div class="governance__summary" role="status">
      <div><strong>{{ healthyCount }}</strong><span>可用来源</span></div>
      <div><strong>{{ warningCount }}</strong><span>部分/估算/旧数据</span></div>
      <div><strong>{{ unavailableCount }}</strong><span>不可用来源</span></div>
      <div><strong>{{ lastUpdated || '尚未完成刷新' }}</strong><span>最近整体验证</span></div>
    </div>

    <div class="governance__grid">
      <article v-for="state in orderedStates" :key="state.key" class="source-card" :class="`is-${state.status}`">
        <div>
          <component :is="sourceIcon(state.status)" :size="15" />
          <strong>{{ state.label }}</strong>
          <em>{{ dataAvailabilityLabel(state.status) }}</em>
        </div>
        <p>{{ state.reason || stateDescription(state.status) }}</p>
        <small>{{ state.updatedAt ? formatTime(state.updatedAt) : '尚无成功时间' }}</small>
      </article>
    </div>

    <div class="governance__policies">
      <article>
        <CalendarDays :size="18" />
        <div><strong>“当天”是北京时间自然日</strong><p>从 Asia/Shanghai 00:00 起算；它不是滚动 24 小时。跨北京时间 00:00 后当天统计自然归零，采购和损失账本不会归零。</p></div>
      </article>
      <article>
        <History :size="18" />
        <div><strong>经济样本自动保留 90 天</strong><p>样本只用于相邻累计值差分和预测；账号成员变化或累计值倒退会重置预测基线，不修改 usage_logs。</p></div>
      </article>
      <article>
        <ShieldCheck :size="18" />
        <div><strong>封禁损失账本不可静默清零</strong><p>终局损失、退款与冲销是独立事实账本。删除账号仍保留历史，不在这个面板提供误删入口。</p></div>
      </article>
    </div>

    <footer class="governance__actions">
      <button type="button" data-aui-component="button" data-aui-pressable @click="router.push('/admin/usage')">
        <Database :size="15" /> 打开 usage_logs 历史与清理任务
      </button>
      <div>
        <strong>当前经济采样</strong>
        <span v-if="economics">{{ economics.data_quality.sample_count }} 个样本 · {{ economics.projection.valid_intervals }} 个稳定区间 · 预测 {{ economics.projection.confidence }}</span>
        <span v-else>无数据；请查看上方“经济采样与预测”来源原因</span>
      </div>
    </footer>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { CalendarDays, CheckCircle2, CircleAlert, Clock3, Database, History, RefreshCcw, ShieldCheck, TriangleAlert } from '@lucide/vue'
import type { AccountEconomicsSnapshot } from '@/api/admin/accounts'
import { COST_CENTER_SOURCE_KEYS, dataAvailabilityLabel, type DataAvailability, type DataSourceState } from '../dataState'

const props = defineProps<{
  states: Record<string, DataSourceState>
  economics?: AccountEconomicsSnapshot | null
  lastUpdated?: string
  loading?: boolean
}>()

defineEmits<{ refresh: [] }>()

const router = useRouter()
const orderedStates = computed(() => COST_CENTER_SOURCE_KEYS.map((key) => props.states[key]).filter(Boolean))
const healthyCount = computed(() => orderedStates.value.filter((state) => state.status === 'measured' || state.status === 'empty').length)
const warningCount = computed(() => orderedStates.value.filter((state) => ['estimated', 'partial', 'stale', 'loading'].includes(state.status)).length)
const unavailableCount = computed(() => orderedStates.value.filter((state) => state.status === 'unavailable').length)

function sourceIcon(status: DataAvailability) {
  if (status === 'measured' || status === 'empty') return CheckCircle2
  if (status === 'unavailable') return CircleAlert
  if (status === 'loading' || status === 'stale') return Clock3
  return TriangleAlert
}

function stateDescription(status: DataAvailability): string {
  if (status === 'empty') return '来源读取成功，当前范围内确实没有记录。'
  if (status === 'unavailable') return '来源读取失败；所有依赖值显示为“无数据”。'
  if (status === 'estimated') return '使用已明确标注的回退或估算值。'
  if (status === 'partial') return '只有部分来源或部分范围可用。'
  if (status === 'stale') return '保留上次成功值并标记为旧数据。'
  return status === 'loading' ? '正在读取，暂不显示数值。' : '来源读取成功。'
}

function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? `更新于 ${date.toLocaleString()}` : '更新时间未知'
}
</script>

<style scoped>
.governance { padding: 26px; color: var(--cost-text, #edf1ea); }
.governance__header { display: flex; align-items: flex-end; justify-content: space-between; gap: 20px; }
.governance__header span { color: var(--cost-lime, #b9e55a); font: 10px 'Cascadia Mono', monospace; letter-spacing: .08em; }
.governance__header h2 { margin: 7px 0 5px; font-size: clamp(24px, 2.2vw, 35px); }
.governance__header p, .source-card p, .governance__policies p { margin: 0; color: #89948b; font-size: 12px; line-height: 1.55; }
.governance button { display: inline-flex; min-height: 38px; align-items: center; gap: 7px; border: 1px solid rgb(185 229 90 / 45%); border-radius: 10px; padding: 0 13px; color: #ddecce; background: rgb(185 229 90 / 8%); cursor: pointer; }
.governance button:disabled { cursor: progress; opacity: .55; }
.governance__summary { display: grid; grid-template-columns: repeat(3, minmax(130px, .7fr)) minmax(220px, 1.4fr); margin-top: 22px; border: 1px solid #303830; border-radius: 14px; overflow: hidden; background: #111612; }
.governance__summary div { min-height: 92px; padding: 17px; border-right: 1px solid #303830; }
.governance__summary div:last-child { border-right: 0; }
.governance__summary strong, .governance__summary span { display: block; }
.governance__summary strong { font: 650 21px 'Cascadia Mono', monospace; }
.governance__summary span { margin-top: 7px; color: #7f8a81; font-size: 11px; }
.governance__grid { display: grid; grid-template-columns: repeat(3, minmax(240px, 1fr)); gap: 10px; margin-top: 14px; }
.source-card { min-height: 118px; border: 1px solid #303830; border-left-width: 3px; border-radius: 11px; padding: 14px; background: #121713; }
.source-card > div { display: flex; align-items: center; gap: 7px; }
.source-card strong { font-size: 13px; }
.source-card em { margin-left: auto; color: #9ca69e; font-size: 10px; font-style: normal; }
.source-card p { margin-top: 11px; }
.source-card small { display: block; margin-top: 8px; color: #647068; font: 9px 'Cascadia Mono', monospace; }
.source-card.is-measured, .source-card.is-empty { border-left-color: #72c88a; }
.source-card.is-estimated, .source-card.is-partial { border-left-color: #d6aa47; }
.source-card.is-unavailable { border-left-color: #d58473; }
.source-card.is-stale, .source-card.is-loading { border-left-color: #7eb6d8; }
.governance__policies { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; margin-top: 14px; }
.governance__policies article { display: flex; gap: 11px; border: 1px solid #303830; border-radius: 11px; padding: 15px; background: #0f1410; }
.governance__policies svg { flex: 0 0 auto; color: var(--cost-lime, #b9e55a); }
.governance__policies strong { display: block; margin-bottom: 7px; font-size: 13px; }
.governance__actions { display: flex; align-items: center; gap: 18px; margin-top: 14px; border-top: 1px solid #303830; padding-top: 16px; }
.governance__actions div { display: grid; gap: 4px; }
.governance__actions span { color: #818c83; font-size: 11px; }
.spin { animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
@media (max-width: 1100px) { .governance__grid, .governance__policies { grid-template-columns: repeat(2, 1fr); }.governance__summary { grid-template-columns: repeat(2, 1fr); } }
@media (max-width: 700px) { .governance__header, .governance__actions { align-items: stretch; flex-direction: column; }.governance__grid, .governance__policies, .governance__summary { grid-template-columns: 1fr; } }
</style>
