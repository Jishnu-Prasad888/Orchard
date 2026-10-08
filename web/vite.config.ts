/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// In development the Vite dev server proxies API traffic to the Go backend,
// so the frontend uses same-origin /api/v1 URLs in every environment.
const apiTarget = process.env.CAIRN_API_TARGET ?? 'http://127.0.0.1:8715';

export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 5173,
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    // Comfortably above the 5s `asyncUtilTimeout` the setup file configures, so
    // a genuinely missing element is reported by the assertion that wanted it
    // rather than by a bare test timeout.
    testTimeout: 15_000,
  },
});
