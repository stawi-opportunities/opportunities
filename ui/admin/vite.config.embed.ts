import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { resolve } from 'path';

// Standalone SPA build for embedding in the Go api binary via
// //go:embed (apps/api/cmd/admin_ui.go). Emits dist/ with index.html
// + assets at base /admin/. The default vite.config.ts stays in
// place for the Hugo-integrated build path (writes to
// ../static/admin-app/ with manifest); this config does NOT use
// the Hugo manifest plugin or the bare-tsx entrypoint — it builds
// a self-contained SPA the api can serve directly.
export default defineConfig({
  plugins: [react()],
  base: '/admin/',
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  build: {
    outDir: resolve(__dirname, 'dist'),
    emptyOutDir: true,
    sourcemap: false,
  },
});
