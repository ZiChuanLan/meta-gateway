import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  base: '/admin-ui/',
  plugins: [react()],
  build: { outDir: '../internal/webui/dist', emptyOutDir: true },
  server: {
    port: 4173,
    proxy: {
      '/admin/': 'http://127.0.0.1:4100',
      '/healthz': 'http://127.0.0.1:4100',
      '/readyz': 'http://127.0.0.1:4100',
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: true,
  },
})
