import { defineConfig } from 'vitest/config';

// API server used by `pnpm dev` (a running `depgraph serve`).
const api = process.env.DEPGRAPH_API ?? 'http://127.0.0.1:7792';

export default defineConfig({
  // served by the Go server at /
  base: '/',
  build: {
    outDir: '../pkg/interfaces/handler/ui/dist',
    emptyOutDir: true,
    // one cacheable chunk for the pinned libraries (cytoscape, elkjs API);
    // the ELK worker is emitted as its own asset (src/layout.ts)
    rolldownOptions: {
      output: {
        codeSplitting: { groups: [{ name: 'vendor', test: /node_modules/ }] },
      },
    },
  },
  server: {
    proxy: {
      '/api': { target: api, changeOrigin: true },
      // SSE: keep the connection open and unbuffered
      '/events': { target: api, changeOrigin: true, timeout: 0, proxyTimeout: 0 },
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
});
