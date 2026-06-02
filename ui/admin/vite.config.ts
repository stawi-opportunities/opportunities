import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { resolve } from 'path';
import { writeFileSync, mkdirSync } from 'fs';

// Mirrors ui/app/vite.config.ts. Writes a manifest consumed by the admin
// Hugo layout so it can reference the hashed bundle filenames emitted by
// rollup. The admin app gets its own manifest (admin_manifest.json) so the
// candidate app's manifest is not overwritten when both are rebuilt.
function hugoManifestPlugin() {
  return {
    name: 'hugo-admin-manifest',
    writeBundle(
      _options: unknown,
      bundle: Record<string, { type: string; isEntry?: boolean; fileName: string }>
    ) {
      const manifest: Record<string, string> = {};
      // Vite emits to ui/static/admin-app/assets/<file>; Hugo copies those
      // into ui/public/admin-app/assets/<file> and serves at the matching
      // /admin-app/assets/<file> URL. Record the /admin-app/-prefixed path so
      // the layout can drop it straight into a <script src="/…"> tag.
      for (const [fileName, chunk] of Object.entries(bundle)) {
        if (chunk.type === 'chunk' && chunk.isEntry) {
          manifest['main.js'] = 'admin-app/' + fileName;
        }
        if (fileName.endsWith('.css')) {
          manifest['main.css'] = 'admin-app/' + fileName;
        }
      }
      const outDir = resolve(__dirname, '../data');
      mkdirSync(outDir, { recursive: true });
      writeFileSync(
        resolve(outDir, 'admin_manifest.json'),
        JSON.stringify(manifest, null, 2) + '\n'
      );
    },
  };
}

export default defineConfig(({ command }) => ({
  plugins: [react(), hugoManifestPlugin()],
  // Production builds emit under /admin-app/ because Hugo copies
  // static/admin-app/ to public/admin-app/. In dev mode the Hugo template
  // loads directly from the Vite dev server origin, so no base prefix is
  // needed.
  base: command === 'build' ? '/admin-app/' : '/',
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  build: {
    outDir: resolve(__dirname, '../static/admin-app'),
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
    port: 5174,
    strictPort: true,
    cors: true,
    origin: 'http://localhost:5174',
  },
}));
