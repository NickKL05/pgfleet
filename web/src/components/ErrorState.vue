<script setup>
// A calm, recoverable error panel. It leads with plain language, keeps the
// technical cause secondary, and always offers a retry — a transient backend
// hiccup should read as "try again", not "this app is broken".
defineProps({
  error: { type: Object, required: true }, // ApiError (or any Error)
  retrying: { type: Boolean, default: false },
})
defineEmits(['retry'])
</script>

<template>
  <div class="error-state">
    <div class="error-headline">{{ error.message }}</div>
    <p v-if="error.detail" class="error-detail">{{ error.detail }}</p>
    <button :disabled="retrying" @click="$emit('retry')">
      {{ retrying ? 'Retrying…' : 'Try again' }}
    </button>
  </div>
</template>
