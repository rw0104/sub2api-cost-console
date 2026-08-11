<template>
  <div class="cost-chart" :class="`data-${state}`" :style="{ height: `${height}px` }" :data-data-state="state">
    <Line v-if="showChart" :data="chartData" :options="chartOptions" />
    <div v-else class="cost-chart__state" role="status">
      <span>{{ stateLabel }}</span>
      <small>{{ stateReason }}</small>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import {
  CategoryScale,
  Chart as ChartJS,
  Filler,
  Legend,
  LinearScale,
  LineElement,
  PointElement,
  Tooltip,
  type ChartData,
  type ChartOptions,
} from 'chart.js'
import { Line } from 'vue-chartjs'
import { dataAvailabilityLabel, type DataAvailability } from '../dataState'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Filler, Tooltip, Legend)

export interface CostChartSeries {
  label: string
  data: number[]
  color: string
  dashed?: boolean
  fill?: boolean
}

const props = withDefaults(defineProps<{
  labels: string[]
  series: CostChartSeries[]
  height?: number
  valuePrefix?: string
  valueSuffix?: string
  state?: DataAvailability
  stateReason?: string
}>(), {
  height: 190,
  valuePrefix: '',
  valueSuffix: '',
  state: 'measured',
  stateReason: '',
})

const showChart = computed(() => !['loading', 'unavailable', 'empty'].includes(props.state))
const stateLabel = computed(() => dataAvailabilityLabel(props.state))

const chartData = computed<ChartData<'line'>>(() => ({
  labels: props.labels,
  datasets: props.series.map((item) => ({
    label: item.label,
    data: item.data,
    borderColor: item.color,
    backgroundColor: item.fill ? `${item.color}16` : 'transparent',
    borderDash: item.dashed ? [5, 4] : [],
    borderWidth: item.dashed ? 1.4 : 1.8,
    fill: item.fill ? 'origin' : false,
    pointRadius: 0,
    pointHoverRadius: 4,
    pointHoverBorderWidth: 1,
    pointHoverBackgroundColor: '#101410',
    tension: 0.18,
  })),
}))

const chartOptions = computed<ChartOptions<'line'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  animation: { duration: 180 },
  interaction: { mode: 'index', intersect: false },
  plugins: {
    legend: {
      display: true,
      align: 'end',
      labels: {
        color: '#8f9b91',
        boxWidth: 16,
        boxHeight: 2,
        padding: 14,
        font: { family: 'Bahnschrift, Segoe UI Variable, sans-serif', size: 10 },
      },
    },
    tooltip: {
      backgroundColor: '#111611',
      borderColor: '#3b443b',
      borderWidth: 1,
      titleColor: '#f1f4ec',
      bodyColor: '#b8c1b8',
      padding: 10,
      callbacks: {
        label: (context) => {
          const value = Number(context.parsed.y ?? 0)
          return ` ${context.dataset.label}: ${props.valuePrefix}${value.toLocaleString(undefined, { maximumFractionDigits: 4 })}${props.valueSuffix}`
        },
      },
    },
  },
  scales: {
    x: {
      grid: { display: false },
      border: { display: false },
      ticks: {
        color: '#68736a',
        maxTicksLimit: 7,
        maxRotation: 0,
        font: { family: 'Cascadia Mono, Consolas, monospace', size: 9 },
      },
    },
    y: {
      beginAtZero: true,
      grid: { color: '#293129', lineWidth: 0.7 },
      border: { display: false },
      ticks: {
        color: '#68736a',
        maxTicksLimit: 4,
        padding: 8,
        font: { family: 'Cascadia Mono, Consolas, monospace', size: 9 },
        callback: (value) => `${props.valuePrefix}${Number(value).toLocaleString(undefined, { maximumFractionDigits: 2 })}${props.valueSuffix}`,
      },
    },
  },
}))
</script>

<style scoped>
.cost-chart {
  position: relative;
  width: 100%;
  min-width: 0;
}

.cost-chart__state {
  display: grid;
  height: 100%;
  place-content: center;
  gap: 6px;
  border: 1px dashed rgb(255 255 255 / 13%);
  border-radius: 10px;
  color: #a9b2aa;
  text-align: center;
}

.cost-chart__state span { font-size: 14px; font-weight: 650; }
.cost-chart__state small { max-width: 420px; color: #758078; font-size: 11px; }
.cost-chart.data-unavailable .cost-chart__state { border-color: rgb(213 132 115 / 35%); }

@media (prefers-reduced-motion: reduce) {
  .cost-chart {
    scroll-behavior: auto;
  }
}
</style>
