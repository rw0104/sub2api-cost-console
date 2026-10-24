<template>
  <div class="model-contribution-chart" :style="{ height: `${height}px` }">
    <Bar v-if="rows.length && !['loading', 'unavailable', 'empty'].includes(effectiveState)" :data="chartData" :options="chartOptions" />
    <div v-else class="model-contribution-chart__state"><strong>{{ dataAvailabilityLabel(effectiveState) }}</strong><span>{{ effectiveReason }}</span></div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Bar } from 'vue-chartjs'
import { BarElement, CategoryScale, Chart as ChartJS, Legend, LinearScale, Tooltip, type ChartData, type ChartOptions } from 'chart.js'
import type { ModelCostRow } from '../modelCostAnalysis'
import { dataAvailabilityLabel, type DataAvailability } from '../dataState'

ChartJS.register(CategoryScale, LinearScale, BarElement, Tooltip, Legend)

const props = withDefaults(defineProps<{
  rows: ModelCostRow[]
  state: DataAvailability
  stateReason?: string
  height?: number
}>(), { stateReason: '', height: 230 })

const topRows = computed(() => [...props.rows]
  .sort((left, right) => right.revenue - left.revenue || right.accountCost - left.accountCost)
  .slice(0, 8))
const effectiveState = computed<DataAvailability>(() => !props.rows.length && !['loading', 'unavailable'].includes(props.state) ? 'empty' : props.state)
const effectiveReason = computed(() => effectiveState.value === 'empty' ? props.stateReason || '所选窗口没有模型贡献数据' : props.stateReason)

const chartData = computed<ChartData<'bar'>>(() => ({
  labels: topRows.value.map((row) => row.model || '未知模型'),
  datasets: [
    { label: '用户收入', data: topRows.value.map((row) => row.revenue), backgroundColor: '#e0bd4e' },
    { label: '上游成本', data: topRows.value.map((row) => row.accountCost), backgroundColor: '#7eb6d8' },
    { label: '毛利', data: topRows.value.map((row) => row.grossProfit), backgroundColor: '#b9e55a' },
  ],
}))

const chartOptions = computed<ChartOptions<'bar'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  animation: { duration: 180 },
  interaction: { mode: 'index', intersect: false },
  plugins: {
    legend: { align: 'end', labels: { color: '#8f9b91', boxWidth: 11, boxHeight: 5, font: { size: 10 } } },
    tooltip: { backgroundColor: '#111611', borderColor: '#3b443b', borderWidth: 1, callbacks: { label: (context) => ` ${context.dataset.label}: $${Number(context.parsed.y || 0).toFixed(6)}` } },
  },
  scales: {
    x: { grid: { display: false }, ticks: { color: '#768079', maxRotation: 0, autoSkip: true, font: { size: 9 } } },
    y: { beginAtZero: true, grid: { color: '#293129' }, ticks: { color: '#768079', callback: (value) => `$${Number(value).toLocaleString(undefined, { maximumFractionDigits: 4 })}` } },
  },
}))
</script>

<style scoped>
.model-contribution-chart { width: 100%; min-width: 0; }
.model-contribution-chart__state { display: grid; height: 100%; place-content: center; gap: 5px; border: 1px dashed rgb(255 255 255 / 13%); border-radius: 10px; color: #a9b2aa; text-align: center; }
.model-contribution-chart__state span { color: #758078; font-size: 10px; }
</style>
