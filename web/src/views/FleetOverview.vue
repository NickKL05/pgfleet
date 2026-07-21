<script setup>
import { onMounted, ref } from 'vue'
import { api } from '../api.js'
import SummaryCards from '../components/SummaryCards.vue'
import FleetTable from '../components/FleetTable.vue'
import VersionChart from '../components/VersionChart.vue'
import ErrorState from '../components/ErrorState.vue'

const summary = ref(null)
const tenants = ref([])
const versions = ref(null)
const loading = ref(true)
const error = ref(null)

async function load(refresh = false) {
  loading.value = true
  error.value = null
  try {
    const opts = { refresh }
    // One page load fans out to the three fleet endpoints; the server's short
    // cache collapses them into a single pass over the database.
    const [s, t, v] = await Promise.all([
      api.summary(opts),
      api.tenants(opts),
      api.versions(opts),
    ])
    summary.value = s
    tenants.value = t.tenants
    versions.value = v
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}

onMounted(() => load())
</script>

<template>
  <div>
    <div v-if="summary" class="toolbar">
      <button :disabled="loading" @click="load(true)">
        {{ loading ? 'Refreshing&hellip;' : 'Refresh' }}
      </button>
      <span class="muted">reference: {{ summary.reference }}</span>
    </div>

    <!-- Shown on its own when nothing has loaded yet, or above the last good
         data when a refresh fails, so a failed refresh never blanks the page. -->
    <ErrorState v-if="error" :error="error" :retrying="loading" @retry="load(true)" />

    <template v-if="summary">
      <SummaryCards :summary="summary" />

      <h2 class="section-title">Tenants per version</h2>
      <VersionChart
        v-if="versions && versions.versions.length > 1"
        :versions="versions.versions"
        :latest-version="versions.latest_version"
      />

      <h2 class="section-title">Fleet</h2>
      <FleetTable :tenants="tenants" />
    </template>

    <div v-else-if="loading && !error" class="empty">Loading fleet&hellip;</div>
  </div>
</template>
