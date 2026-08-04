<template>
  <div class="cost-chart" :style="{ height: `${height}px` }">
    <Line :data="chartData" :options="chartOptions" />
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
}>(), {
  height: 190,
  valuePrefix: '',
  valueSuffix: '',
})

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

@media (prefers-reduced-motion: reduce) {
  .cost-chart {
    scroll-behavior: auto;
  }
}
</style>
