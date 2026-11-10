<script lang="ts">
  import NotificationSettings from '../src/lib/NotificationSettings.svelte';
  import SettingsOverview from '../src/lib/SettingsOverview.svelte';
  import {
    provideAppSession,
    type AppSession,
  } from '../src/lib/app-context.ts';
  import { createPushAPI, deriveRevocationToken } from '../src/lib/push-api.ts';
  import { createPushController } from '../src/lib/push-controller.ts';
  import { createNotificationsAPI } from '../src/lib/notifications-api.ts';
  import { notificationSettings } from '../src/lib/notifications-fixtures.ts';
  let { mode }: { mode: string } = $props();
  const app = '11111111-1111-4111-8111-111111111111',
    deviceID = '22222222-2222-4222-8222-222222222222',
    challenge = '33333333-3333-4333-8333-333333333333',
    secret = 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA';
  for (const key of Object.keys(localStorage))
    if (key.startsWith('cycrio.push.')) localStorage.removeItem(key);
  const trace = window.browserTest;
  trace.pushCalls = [];
  trace.pushActive = false;
  let receive: (data: unknown) => void = () => {};
  let deviceState: 'pending' | 'active' | 'disabled' | 'expired' | null = null;
  let nativePermission: NotificationPermission = 'granted';
  let clock = Date.now(),
    preparations = 0;
  let finish: () => void = () => {};
  let subscriptions = 0;
  const device = () => ({
    id: deviceID,
    state: deviceState,
    created_at: '2026-11-10T00:00:00Z',
    updated_at: '2026-11-10T00:00:00Z',
  });
  const emit = () =>
    receive({
      cycrio_challenge_id: challenge,
      cycrio_challenge_secret: secret,
    });
  const controller = createPushController({
    now: () => clock,
    storage: localStorage,
    sdk: {
      currentPermission: () => nativePermission,
      availability: () =>
        mode === 'push-home'
          ? 'home_screen'
          : mode === 'push-unsupported'
            ? 'unsupported'
            : 'supported',
      prepare: async () => {
        trace.pushCalls!.push('prepare');
        if (mode === 'push-sdk-retry' && ++preparations === 1)
          throw Error('sdk unavailable');
        if (mode === 'push-sdk-reload') throw Error('reload');
      },
      permission: async () => {
        trace.pushCalls!.push('permission');
        return mode === 'push-denied' ? 'denied' : 'granted';
      },
      subscription: async () => {
        if (mode === 'push-subscription-retry' && ++subscriptions === 1)
          throw Error('合成の購読取得失敗');
        return deviceID;
      },
      listen: (cb) => {
        receive = cb;
        return () => {};
      },
    },
    api: createPushAPI('/synthetic-push', async (url, init) => {
      const path = new URL(String(url), location.origin).pathname;
      if (
        path === '/synthetic-push/v1/push-devices' &&
        (!init?.method || init.method === 'GET')
      )
        return Response.json({
          app_id: app,
          available: true,
          reason: '',
          devices: deviceState ? [device()] : [],
        });
      if (
        path === '/synthetic-push/v1/push-devices' &&
        init?.method === 'POST'
      ) {
        trace.pushCalls!.push('register');
        deviceState = 'pending';
        if (!['push-late', 'push-expired'].includes(mode)) emit();
        return Response.json({
          device_id: deviceID,
          challenge_id: challenge,
          state: 'pending',
          expires_at: new Date(clock + 300000).toISOString(),
        });
      }
      if (path === '/synthetic-push/v1/push-devices/confirm') {
        trace.pushCalls!.push('confirm');
        const token = await deriveRevocationToken(secret);
        trace.pushStoredBeforeConfirm = Object.keys(localStorage).some(
          (key) =>
            key.startsWith('cycrio.push.receipt.') &&
            JSON.parse(localStorage.getItem(key)!).revocation_token === token,
        );
        const success = () => {
          deviceState = 'active';
          trace.pushActive = true;
          return Response.json({ device: device(), revocation_token: token });
        };
        return mode === 'push-late'
          ? new Promise((resolve) => {
              finish = () => resolve(success());
            })
          : success();
      }
      if (
        path === '/synthetic-push/v1/push-device-revocations' ||
        init?.method === 'DELETE'
      ) {
        trace.pushCalls!.push('revoke');
        deviceState = 'disabled';
        trace.pushActive = false;
        return new Response(null, { status: 204 });
      }
      throw Error('未知の合成Push API');
    }),
  });
  let prefs = notificationSettings().preferences;
  const notificationsAPI = createNotificationsAPI(
    '/synthetic-notifications',
    async (url, init) => {
      if (init?.method === 'PUT') {
        const input = JSON.parse(String(init.body));
        prefs = { ...input, version: input.expected_version + 1 };
      }
      return Response.json({
        ...notificationSettings(),
        preferences: prefs,
        email: {
          available: true,
          reason: '',
          profile_ready: true,
          sender_ready: true,
        },
        push: { available: true, reason: '' },
      });
    },
  );
  let settings = $state(false),
    shown = $state(true);
  void controller.bind(app, async () => 'synthetic');
  trace.pushEmit = emit;
  trace.pushFinishConfirm = () => finish();
  trace.pushLogout = () => controller.bind(null);
  trace.pushRefreshToken = () => controller.bind(app, async () => 'new-token');
  trace.pushSetPermission = async (permission) => {
    nativePermission = permission;
    await controller.synchronize();
  };
  trace.pushExpire = () => {
    clock += 300000;
    deviceState = 'expired';
  };
  trace.pushToggleSettings = () => {
    shown = !shown;
  };
  provideAppSession({
    user: { id: app, role: 'user' },
    scope: 1,
    token: async () => 'synthetic',
    notificationsAPI,
    pushController: controller,
    onDenied: () => {
      throw Error('合成権限拒否');
    },
  } as AppSession);
</script>

<svelte:window
  onclick={(event) => {
    const anchor = (event.target as HTMLElement).closest?.('a');
    if (anchor?.getAttribute('href') === '/settings/notifications') {
      event.preventDefault();
      settings = true;
    }
  }}
/>
<main>
  {#if settings}{#if shown}<NotificationSettings
        api={notificationsAPI}
        token={async () => 'synthetic'}
        onDenied={() => {
          throw Error('合成権限拒否');
        }}
        pushController={controller}
      />{/if}{:else}<SettingsOverview />{/if}
</main>

<style>
  main {
    max-width: 1100px;
    margin: auto;
    padding: 24px;
    min-width: 0;
  }
  @media (max-width: 550px) {
    main {
      padding: 12px;
    }
  }
</style>
