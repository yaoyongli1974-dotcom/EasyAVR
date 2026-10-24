import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    chunkSizeWarningLimit: 700,
    rollupOptions: {
      output: {
        manualChunks: {
          'vendor-vue': ['vue', 'vue-router', 'pinia', 'axios'],
          'vendor-element': ['element-plus'],
          'vendor-map': ['leaflet'],
          'vendor-player': ['hls.js', 'mpegts.js'],
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:18000',
        changeOrigin: true,
      },
      // Snapshot images are served by the API origin outside /api.
      '/snapshots': {
        target: 'http://127.0.0.1:18000',
        changeOrigin: true,
      },
    },
  },
})
