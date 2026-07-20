<script setup>
// A single colored status pill. `kind` picks the color: ok (green), warn
// (amber), bad (red).
defineProps({
  kind: { type: String, required: true },
  label: { type: String, required: true },
})
</script>

<template>
  <span class="badge" :class="kind">{{ label }}</span>
</template>
