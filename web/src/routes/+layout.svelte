<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import '#lib/brand.css';
  import '#lib/app.css';
  import AppShell from '#lib/AppShell.svelte';
  import AppAccess from '#lib/AppAccess.svelte';
  import { createAuthController } from '#lib/auth.ts';
  import { createSessionBoundary } from '#lib/app-session.ts';
  import { provideAppSession } from '#lib/app-context.ts';
  import { createThemeController, type ThemePreference } from '#lib/theme.ts';
  import { createAPI } from '#lib/api.ts';
  import { createCatalogAPI } from '#lib/catalog-api.ts';
  import { createAIRuntimeAPI } from '#lib/airuntime-api.ts';
  import { createMailAPI } from '#lib/mail-api.ts';
  import { createMailSyncAPI } from '#lib/mail-sync-api.ts';
  import { createMailProcessingAPI } from '#lib/mail-processing-api.ts';

  let { children } = $props();
  let controller: ReturnType<typeof createAuthController> | undefined;
  let themeController: ReturnType<typeof createThemeController> | undefined;
  const boundary = createSessionBoundary(
    async () => (await controller?.getToken()) ?? null,
  );
  let sessionState = $state(boundary.current());
  let theme = $state<ThemePreference>('system');
  let signUp = $derived(page.url.searchParams.get('sign_up') === 'true');
  const api = createAPI(PUBLIC_API_BASE_URL);
  provideAppSession({
    get user() {
      return sessionState.auth.status === 'allowed'
        ? sessionState.auth.user
        : null;
    },
    get scope() {
      return sessionState.scope;
    },
    get token() {
      return sessionState.token;
    },
    get theme() {
      return theme;
    },
    setTheme(preference) {
      themeController?.set(preference);
      theme = preference;
    },
    onDenied() {
      void controller?.refresh();
    },
    api,
    catalogAPI: createCatalogAPI(PUBLIC_API_BASE_URL),
    runtimeAPI: createAIRuntimeAPI(PUBLIC_API_BASE_URL),
    mailAPI: createMailAPI(PUBLIC_API_BASE_URL),
    mailSyncAPI: createMailSyncAPI(PUBLIC_API_BASE_URL),
    mailProcessingAPI: createMailProcessingAPI(PUBLIC_API_BASE_URL),
  });
  onMount(() => {
    // Keep all browser access here so the layout also remains SSR safe.
    let storage: Pick<Storage, 'getItem' | 'setItem'>;
    try {
      storage = window.localStorage;
    } catch {
      storage = { getItem: () => null, setItem: () => {} };
    }
    themeController = createThemeController({
      root: document.documentElement,
      storage,
      media: window.matchMedia('(prefers-color-scheme: dark)'),
    });
    theme = themeController.preference();
    controller = createAuthController(
      api,
      page.url.pathname === '/sso-callback' ? 'callback' : 'home',
      (auth) => {
        boundary.update(auth);
        sessionState = boundary.current();
      },
    );
    void controller.start();
    return () => {
      controller?.dispose();
      themeController?.dispose();
    };
  });
</script>

{#if sessionState.auth.status === 'allowed'}
  {#key sessionState.scope}
    <AppShell
      user={sessionState.auth.user}
      pathname={page.url.pathname}
      signOut={() => {
        void controller?.signOut();
      }}
    >
      {@render children()}
    </AppShell>
  {/key}
{:else}
  <AppAccess
    state={sessionState.auth}
    {signUp}
    retry={() => {
      void controller?.start();
    }}
    signOut={() => {
      void controller?.signOut();
    }}
    mountAuth={(node, signup) =>
      controller?.mountAuth(node, signup) ?? (() => {})}
  />
{/if}
