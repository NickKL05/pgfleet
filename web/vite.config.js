import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// The production build is written straight into the Go package that embeds it
// (internal/web/dist), so `go build` picks up a fresh bundle with no copy step.
// base: '/' (absolute asset paths) is required: the binary serves the SPA from
// the domain root with a client-side router, and a relative base would resolve
// /assets/* against a deep-link path (e.g. /tenant/x -> /tenant/assets/...) and
// fail to load on refresh or direct navigation.
export default defineConfig({
  plugins: [vue()],
  base: '/',
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    // `npm run dev` serves the UI on :5173 and proxies the API to a running
    // `pgfleet web` on :8080, so the frontend can be iterated with hot reload.
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
