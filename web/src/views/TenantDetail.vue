<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../api.js'
import StatusBadge from '../components/StatusBadge.vue'

const props = defineProps({
  schema: { type: String, required: true },
})

const report = ref(null) // drift diff report for this tenant
const row = ref(null) // migration/drift summary row from the fleet list
const loading = ref(true)
const error = ref('')

async function load(refresh = false) {
  loading.value = true
  error.value = ''
  try {
    const [diff, fleet] = await Promise.all([
      api.tenantDrift(props.schema, { refresh }),
      api.tenants({ refresh }),
    ])
    report.value = diff.tenants[0] || { schema: props.schema, drifted: false, differences: [] }
    row.value = fleet.tenants.find((t) => t.schema === props.schema) || null
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

onMounted(() => load())
watch(() => props.schema, () => load())

const reference = computed(() => report.value && report.value.reference)

// Group object differences by type (table, index, column, constraint) so the
// diff reads as an organized list rather than a flat dump.
const groups = computed(() => {
  if (!report.value || !report.value.differences) return []
  const byType = new Map()
  for (const d of report.value.differences) {
    if (!byType.has(d.type)) byType.set(d.type, [])
    byType.get(d.type).push(d)
  }
  return [...byType.entries()]
    .map(([type, items]) => ({ type, items }))
    .sort((a, b) => a.type.localeCompare(b.type))
})

function classBadge(cls) {
  // missing = red (something the reference has, tenant lacks), extra = amber
  // (tenant has something extra), modified = amber.
  return cls === 'missing' ? { kind: 'bad', label: cls } : { kind: 'warn', label: cls }
}
</script>

<template>
  <div>
    <div class="toolbar">
      <RouterLink to="/" class="btn">&larr; Fleet</RouterLink>
      <button :disabled="loading" @click="load(true)">Refresh</button>
    </div>

    <div v-if="error" class="error">Failed to load tenant: {{ error }}</div>

    <template v-if="report">
      <div class="detail-header">
        <h2>{{ schema }}</h2>
        <template v-if="row">
          <StatusBadge
            v-bind="
              row.migration_status === 'behind'
                ? { kind: 'warn', label: 'behind (v' + row.version + ')' }
                : { kind: 'ok', label: 'up-to-date (v' + row.version + ')' }
            "
          />
        </template>
        <StatusBadge
          v-bind="report.drifted ? { kind: 'bad', label: 'drifted' } : { kind: 'ok', label: 'clean' }"
        />
        <span v-if="reference" class="muted">vs {{ reference }}</span>
      </div>

      <template v-if="report.drifted">
        <div v-for="g in groups" :key="g.type" class="diff-group">
          <header>{{ g.type }} &middot; {{ g.items.length }}</header>
          <div v-for="d in g.items" :key="d.name" class="diff-object">
            <StatusBadge v-bind="classBadge(d.class)" />
            <span class="obj-name"> {{ d.name }}</span>
            <div v-for="ch in d.changes || []" :key="ch.field" class="change-line">
              {{ ch.field }}:
              <span class="from">{{ ch.from || '(none)' }}</span> &rarr;
              <span class="to">{{ ch.to || '(none)' }}</span>
            </div>
          </div>
        </div>
      </template>

      <div v-else class="match-state">
        <StatusBadge kind="ok" label="&#10003;" />
        <span>This tenant matches the reference &mdash; no schema drift detected.</span>
      </div>
    </template>

    <div v-else-if="loading && !error" class="empty">Loading tenant&hellip;</div>
  </div>
</template>
