import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { resolve } from 'path';
import { writeFileSync, mkdirSync } from 'fs';

function hugoManifestPlugin() {
  return {
    name: 'hugo-manifest',
    writeBundle(
      _options: unknown,
      bundle: Record<string, { type: string; isEntry?: boolean; fileName: string }>
    ) {
      const manifest: Record<string, string> = {};
      for (const [fileName, chunk] of Object.entries(bundle)) {
        if (chunk.type === 'chunk' && chunk.isEntry) {
          manifest['main.js'] = 'app/' + fileName;
        }
        if (fileName.endsWith('.css')) {
          manifest['main.css'] = 'app/' + fileName;
        }
      }
      const outDir = resolve(__dirname, '../data');
      mkdirSync(outDir, { recursive: true });
      writeFileSync(resolve(outDir, 'app_manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
    },
  };
}

export default defineConfig(({ command }) => ({
  plugins: [react(), hugoManifestPlugin()],
  base: command === 'build' ? '/app/' : '/',
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  build: {
    outDir: resolve(__dirname, '../static/app'),
    emptyOutDir: true,
    manifest: false,
    sourcemap: true,
    rollupOptions: {
      input: resolve(__dirname, 'src/main.tsx'),
      output: {
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
        // Split large, stable vendors out of the entry chunk so the main
        // bundle stays under Vite's 500 kB warning threshold and these
        // rarely-changing libs cache independently across deploys.
        manualChunks: {
          react: ['react', 'react-dom', 'react/jsx-runtime'],
          'react-query': ['@tanstack/react-query'],
          analytics: ['posthog-js'],
        },
      },
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    cors: true,
    origin: "http://localhost:5173",
    proxy: {
      '/jobs-api': {
        target: 'https://api.stawi.org',
        changeOrigin: true,
        secure: true,
        rewrite: (path: string) => path.replace(/^\/jobs-api/, '/jobs'),
      },
      '/candidates-api': {
        target: 'https://api.stawi.org',
        changeOrigin: true,
        secure: true,
        rewrite: (path: string) => path.replace(/^\/candidates-api/, ''),
      },
    },
  },
}));