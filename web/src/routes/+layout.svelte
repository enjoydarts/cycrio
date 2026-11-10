<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { afterNavigate } from '$app/navigation';
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
  import { createAIRoutesAPI } from '#lib/ai-routes-api.ts';
  import { createMailAPI } from '#lib/mail-api.ts';
  import { createMailSyncAPI } from '#lib/mail-sync-api.ts';
  import { createMailProcessingAPI } from '#lib/mail-processing-api.ts';
  import { createOverviewAPI } from '#lib/overview-api.ts';
  import { createNotificationsAPI } from '#lib/notifications-api.ts';
  import { createPushAPI } from '#lib/push-api.ts';
  import { createPushBrowser } from '#lib/push-browser.ts';
  import {
    createPushController,
    type PushController,
  } from '#lib/push-controller.ts';
  import { startBrowserTelemetry } from '#lib/telemetry-browser.ts';

  let { children } = $props();
  let controller: ReturnType<typeof createAuthController> | undefined;
  let browserTelemetry: ReturnType<typeof startBrowserTelemetry> | undefined;
  let themeController: ReturnType<typeof createThemeController> | undefined;
  let pushController = $state<PushController | undefined>();
  const boundary = createSessionBoundary(
    async () => (await controller?.getToken()) ?? null,
  );
  let sessionState = $state(boundary.current());
  let theme = $state<ThemePreference>('system');
  let signUp = $derived(page.url.searchParams.get('sign_up') === 'true');
  const api = createAPI(PUBLIC_API_BASE_URL);
  const mailAPI = createMailAPI(PUBLIC_API_BASE_URL);
  const overviewAPI = createOverviewAPI(PUBLIC_API_BASE_URL);
  const notificationsAPI = createNotificationsAPI(PUBLIC_API_BASE_URL);
  function setTheme(preference: ThemePreference) {
    themeController?.set(preference);
    theme = preference;
  }
  function onDenied() {
    void pushController?.bind(null);
    void controller?.refresh();
  }
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
    setTheme,
    onDenied,
    signOut: () => controller?.signOut(),
    api,
    catalogAPI: createCatalogAPI(PUBLIC_API_BASE_URL),
    runtimeAPI: createAIRuntimeAPI(PUBLIC_API_BASE_URL),
    routesAPI: createAIRoutesAPI(PUBLIC_API_BASE_URL),
    mailAPI,
    mailSyncAPI: createMailSyncAPI(PUBLIC_API_BASE_URL),
    mailProcessingAPI: createMailProcessingAPI(PUBLIC_API_BASE_URL),
    notificationsAPI,
    get pushController() {
      return pushController;
    },
  });
  afterNavigate(({ to }) => {
    if (to) browserTelemetry?.navigate(to.url.pathname);
  });
  onMount(() => {
    // Keep all browser access here so the layout also remains SSR safe.
    const telemetry = startBrowserTelemetry(PUBLIC_API_BASE_URL);
    browserTelemetry = telemetry;
    telemetry.navigate(page.url.pathname);
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
    try {
      pushController = createPushController({
        api: createPushAPI(PUBLIC_API_BASE_URL),
        sdk: createPushBrowser({ window, document }),
        storage: window.localStorage,
        denied: () => {
          void controller?.refresh();
        },
      });
    } catch {
      // 解除情報を永続化できない環境では端末登録を開始しない。
    }
    const storageChanged = (event: StorageEvent) => {
      if (event.key?.startsWith('cycrio.push.'))
        void pushController?.synchronize();
    };
    const retryPushCleanup = () => {
      void pushController?.retryCleanup();
    };
    window.addEventListener('storage', storageChanged);
    window.addEventListener('online', retryPushCleanup);
    controller = createAuthController(
      api,
      page.url.pathname === '/sso-callback' ? 'callback' : 'home',
      (auth) => {
        boundary.update(auth);
        sessionState = boundary.current();
        telemetry.setUser(auth.status === 'allowed' ? auth.user.id : null);
        if (auth.status === 'allowed')
          void pushController?.bind(auth.user.id, sessionState.token);
        else if (
          ['signed_out', 'denied', 'pending', 'unconfigured'].includes(
            auth.status,
          )
        )
          void pushController?.bind(null);
      },
      undefined,
      () => pushController?.bind(null),
    );
    void controller.start();
    return () => {
      controller?.dispose();
      telemetry.dispose();
      if (browserTelemetry === telemetry) browserTelemetry = undefined;
      themeController?.dispose();
      window.removeEventListener('storage', storageChanged);
      window.removeEventListener('online', retryPushCleanup);
    };
  });
</script>

{#if sessionState.auth.status === 'allowed'}
  {#key sessionState.scope}
    <AppShell
      user={sessionState.auth.user}
      pathname={page.url.pathname}
      search={page.url.search}
      token={sessionState.token}
      {mailAPI}
      {overviewAPI}
      {notificationsAPI}
      {pushController}
      {onDenied}
      {theme}
      {setTheme}
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
