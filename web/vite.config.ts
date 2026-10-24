import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [sveltekit({ adapter: adapter({ fallback: 'index.html' }) })],
  build: {
    // Clerk ships a prebuilt ~1.5 MiB SDK, loaded only after auth is configured.
    chunkSizeWarningLimit: 1600,
    rolldownOptions: {
      // SvelteKit replaces onwarn during the client build; onLog preserves it.
      onLog(level, warning, log) {
        // React's server/client marker has no effect in this static Svelte SPA.
        if (
          level === 'warn' &&
          warning.code === 'MODULE_LEVEL_DIRECTIVE' &&
          warning.id?.includes('/node_modules/@clerk/ui/') &&
          warning.message.includes('use client')
        )
          return;
        log(level, warning);
      },
    },
  },
});
