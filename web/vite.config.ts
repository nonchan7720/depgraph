import { defineConfig, type Plugin } from 'vitest/config';
import { inlineHtml } from './src/lib/inline.ts';

// API server used by `pnpm dev` (a running `depgraph serve`).
const api = process.env.DEPGRAPH_API ?? 'http://127.0.0.1:7792';

// `vite build --mode mcp-app`: one self-contained HTML for the MCP App
// (pkg/interfaces/handler/mcpapp), started from src/mcp-app.ts instead of src/main.ts.
function singleFile(): Plugin {
  return {
    name: 'depgraph-single-file',
    enforce: 'post',
    transformIndexHtml: {
      order: 'pre',
      handler: (html) => {
        const out = html.replace('/src/main.ts', '/src/mcp-app.ts');
        if (out === html) throw new Error('single-file: index.html does not load /src/main.ts');
        return out;
      },
    },
    generateBundle(_, bundle) {
      const html = bundle['index.html'];
      if (!html || html.type !== 'asset') throw new Error('single-file: index.html missing from the bundle');
      const files: Record<string, string> = {};
      for (const [name, out] of Object.entries(bundle)) {
        if (out.type === 'chunk') files[name] = out.code;
        else if (name.endsWith('.css')) files[name] = String(out.source);
      }
      html.source = inlineHtml(String(html.source), files);
      for (const name of Object.keys(files)) delete bundle[name];
    },
  };
}

export default defineConfig(({ mode }) => {
  const app = mode === 'mcp-app';
  return {
    // served by the Go server at /
    base: '/',
    plugins: app ? [singleFile()] : [],
    build: app
      ? {
          outDir: '../pkg/interfaces/handler/mcpapp/dist',
          emptyOutDir: true,
          assetsInlineLimit: Number.MAX_SAFE_INTEGER,
          cssCodeSplit: false,
          rolldownOptions: { output: { codeSplitting: false } },
        }
      : {
          outDir: '../pkg/interfaces/handler/ui/dist',
          emptyOutDir: true,
          // one cacheable chunk for the pinned libraries (cytoscape, elkjs API);
          // the ELK worker is emitted as its own asset (src/host/http.ts)
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
  };
});
