<script setup>
import { computed } from 'vue'

const props = defineProps({
  summary: { type: Object, required: true },
})

// Each card carries a semantic color: behind/drifted turn amber/red only when
// non-zero, so a healthy fleet reads as calm green/neutral.
const cards = computed(() => {
  const s = props.summary
  return [
    { label: 'Total tenants', value: s.total, tone: 'neutral' },
    { label: `On latest (v${s.latest_version})`, value: s.up_to_date, tone: 'good' },
    { label: 'Behind', value: s.behind, tone: s.behind > 0 ? 'warn' : 'neutral' },
    { label: 'Drifted', value: s.drifted, tone: s.drifted > 0 ? 'bad' : 'neutral' },
  ]
})
</script>

<template>
  <div class="cards">
    <div v-for="c in cards" :key="c.label" class="card" :class="c.tone">
      <div class="value">{{ c.value }}</div>
      <div class="label">{{ c.label }}</div>
    </div>
  </div>
</template>
