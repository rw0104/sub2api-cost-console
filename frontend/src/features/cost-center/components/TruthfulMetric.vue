<template>
  <div
    class="cost-metric-cell"
    :class="[accent ? `accent-${accent}` : '', `data-${state}`]"
    :data-data-state="state"
  >
    <span class="cost-metric-cell__label">{{ label }}</span>
    <strong>{{ value }}</strong>
    <small>{{ note }}</small>
    <em v-if="state !== 'measured'" class="cost-metric-cell__state">
      <CircleAlert v-if="state === 'unavailable'" :size="11" />
      <Clock3 v-else-if="state === 'loading' || state === 'stale'" :size="11" />
      <Sigma v-else-if="state === 'estimated'" :size="11" />
      <CircleMinus v-else-if="state === 'empty'" :size="11" />
      <TriangleAlert v-else :size="11" />
      {{ dataAvailabilityLabel(state) }}
    </em>
  </div>
</template>

<script setup lang="ts">
import { CircleAlert, CircleMinus, Clock3, Sigma, TriangleAlert } from '@lucide/vue'
import { dataAvailabilityLabel, type DataAvailability } from '../dataState'

withDefaults(defineProps<{
  label?: string
  value?: string
  note?: string
  accent?: string
  state?: DataAvailability
}>(), {
  label: '',
  value: '无数据',
  note: '',
  accent: '',
  state: 'measured',
})
</script>

<style scoped>
.cost-metric-cell {
  position: relative;
}

.cost-metric-cell__state {
  position: absolute;
  top: 10px;
  right: 12px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: 1px solid rgb(255 255 255 / 12%);
  border-radius: 999px;
  padding: 3px 6px;
  color: #9ca69e;
  background: rgb(255 255 255 / 4%);
  font: 500 9px/1 'Segoe UI Variable', sans-serif;
  font-style: normal;
  letter-spacing: .02em;
}

.cost-metric-cell.data-unavailable strong,
.cost-metric-cell.data-loading strong {
  color: #aab2ab;
}

.cost-metric-cell.data-unavailable .cost-metric-cell__state { color: #e39787; border-color: rgb(227 151 135 / 32%); }
.cost-metric-cell.data-estimated .cost-metric-cell__state,
.cost-metric-cell.data-partial .cost-metric-cell__state { color: #e0bd4e; border-color: rgb(224 189 78 / 32%); }
.cost-metric-cell.data-empty .cost-metric-cell__state,
.cost-metric-cell.data-stale .cost-metric-cell__state { color: #91a3b0; border-color: rgb(145 163 176 / 30%); }

@media (forced-colors: active) {
  .cost-metric-cell__state { border-color: CanvasText; color: CanvasText; }
}
</style>
