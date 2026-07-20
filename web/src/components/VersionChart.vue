<script setup>
import { computed } from 'vue'
import { Bar } from 'vue-chartjs'
import {
  Chart as ChartJS,
  Title,
  Tooltip,
  BarElement,
  CategoryScale,
  LinearScale,
} from 'chart.js'

ChartJS.register(Title, Tooltip, BarElement, CategoryScale, LinearScale)

const props = defineProps({
  versions: { type: Array, required: true }, // [{ version, count }]
  latestVersion: { type: Number, required: true },
})

const chartData = computed(() => ({
  labels: props.versions.map((b) => `v${b.version}`),
  datasets: [
    {
      label: 'tenants',
      data: props.versions.map((b) => b.count),
      // Latest version is the goal state, so highlight it green; earlier
      // versions (tenants still mid-rollout) read amber.
      backgroundColor: props.versions.map((b) =>
        b.version === props.latestVersion ? '#3fb950' : '#d29922',
      ),
      borderRadius: 4,
    },
  ],
}))

const chartOptions = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: { legend: { display: false } },
  scales: {
    x: { grid: { display: false }, ticks: { color: '#8b949e' } },
    y: {
      beginAtZero: true,
      grid: { color: '#30363d' },
      ticks: { color: '#8b949e', precision: 0 },
    },
  },
}
</script>

<template>
  <div class="panel chart-wrap">
    <Bar :data="chartData" :options="chartOptions" />
  </div>
</template>
