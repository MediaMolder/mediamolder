import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: true,
    rollupOptions: {
      output: {
        manualChunks: (id) => {
          // React core — very stable; cache independently of app code.
          if (id.includes('node_modules/react/') || id.includes('node_modules/react-dom/')) {
            return 'react-vendor';
          }
          // Canvas + graph layout — change together, rarely.
          if (id.includes('node_modules/@xyflow/') || id.includes('node_modules/@dagrejs/')) {
            return 'xyflow';
          }
          // zustand is tiny (~3 kB minified); leave it in the main chunk.
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      // Forward API calls to the Go backend running with `mediamolder gui --dev`.
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
        ws: true,
      },
      // Proxy perf/metrics endpoints to the metrics server during local dev.
      '/perf': {
        target: 'http://localhost:9090',
        changeOrigin: true,
      },
      '/metrics': {
        target: 'http://localhost:9090',
        changeOrigin: true,
      },
      '/health': {
        target: 'http://localhost:9090',
        changeOrigin: true,
      },
    },
  },
});
