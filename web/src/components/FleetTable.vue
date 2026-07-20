<script setup>
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import StatusBadge from './StatusBadge.vue'

const props = defineProps({
  tenants: { type: Array, required: true },
})

const router = useRouter()
const search = ref('')
const statusFilter = ref('all') // all | behind | drifted | ok

// Client-side filtering keeps the whole 250-row fleet in the page and makes the
// search box instant; the dataset is small enough that this is simpler and
// snappier than paging the API.
const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return props.tenants.filter((t) => {
    if (q && !t.schema.toLowerCase().includes(q)) return false
    switch (statusFilter.value) {
      case 'behind':
        return t.migration_status === 'behind'
      case 'drifted':
        return t.drifted
      case 'ok':
        return t.migration_status === 'up-to-date' && !t.drifted
      default:
        return true
    }
  })
})

function migrationBadge(t) {
  return t.migration_status === 'behind'
    ? { kind: 'warn', label: 'behind' }
    : { kind: 'ok', label: 'up-to-date' }
}
function driftBadge(t) {
  return t.drifted ? { kind: 'bad', label: 'drifted' } : { kind: 'ok', label: 'clean' }
}

function open(t) {
  router.push({ name: 'tenant', params: { schema: t.schema } })
}
</script>

<template>
  <div>
    <div class="toolbar">
      <input v-model="search" type="search" placeholder="Filter by schema name&hellip;" />
      <select v-model="statusFilter">
        <option value="all">All statuses</option>
        <option value="ok">Healthy (latest &amp; clean)</option>
        <option value="behind">Behind</option>
        <option value="drifted">Drifted</option>
      </select>
      <span class="muted">{{ filtered.length }} of {{ tenants.length }} tenants</span>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr>
            <th>Schema</th>
            <th>Version</th>
            <th>Migration</th>
            <th>Drift</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in filtered" :key="t.schema" @click="open(t)">
            <td class="schema">{{ t.schema }}</td>
            <td class="num">{{ t.version }}</td>
            <td><StatusBadge v-bind="migrationBadge(t)" /></td>
            <td><StatusBadge v-bind="driftBadge(t)" /></td>
          </tr>
        </tbody>
      </table>
      <div v-if="filtered.length === 0" class="empty">No tenants match the current filter.</div>
    </div>
  </div>
</template>
