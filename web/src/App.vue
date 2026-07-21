<script setup>
import { RouterView, RouterLink } from 'vue-router'
</script>

<template>
  <div class="app-shell">
    <header class="app-header">
      <h1><RouterLink to="/"><span class="brand-mark">pgfleet</span></RouterLink></h1>
      <span class="subtitle">read-only fleet dashboard: migration status &amp; schema drift</span>
    </header>
    <RouterView />
  </div>
</template>
