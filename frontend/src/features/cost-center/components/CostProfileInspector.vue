<template>
  <aside v-if="account" class="cost-inspector" role="dialog" aria-modal="false" aria-labelledby="cost-profile-title">
    <header class="cost-inspector__header">
      <div>
        <span class="cost-kicker">ACCOUNT COST PROFILE</span>
        <h2 id="cost-profile-title">号码成本配置</h2>
        <p>{{ account.name }}</p>
      </div>
      <button type="button" class="cost-icon-button" aria-label="关闭成本配置" title="关闭 (Esc)" @click="$emit('close')">
        <X :size="18" />
      </button>
    </header>

    <div class="cost-inspector__body">
      <section class="cost-inspector__summary">
        <div>
          <span>累计采购成本</span>
          <strong>{{ formatMoney(currentAccrued, form.currency, 4) }}</strong>
        </div>
        <div>
          <span>折算小时成本</span>
          <strong>{{ formatMoney(currentHourly, form.currency, 5) }}</strong>
        </div>
        <div>
          <span>起算时长</span>
          <strong>{{ currentElapsed.toFixed(1) }} h</strong>
        </div>
      </section>

      <div class="cost-form-field">
        <label for="cost-plan">识别套餐</label>
        <div id="cost-plan" class="cost-readonly-row">
          <span>{{ planLabel }}</span>
          <button type="button" class="cost-text-button" @click="applyPlanDefault">应用套餐默认价</button>
        </div>
      </div>

      <div class="cost-form-grid">
        <div class="cost-form-field">
          <label for="cost-amount">采购金额</label>
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

      <div class="cost-form-field">
        <label for="cost-cycle">计费周期</label>
        <select id="cost-cycle" v-model="form.billing_cycle">
          <option value="hourly">每小时</option>
          <option value="daily">每日</option>
          <option value="weekly">每周</option>
          <option value="monthly">每月（730 小时）</option>
          <option value="one_time">一次性</option>
        </select>
      </div>

      <div class="cost-form-field">
        <label for="cost-started">成本起算时间</label>
        <input id="cost-started" v-model="form.started_at" type="datetime-local" :min="joinedAtLocal" />
        <small>账号加入时间：{{ formatDateTime(account.created_at) }}。成本不会早于该时刻计算。</small>
      </div>

      <section class="cost-form-note">
        <Clock3 :size="16" />
        <p>号码加入后按实际毫秒线性累计；月费按 730 小时折算。一次性成本在起算时刻计入全额。</p>
      </section>

      <section class="cost-form-meta">
        <div><span>平台</span><strong>{{ account.platform }}</strong></div>
        <div><span>账号类型</span><strong>{{ account.type }}</strong></div>
        <div><span>配置来源</span><strong>{{ resolved.source === 'custom' ? '自定义' : '套餐默认' }}</strong></div>
        <div><span>算法版本</span><strong>v{{ resolved.algorithm_version }}</strong></div>
      </section>
    </div>

    <footer class="cost-inspector__footer">
      <button type="button" class="cost-button cost-button--quiet" @click="$emit('close')">取消</button>
      <button type="button" class="cost-button cost-button--primary" :disabled="saving || !isValid" @click="submit">
        <LoaderCircle v-if="saving" class="cost-spin" :size="16" />
        <Save v-else :size="16" />
        保存成本档案
      </button>
    </footer>
  </aside>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { Clock3, LoaderCircle, Save, X } from '@lucide/vue'
import type { Account } from '@/types'
import {
  DEFAULT_MONTHLY_PRICES_CNY,
  COST_ALGORITHM_VERSION,
  accruedCost,
  elapsedHours,
  formatMoney,
  hourlyRate,
  inferPlan,
  resolveCostProfile,
  type BillingCycle,
  type CostCurrency,
  type CostProfile,
} from '../model'

const props = defineProps<{
  account: Account | null
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
  },
  { immediate: true },
)

