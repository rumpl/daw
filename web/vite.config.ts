import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// The dev server proxies /api to the Go server so `make dev` behaves exactly
// like production (same origin, same CSRF flow).
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': new URL('./src', import.meta.url).pathname },
  },
  build: {
    // Built straight into the Go embed directory: `make build` produces a
    // single binary with no loose-file dependency.
    outDir: '../internal/webassets/dist',
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    host: '127.0.0.1',
    port: 4789,
    strictPort: true,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:4788',
        changeOrigin: false,
      },
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
});
