import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  server: {
    // During `npm run dev`, forward the API and SSE stream to the running daemon.
    proxy: { '/api': 'http://127.0.0.1:8081' },
  },
  build: {
    outDir: '../internal/api/dist',
    emptyOutDir: true,
    target: 'es2022',
  },
})
