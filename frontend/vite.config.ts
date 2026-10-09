/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    host: '0.0.0.0',
    proxy: {
      '/api': {
        target: 'http://backend:4000',
        changeOrigin: true,
      },
    },
  },
  test: {
    // Only *.test.ts files; the existing *.test-d.ts files are compile-time
    // type checks run by vue-tsc, not Vitest suites.
    include: ['src/**/*.test.ts'],
  },
})
