<template>
  <td class="upstream-probe-cell" aria-live="polite">
    <button
      type="button"
      class="upstream-probe-cell__trigger"
      :class="stateClass"
      :disabled="state?.loading"
      :aria-label="`检测 ${accountName} 的真实上游连接总耗时`"
      :title="detail"
      @click="emit('probe')"
    >
      <span class="upstream-probe-cell__value">
        <LoaderCircle v-if="state?.loading" :size="14" class="upstream-probe-cell__spinner" />
        <Activity v-else :size="14" />
        <strong>{{ value }}</strong>
      </span>
      <small>{{ detail }}</small>
    </button>
  </td>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Activity, LoaderCircle } from '@lucide/vue'
import type { AccountProbeState } from '../upstreamTable'

const props = defineProps<{
  accountName: string
  state?: AccountProbeState
}>()

const emit = defineEmits<{
  probe: []
}>()

const value = computed(() => {
  if (!props.state) return '未测试'
  if (props.state.loading) return '检测中'
  if (props.state.latency_ms != null) return `${Math.max(1, Math.round(props.state.latency_ms)).toLocaleString()} ms`
  return props.state.success ? '可用' : '失败'
})

const detail = computed(() => {
  if (!props.state) return '点击开始真实检测'
  if (props.state.loading) return props.state.message || '正在请求真实上游…'
  if (props.state.success) return '成功 · 连接测试总耗时'
  return `失败 · ${props.state.message || '未收到有效上游响应'}`
})

const stateClass = computed(() => ({
  'is-loading': props.state?.loading,
  'is-success': props.state?.success === true,
  'is-error': props.state?.success === false,
}))
</script>

<style scoped>
.upstream-probe-cell {
  padding: 0 !important;
}

.upstream-probe-cell__trigger {
  display: grid;
  width: 100%;
  min-width: 132px;
  min-height: 72px;
  align-content: center;
  gap: 5px;
  padding: 10px 12px;
  color: inherit;
  text-align: left;
  background: transparent;
  border: 0;
  border-radius: 10px;
  cursor: pointer;
  transition: color 140ms ease, background 140ms ease, box-shadow 140ms ease;
}

.upstream-probe-cell__trigger:hover,
.upstream-probe-cell__trigger:focus-visible {
  color: #eff8d8;
  background: rgb(185 229 90 / 8%);
  box-shadow: inset 0 0 0 1px rgb(185 229 90 / 34%);
  outline: none;
}

.upstream-probe-cell__trigger:disabled {
  cursor: wait;
  opacity: .76;
}

.upstream-probe-cell__value {
  display: flex;
  align-items: center;
  gap: 6px;
}

.upstream-probe-cell__value strong {
  color: #edf2eb;
}

.upstream-probe-cell__trigger small {
  display: block;
  max-width: 180px;
  overflow: hidden;
  color: #7f8c82;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.upstream-probe-cell__trigger.is-success { background: rgb(185 229 90 / 5%); }
.upstream-probe-cell__trigger.is-success svg,
.upstream-probe-cell__trigger.is-success small { color: #a7cf58; }
.upstream-probe-cell__trigger.is-error { background: rgb(228 138 114 / 6%); }
.upstream-probe-cell__trigger.is-error svg,
.upstream-probe-cell__trigger.is-error small { color: #e48a72; }

.upstream-probe-cell__spinner { animation: upstream-probe-spin 850ms linear infinite; }

@keyframes upstream-probe-spin {
  to { transform: rotate(360deg); }
}
</style>
