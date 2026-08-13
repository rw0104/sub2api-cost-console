<template>
  <div class="cost-chart" :class="`data-${effectiveState}`" :style="{ height: `${height}px` }" :data-data-state="effectiveState">
    <Line v-if="showChart" :data="chartData" :options="chartOptions" />
    <div v-if="showChart && effectiveState === 'partial'" class="cost-chart__partial" :title="stateReason">部分数据</div>
    <div v-if="showChart && events.length" class="cost-chart__events" aria-label="图表事件">
      <span
        v-for="event in events"
        :key="`${event.index}-${event.label}`"
        class="cost-chart__event"
        :class="`is-${event.severity || 'info'}`"
        :style="{ left: `${eventPosition(event.index)}%` }"
        :title="event.label"
      ><i></i></span>
    </div>
    <div v-if="!showChart" class="cost-chart__state" role="status">
      <span>{{ stateLabel }}</span>
      <small>{{ effectiveReason }}</small>
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
  data: Array<number | null>
  color: string
  dashed?: boolean
  fill?: boolean
}

export interface CostChartEvent {
  index: number
  label: string
  severity?: 'info' | 'warning'
}

const props = withDefaults(defineProps<{
  labels: string[]
  series: CostChartSeries[]
  height?: number
  valuePrefix?: string
  valueSuffix?: string
  state?: DataAvailability
  stateReason?: string
  events?: CostChartEvent[]
}>(), {
  height: 190,
  valuePrefix: '',
  valueSuffix: '',
  state: 'measured',
  stateReason: '',
  events: () => [],
})

const hasPlottableValue = computed(() => props.labels.length > 0 && props.series.some((item) => item.data.some((value) => value != null && Number.isFinite(value))))
const effectiveState = computed<DataAvailability>(() => !['loading', 'unavailable', 'empty'].includes(props.state) && !hasPlottableValue.value ? 'empty' : props.state)
const showChart = computed(() => !['loading', 'unavailable', 'empty'].includes(effectiveState.value))
const stateLabel = computed(() => dataAvailabilityLabel(effectiveState.value))
const effectiveReason = computed(() => effectiveState.value === 'empty' && !hasPlottableValue.value ? props.stateReason || '所选窗口没有可绘制的有效数据' : props.stateReason)

const chartData = computed<ChartData<'line'>>(() => ({
  labels: props.labels,
  datasets: props.series.slice(0, 3).map((item) => ({
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
    spanGaps: false,
  })),
}))

const chartOptions = computed<ChartOptions<'line'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  // Auto refresh is a data replacement, not a page transition. Disabling the
  // replay animation prevents the WebView chart from visibly flashing every
  // refresh cycle while preserving hover interaction.
  animation: false,
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

function eventPosition(index: number): number {
  if (props.labels.length <= 1) return 50
  return Math.max(2, Math.min(98, (index / (props.labels.length - 1)) * 100))
}
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
.cost-chart.data-partial::after {
  position: absolute;
  inset: 28px 0 0;
  pointer-events: none;
  background: repeating-linear-gradient(135deg, transparent 0 10px, rgb(224 189 78 / 2.5%) 10px 20px);
  content: '';
}
.cost-chart__partial { position: absolute; top: 2px; left: 2px; z-index: 2; padding: 2px 6px; border: 1px solid rgb(224 189 78 / 28%); border-radius: 999px; background: #191a12; color: #d8b94d; font-size: 9px; }
.cost-chart__events { position: absolute; inset: 32px 18px 22px 42px; z-index: 3; pointer-events: none; }
.cost-chart__event { position: absolute; top: 0; bottom: 0; width: 1px; border-left: 1px dashed rgb(126 182 216 / 65%); }
.cost-chart__event i { position: absolute; top: -1px; left: -4px; width: 7px; height: 7px; border: 1px solid #111611; border-radius: 50%; background: #7eb6d8; }
.cost-chart__event.is-warning { border-left-color: rgb(224 189 78 / 78%); }
.cost-chart__event.is-warning i { background: #e0bd4e; }

@media (prefers-reduced-motion: reduce) {
  .cost-chart {
    scroll-behavior: auto;
  }
}
</style>
