import { svelte } from '@sveltejs/vite-plugin-svelte'
import { defineConfig } from 'vite'

// Dev: `npm run dev` with a netchat service running; API calls are proxied.
// Build: output is embedded into the Go binary (internal/webui).
const api = process.env.NETCHAT_API || '127.0.0.1:47801'

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/v1': { target: `http://${api}`, changeOrigin: true },
    },
  },
})
