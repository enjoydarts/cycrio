import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [
    sveltekit({ adapter: adapter({ fallback: 'index.html' }) }),
    {
      name: 'cycrio-private-source-map-protocol',
      apply: 'build',
      // Start a new immutable-cache namespace when adding post-build chunk IDs.
      // Bump this salt whenever the pinned injector changes its output protocol.
      augmentChunkHash: () => 'cycrio-posthog-cli-0.19.0-v1',
    },
  ],
  build: {
    sourcemap: 'hidden',
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
