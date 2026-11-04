import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  cacheDir: '/tmp/cycrio-browser-vite',
  publicDir: 'static',
  plugins: [svelte()],
  optimizeDeps: { entries: ['browser-tests/index.html'] },
  resolve: {
    alias: {
      '$app/env/public': '/app/browser-tests/env.ts',
      '$app/state': '/app/browser-tests/router.svelte.ts',
      '$app/navigation': '/app/browser-tests/router.svelte.ts',
    },
  },
  server: {
    port: 5199,
    strictPort: true,
    allowedHosts: ['fixture'],
    watch: { ignored: ['**/build/**', '**/.svelte-kit/**'] },
  },
});
