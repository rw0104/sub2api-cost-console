<template>
  <aside v-if="account" class="cost-inspector" role="dialog" aria-modal="false" aria-labelledby="cost-profile-title">
    <header class="cost-inspector__header">
      <div>
        <span class="cost-kicker">ACCOUNT COST PROFILE</span>
        <h2 id="cost-profile-title">{{ inspectorTitle }}</h2>
        <p>{{ accountCount && accountCount > 1 ? `${accountCount} 个账号 · 以 ${account.name} 为表单基准` : account.name }}</p>
      </div>
      <button type="button" class="cost-icon-button" aria-label="关闭成本配置" title="关闭 (Esc)" @click="$emit('close')">
        <X :size="18" />
      </button>
    </header>

    <div class="cost-inspector__body">
      <section v-if="isMetered" class="cost-metered-card">
        <span class="cost-kicker">AUTOMATIC TOKEN BILLING</span>
        <h3>{{ upstreamOrigin }}</h3>
        <strong>无需填写采购金额</strong>
        <p>{{ meteredExplanation }}</p>
        <div class="cost-metered-flow" aria-label="按量成本计算步骤">
          <span>usage_logs</span><i>→</i><span>实际上游模型</span><i>→</i><span>{{ meteredPriceSource }}</span><i>→</i><span>账号倍率</span>
        </div>
      </section>

      <section v-if="isMetered" class="cost-inspector__summary cost-inspector__summary--metered">
        <div>
          <span>按量计费口径</span>
          <strong>Token</strong>
          <small>输入 Token + 缓存命中 + 输出 Token</small>
        </div>
        <div>
          <span>账号成本倍率</span>
          <strong>×{{ accountRateMultiplier.toFixed(4) }}</strong>
          <small>中转折扣或实际采购倍率</small>
        </div>
        <div>
          <span>固定附加成本</span>
          <strong>{{ resolved.source === 'custom' ? formatMoney(currentHourly, form.currency, 5) + '/h' : '无' }}</strong>
          <small>可选，不替代 Token 成本</small>
        </div>
      </section>

      <button
        v-if="isMetered && !showFixedProfileEditor"
        type="button"
        class="cost-overhead-toggle"
        data-testid="enable-fixed-overhead"
        @click="fixedEditorOpen = true"
      >
        另有月租、充值手续费或专线费？添加固定附加成本
      </button>

      <section v-if="showFixedProfileEditor" class="cost-inspector__summary">
        <div>
          <span>{{ isMetered ? '累计固定附加成本' : '累计采购成本（配置推算）' }}</span>
          <strong>{{ formatMoney(currentAccrued, form.currency, 4) }}</strong>
        </div>
        <div>
          <span>{{ isMetered ? '固定附加小时成本' : '折算小时成本（配置推算）' }}</span>
          <strong>{{ formatMoney(currentHourly, form.currency, 5) }}</strong>
        </div>
        <div>
          <span>起算时长</span>
          <strong>{{ currentElapsed.toFixed(1) }} h</strong>
        </div>
      </section>

      <div v-if="showFixedProfileEditor && !isMetered" class="cost-form-field">
        <label for="cost-plan">识别套餐</label>
        <div id="cost-plan" class="cost-readonly-row">
          <span>{{ planLabel }}</span>
          <button type="button" class="cost-text-button" @click="applyPlanDefault">应用美国官方默认价</button>
        </div>
        <small class="cost-official-price-note">Plus $20/月；Pro $100 起；Business/旧 Team $25/席位/月；美国 K–12 已验证教育者当前免费。Pro 与年付方案请按实际账单覆盖。</small>
      </div>

      <div v-if="showFixedProfileEditor" class="cost-form-grid">
        <div class="cost-form-field">
          <label for="cost-amount">{{ isMetered ? '固定附加金额' : '采购金额' }}</label>
          <input id="cost-amount" v-model.number="form.amount" type="number" min="0" step="0.01" inputmode="decimal" />
        </div>
        <div class="cost-form-field">
          <label for="cost-currency">币种</label>
          <select id="cost-currency" v-model="form.currency">
            <option value="CNY">CNY · 人民币</option>
            <option value="USD">USD · 美元</option>
          </select>
        </div>
      </div>

      <div v-if="showFixedProfileEditor" class="cost-form-field">
        <label for="cost-cycle">计费周期</label>
        <select id="cost-cycle" v-model="form.billing_cycle">
          <option value="hourly">每小时</option>
          <option value="daily">每日</option>
          <option value="weekly">每周</option>
          <option value="monthly">每月（730 小时）</option>
          <option value="one_time">一次性</option>
        </select>
      </div>

      <div v-if="showFixedProfileEditor" class="cost-form-field">
        <label for="cost-started">成本起算时间</label>
        <input id="cost-started" v-model="form.started_at" type="datetime-local" :min="joinedAtLocal" />
        <small>账号加入时间：{{ formatDateTime(account.created_at) }}。成本不会早于该时刻计算。</small>
      </div>

      <section v-if="showFixedProfileEditor" class="cost-form-note">
        <Clock3 :size="16" />
        <p v-if="isMetered">这里仅配置 Token 费用之外的固定附加成本。模型调用仍按 usage_logs、实际上游模型、价格目录或渠道价格自动计算，两部分在综合成本中相加。</p>
        <p v-else>这里展示的是成本档案推算，不是上游账单。号码加入后按实际毫秒线性累计；月费按 730 小时折算，一次性成本在起算时刻计入全额。</p>
      </section>

      <section class="cost-form-meta">
        <div><span>平台</span><strong>{{ account.platform }}</strong></div>
        <div><span>账号类型</span><strong>{{ account.type }}</strong></div>
        <div><span>成本模式</span><strong>{{ isMetered ? '模型用量自动计费' : '固定订阅采购' }}</strong></div>
        <div><span>配置来源</span><strong>{{ configurationSourceLabel }}</strong></div>
        <div><span>算法版本</span><strong>v{{ resolved.algorithm_version }}</strong></div>
      </section>
    </div>

    <footer class="cost-inspector__footer">
      <button type="button" class="cost-button cost-button--quiet" @click="$emit('close')">{{ isMetered && !showFixedProfileEditor ? '完成' : '取消' }}</button>
      <button v-if="showFixedProfileEditor" type="button" class="cost-button cost-button--primary" :disabled="saving || !isValid" @click="submit">
        <LoaderCircle v-if="saving" class="cost-spin" :size="16" />
        <Save v-else :size="16" />
        {{ isMetered ? '保存固定附加成本' : '保存成本档案' }}
      </button>
    </footer>
  </aside>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Clock3, LoaderCircle, Save, X } from '@lucide/vue'