const plan = computed(() => props.account ? inferPlan(props.account) : 'unknown')
const planLabel = computed(() => plan.value === 'unknown' ? '未识别' : plan.value.toUpperCase())
const resolved = computed(() => props.account ? resolveCostProfile(props.account) : {
  amount: 0,
  currency: 'CNY' as CostCurrency,
  billing_cycle: 'monthly' as BillingCycle,
  started_at: new Date().toISOString(),
  source: 'default' as const,
  algorithm_version: COST_ALGORITHM_VERSION,
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
  form.amount = DEFAULT_MONTHLY_PRICES_CNY[plan.value]
  form.currency = 'CNY'
  form.billing_cycle = 'monthly'
  if (props.account) form.started_at = toLocalInput(props.account.created_at)
}

function formatDateTime(value: string): string {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '—'
}

function submit() {
  if (!isValid.value) return
  emit('save', draftProfile.value)
}
</script>

<style scoped>
.cost-inspector {
  position: fixed;
  z-index: 70;
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
}

.cost-inspector__header h2 { margin: 3px 0 0; font-size: 20px; font-weight: 680; }
.cost-inspector__header p { margin: 4px 0 0; color: #8b968d; font-family: 'Cascadia Mono', monospace; font-size: 12px; }
.cost-kicker { color: #b9e55a; font-family: 'Cascadia Mono', monospace; font-size: 10px; letter-spacing: .08em; }
.cost-icon-button { display: grid; width: 34px; height: 34px; place-items: center; color: #aab4aa; background: transparent; border: 1px solid #374037; }
.cost-icon-button:active { transform: translateY(1px); background: #202720; }

.cost-inspector__body { flex: 1; min-height: 0; overflow: auto; padding: 20px; }
.cost-inspector__summary { display: grid; grid-template-columns: 1fr 1fr; margin-bottom: 22px; border: 1px solid #394239; }
.cost-inspector__summary div { padding: 14px; border-right: 1px solid #394239; border-bottom: 1px solid #394239; }
.cost-inspector__summary div:nth-child(2) { border-right: 0; }
.cost-inspector__summary div:last-child { grid-column: 1 / -1; border-right: 0; border-bottom: 0; }
.cost-inspector__summary span, .cost-form-meta span { display: block; color: #7f8b81; font-size: 11px; }
.cost-inspector__summary strong { display: block; margin-top: 5px; color: #c4ed63; font-family: 'Cascadia Mono', monospace; font-size: 17px; }

.cost-form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.cost-form-field { margin-bottom: 16px; }
.cost-form-field label { display: block; margin-bottom: 7px; color: #9ea89f; font-size: 12px; }
.cost-form-field input, .cost-form-field select { width: 100%; height: 38px; padding: 0 11px; color: #eef2e9; background: #171c17; border: 1px solid #3a443b; border-radius: 0; outline: none; }
.cost-form-field input:focus, .cost-form-field select:focus { border-color: #b9e55a; box-shadow: 0 0 0 1px #b9e55a; }
.cost-form-field small { display: block; margin-top: 7px; color: #727e74; line-height: 1.5; }
.cost-readonly-row { display: flex; min-height: 38px; align-items: center; justify-content: space-between; gap: 12px; padding: 0 10px; background: #171c17; border: 1px solid #3a443b; }
.cost-readonly-row span { color: #d9dfd5; font-family: 'Cascadia Mono', monospace; }
.cost-text-button { padding: 4px; color: #b9e55a; background: transparent; border: 0; font-size: 11px; }
.cost-text-button:active { color: #e3ff9d; }
.cost-form-note { display: flex; gap: 10px; margin: 20px 0; padding: 13px; color: #a6b0a6; background: #1a2118; border-left: 2px solid #b9e55a; }
.cost-form-note p { margin: 0; font-size: 12px; line-height: 1.65; }
.cost-form-meta { display: grid; grid-template-columns: repeat(3, 1fr); gap: 1px; background: #303830; border: 1px solid #303830; }
.cost-form-meta div { padding: 10px; background: #151a15; }
.cost-form-meta strong { display: block; margin-top: 4px; color: #dbe1d8; font-size: 12px; font-weight: 550; }

.cost-inspector__footer { justify-content: flex-end; border-top: 1px solid #303830; border-bottom: 0; }
.cost-button { display: inline-flex; height: 38px; align-items: center; gap: 8px; padding: 0 15px; color: #dbe1d8; background: #1a201a; border: 1px solid #465047; }
.cost-button:active { transform: translateY(1px); }
.cost-button--primary { color: #10140f; background: #b9e55a; border-color: #b9e55a; font-weight: 700; }
.cost-button:disabled { opacity: .48; }
.cost-spin { animation: cost-spin .8s linear infinite; }

@keyframes cost-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .cost-spin { animation-duration: 1.8s; } }
</style>
