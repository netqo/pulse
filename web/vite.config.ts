import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The API base the dev server proxies to. The browser always talks to the Vite
// origin under /api, so no CORS setup is needed in development; the production
// build is served by the API itself, where the same relative /api paths resolve
// unchanged.
const API_TARGET = process.env.PULSE_API_URL ?? 'http://localhost:8081';

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: API_TARGET,
        changeOrigin: true,
      },
    },
  },
  build: {
    // Monaco's editor core is a large, deliberate dependency; isolate it in its
    // own long-lived vendor chunk so app changes do not bust its cache, and lift
    // the size warning past it (the app chunk stays small).
    chunkSizeWarningLimit: 2800,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('monaco-editor')) {
            return 'monaco';
          }
          if (id.includes('echarts') || id.includes('zrender')) {
            return 'echarts';
          }
          return undefined;
        },
      },
    },
  },
});