import type { Account } from '@/types'
import {
  DEFAULT_MONTHLY_PRICES_USD,
  COST_ALGORITHM_VERSION,
  accruedCost,
  elapsedHours,
  formatMoney,
  hourlyRate,
  inferPlan,
  resolveAccountBillingMode,
  resolveCostProfile,
  type BillingCycle,
  type CostCurrency,
  type CostProfile,
} from '../model'
import { classifyUpstreamProvider, describeUpstreamOrigin } from '../upstreamProvider'

const props = defineProps<{
  account: Account | null
  accountCount?: number
  saving: boolean
  now: Date
}>()

const emit = defineEmits<{
  close: []
  save: [profile: CostProfile]
}>()

const form = reactive<{
  amount: number
  currency: CostCurrency
  billing_cycle: BillingCycle
  started_at: string
}>({ amount: 0, currency: 'CNY', billing_cycle: 'monthly', started_at: '' })
const fixedEditorOpen = ref(false)

function toLocalInput(value: string): string {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return ''
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

function toIso(value: string, fallback: string): string {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toISOString() : fallback
}

watch(
  () => props.account,
  (account) => {
    if (!account) return
    const profile = resolveCostProfile(account)
    form.amount = profile.amount
    form.currency = profile.currency
    form.billing_cycle = profile.billing_cycle
    form.started_at = toLocalInput(profile.started_at)
    fixedEditorOpen.value = resolveAccountBillingMode(account) === 'metered' && profile.source === 'custom'
  },
  { immediate: true },
)

const plan = computed(() => props.account ? inferPlan(props.account) : 'unknown')
const planLabel = computed(() => plan.value === 'unknown' ? '未识别' : plan.value.toUpperCase())
const billingMode = computed(() => props.account ? resolveAccountBillingMode(props.account) : 'subscription')
const isMetered = computed(() => billingMode.value === 'metered')
const showFixedProfileEditor = computed(() => !isMetered.value || fixedEditorOpen.value)
const inspectorTitle = computed(() => {
  if (props.accountCount && props.accountCount > 1) return isMetered.value ? '批量固定附加成本' : '批量成本配置'
  return isMetered.value ? 'API 按量成本' : '号码成本配置'
})
const upstreamProvider = computed(() => props.account ? classifyUpstreamProvider(props.account) : 'other')
const upstreamOrigin = computed(() => props.account ? describeUpstreamOrigin(props.account) : '上游 API')
const meteredPriceSource = computed(() => upstreamProvider.value === 'relay' ? '渠道自定义价格' : '官方模型价格目录')
const meteredExplanation = computed(() => upstreamProvider.value === 'relay'
  ? '系统优先采用中转渠道的模型价格；未配置渠道价时才使用模型目录与账号倍率估算。上游没有返回 usage 或模型缺价时会明确标记，不能伪造精确成本。'
  : '系统根据每次请求返回的实际 Token、缓存命中量和实际上游模型，自动匹配官方模型价格目录。价格随目录同步更新，模型缺价时会明确告警。')
const accountRateMultiplier = computed(() => {
  const value = Number(props.account?.rate_multiplier ?? 1)
  return Number.isFinite(value) && value >= 0 ? value : 1
})
const resolved = computed(() => props.account ? resolveCostProfile(props.account) : {
  amount: 0,
  currency: 'CNY' as CostCurrency,
  billing_cycle: 'monthly' as BillingCycle,
  started_at: new Date().toISOString(),
  source: 'default' as const,
  algorithm_version: COST_ALGORITHM_VERSION,
})
const configurationSourceLabel = computed(() => {
  if (isMetered.value) return resolved.value.source === 'custom' ? '自动按量 + 固定附加' : meteredPriceSource.value
  return resolved.value.source === 'custom' ? '用户自定义' : '套餐默认'
})
const joinedAtLocal = computed(() => props.account ? toLocalInput(props.account.created_at) : '')
const draftProfile = computed<CostProfile>(() => ({
  amount: Number(form.amount) || 0,
  currency: form.currency,
  billing_cycle: form.billing_cycle,
  started_at: props.account ? maxStartTime(toIso(form.started_at, props.account.created_at), props.account.created_at) : new Date().toISOString(),
  source: 'custom',
  algorithm_version: COST_ALGORITHM_VERSION,
}))
const currentAccrued = computed(() => accruedCost(draftProfile.value, props.now))
const currentHourly = computed(() => hourlyRate(draftProfile.value))
const currentElapsed = computed(() => elapsedHours(draftProfile.value.started_at, props.now))
const isValid = computed(() => Number.isFinite(Number(form.amount)) && Number(form.amount) >= 0 && Boolean(form.started_at))

function maxStartTime(requested: string, joinedAt: string): string {
  return new Date(requested).getTime() < new Date(joinedAt).getTime() ? joinedAt : requested
}

function applyPlanDefault() {
  form.amount = DEFAULT_MONTHLY_PRICES_USD[plan.value]
  form.currency = 'USD'
  form.billing_cycle = 'monthly'
  if (props.account) form.started_at = toLocalInput(props.account.created_at)
}

function formatDateTime(value: string): string {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '无数据'
}

function submit() {
  if (!isValid.value) return
  emit('save', draftProfile.value)
}
</script>

<style scoped>
.cost-inspector {
  position: fixed;
  z-index: 100;
  top: 0;
  right: 0;
  bottom: 0;
  display: flex;
  width: min(430px, 100vw);
  flex-direction: column;
  color: #e7ece4;
  background: #111511;
  border-left: 1px solid #465047;
  box-shadow: -18px 0 48px rgb(0 0 0 / 32%);
  border-radius: 16px 0 0 16px;
  font-family: 'Segoe UI Variable', 'Microsoft YaHei UI', sans-serif;
}

.cost-inspector__header,
.cost-inspector__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 20px;
  border-bottom: 1px solid #303830;
  background: #111511;
}

