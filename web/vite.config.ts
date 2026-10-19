/// <reference types="vitest/config" />
import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { brotliCompressSync, constants, gzipSync } from 'node:zlib';

import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

/**
 * Write .br and .gz copies of the built text assets next to them. The Go
 * server sends whichever the browser accepts, so pages arrive 70-80% smaller
 * over Wi-Fi while the Raspberry Pi spends no CPU compressing anything: the
 * work happens once, here, at maximum quality.
 */
function precompress(): Plugin {
  const compressible = /\.(?:js|mjs|css|html|svg|json|map|txt|wasm)$/;
  const walk = (dir: string): string[] =>
    readdirSync(dir).flatMap((name) => {
      const path = join(dir, name);
      return statSync(path).isDirectory() ? walk(path) : [path];
    });
  return {
    name: 'orchard-precompress',
    apply: 'build',
    closeBundle() {
      for (const file of walk('dist')) {
        if (!compressible.test(file)) continue;
        const raw = readFileSync(file);
        if (raw.length < 1024) continue;
        const br = brotliCompressSync(raw, {
          params: {
            [constants.BROTLI_PARAM_QUALITY]: constants.BROTLI_MAX_QUALITY,
            [constants.BROTLI_PARAM_SIZE_HINT]: raw.length,
          },
        });
        const gz = gzipSync(raw, { level: 9 });
        if (br.length < raw.length * 0.9) writeFileSync(`${file}.br`, br);
        if (gz.length < raw.length * 0.9) writeFileSync(`${file}.gz`, gz);
      }
    },
  };
}

// In development the Vite dev server proxies API traffic to the Go backend,
// so the frontend uses same-origin /api/v1 URLs in every environment.
const apiTarget = process.env.ORCHARD_API_TARGET ?? 'http://127.0.0.1:8090';

export default defineConfig({
  plugins: [react(), precompress()],
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
