import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { resolve } from 'path';
import { writeFileSync, mkdirSync } from 'fs';

// Writes a tiny manifest consumed by Hugo's head.html so the layout can
// reference the hashed bundle filenames emitted by rollup.
function hugoManifestPlugin() {
  return {
    name: 'hugo-manifest',
    writeBundle(
      _options: unknown,
      bundle: Record<string, { type: string; isEntry?: boolean; fileName: string }>
    ) {
      const manifest: Record<string, string> = {};
      // Vite emits to ui/static/app/assets/<file>, which Hugo then copies
      // into ui/public/app/assets/<file> and serves at /app/assets/<file>.
      // We record the /app/-prefixed path so head.html can drop it directly
      // into a <script src="/…"> tag without extra logic.
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
  // Production builds emit under /app/ because Hugo copies static/app/
  // to public/app/. In dev mode the Hugo template loads directly from
  // the Vite dev server origin, so no base prefix is needed — keeping
  // base at "/" avoids the mismatch where head.html requests
  // /src/main.tsx but Vite serves /app/src/main.tsx.
  base: command === 'build' ? '/app/' : '/',
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  build: {
    // Emit into Hugo's static/ tree so the build output is served verbatim.
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
      },
    },
  },
  server: {
    // Hugo dev server lives on 5170; Vite on 5173. head.html detects
    // hugo.IsServer and points <script> at the Vite dev server directly.
    port: 5173,
    strictPort: true,
    cors: true,
    origin: 'http://localhost:5173',
  },
}));