.cost-inspector__header h2 { margin: 3px 0 0; font-size: 20px; font-weight: 680; }
.cost-inspector__header p { margin: 4px 0 0; color: #8b968d; font-family: 'Cascadia Mono', monospace; font-size: 12px; }
.cost-kicker { color: #b9e55a; font-family: 'Cascadia Mono', monospace; font-size: 10px; letter-spacing: .08em; }
.cost-icon-button { display: grid; width: 34px; height: 34px; place-items: center; color: #aab4aa; background: transparent; border: 1px solid #374037; }
.cost-icon-button:active { transform: translateY(1px); background: #202720; }

.cost-inspector__body { flex: 1; min-height: 0; overflow: auto; padding: 20px; }
.cost-metered-card { margin-bottom: 16px; padding: 18px; background: linear-gradient(135deg, #1b2418, #131913); border: 1px solid #50643b; border-radius: 12px; }
.cost-metered-card h3 { margin: 8px 0 4px; color: #eef3e9; font-size: 18px; }
.cost-metered-card > strong { display: block; color: #b9e55a; font-size: 21px; }
.cost-metered-card > p { margin: 10px 0 0; color: #a4aea4; font-size: 12px; line-height: 1.7; }
.cost-metered-flow { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-top: 14px; color: #aab5a8; font-family: 'Cascadia Mono', monospace; font-size: 10px; }
.cost-metered-flow span { padding: 5px 7px; background: #111711; border: 1px solid #354136; border-radius: 6px; }
.cost-metered-flow i { color: #b9e55a; font-style: normal; }
.cost-overhead-toggle { width: 100%; margin: 0 0 18px; padding: 11px 13px; color: #b9e55a; background: #151b15; border: 1px dashed #536143; border-radius: 9px; font-size: 12px; text-align: left; }
.cost-overhead-toggle:active { background: #20291e; }
.cost-inspector__summary { display: grid; grid-template-columns: repeat(3, 1fr); margin-bottom: 20px; border: 1px solid #394239; border-radius: 11px; overflow: hidden; }
.cost-inspector__summary div { padding: 14px; border-right: 1px solid #394239; border-bottom: 1px solid #394239; }
.cost-inspector__summary div:last-child { border-right: 0; border-bottom: 0; }
.cost-inspector__summary span, .cost-form-meta span { display: block; color: #7f8b81; font-size: 11px; }
.cost-inspector__summary strong { display: block; margin-top: 5px; color: #c4ed63; font-family: 'Cascadia Mono', monospace; font-size: 17px; }
.cost-inspector__summary small { display: block; margin-top: 5px; color: #707c72; font-size: 10px; line-height: 1.45; }
.cost-inspector__summary--metered strong { font-size: 15px; }

.cost-form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.cost-form-field { margin-bottom: 16px; }
.cost-form-field label { display: block; margin-bottom: 7px; color: #9ea89f; font-size: 12px; }
.cost-form-field input, .cost-form-field select { width: 100%; height: 40px; padding: 0 11px; color: #eef2e9; background: #171c17; border: 1px solid #3a443b; border-radius: 9px; outline: none; }
.cost-form-field input:focus, .cost-form-field select:focus { border-color: #b9e55a; box-shadow: 0 0 0 1px #b9e55a; }
.cost-form-field small { display: block; margin-top: 7px; color: #727e74; line-height: 1.5; }
.cost-form-field .cost-official-price-note { color: #8f9b91; }
.cost-readonly-row { display: flex; min-height: 40px; align-items: center; justify-content: space-between; gap: 12px; padding: 0 10px; background: #171c17; border: 1px solid #3a443b; border-radius: 9px; }
.cost-readonly-row span { color: #d9dfd5; font-family: 'Cascadia Mono', monospace; }
.cost-text-button { padding: 4px; color: #b9e55a; background: transparent; border: 0; font-size: 11px; }
.cost-text-button:active { color: #e3ff9d; }
.cost-form-note { display: flex; gap: 10px; margin: 20px 0; padding: 13px; color: #a6b0a6; background: #1a2118; border-left: 2px solid #b9e55a; }
.cost-form-note p { margin: 0; font-size: 12px; line-height: 1.65; }
.cost-form-meta { display: grid; grid-template-columns: repeat(2, 1fr); gap: 1px; background: #303830; border: 1px solid #303830; border-radius: 11px; overflow: hidden; }
.cost-form-meta div { min-width: 0; padding: 11px; background: #151a15; }
.cost-form-meta strong { display: block; margin-top: 4px; color: #dbe1d8; font-size: 12px; font-weight: 550; }

.cost-inspector__footer { position: relative; z-index: 1; justify-content: flex-end; border-top: 1px solid #303830; border-bottom: 0; box-shadow: 0 -12px 24px rgb(0 0 0 / 16%); }
.cost-button { display: inline-flex; height: 40px; align-items: center; gap: 8px; padding: 0 15px; color: #dbe1d8; background: #1a201a; border: 1px solid #465047; border-radius: 9px; }
.cost-button:active { transform: translateY(1px); }
.cost-button--primary { color: #10140f; background: #b9e55a; border-color: #b9e55a; font-weight: 700; }
.cost-button:disabled { opacity: .48; }
.cost-spin { animation: cost-spin .8s linear infinite; }

@keyframes cost-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .cost-spin { animation-duration: 1.8s; } }
@media (max-width: 520px) {
  .cost-inspector { width: 100vw; border-radius: 0; }
  .cost-inspector__header, .cost-inspector__footer { padding-inline: 16px; }
  .cost-inspector__body { padding: 16px; }
  .cost-inspector__summary { grid-template-columns: 1fr 1fr; }
  .cost-inspector__summary div:last-child { grid-column: 1 / -1; border-right: 0; }
  .cost-form-grid { grid-template-columns: 1fr; gap: 0; }
}
</style>
