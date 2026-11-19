export type PushBrowser = {
  currentPermission: () => NotificationPermission | 'unsupported';
  availability: () => 'supported' | 'unsupported' | 'home_screen';
  prepare: (appId: string) => Promise<void>;
  permission: () => Promise<NotificationPermission>;
  subscription: () => Promise<string>;
  displayedProof: (challengeID: string) => Promise<unknown>;
  listen: (fn: (data: unknown) => void) => () => void;
};
// 読込が実行前に確定失敗した場合だけ再試行できる。
export class PushSDKLoadError extends Error {}
export type OneSignalSDK = {
  init: (options: Record<string, unknown>) => Promise<void>;
  Notifications: {
    permission: boolean;
    requestPermission: () => Promise<void>;
    isPushSupported: () => boolean;
    addEventListener: (name: string, fn: (event: any) => void) => void;
  };
  User: {
    PushSubscription: {
      id: string | null;
      optedIn: boolean;
      optIn: () => Promise<void>;
      addEventListener: (name: string, fn: () => void) => void;
      removeEventListener: (name: string, fn: () => void) => void;
    };
  };
};
type PushWindow = Window & {
  OneSignalDeferred?: { push: (fn: (sdk: OneSignalSDK) => void) => unknown };
};
const workerPath = '/OneSignalSDKWorker.js';
export function createPushBrowser(env: {
  window: Window;
  document: Document;
  load?: () => Promise<OneSignalSDK>;
}): PushBrowser {
  let app: string | undefined,
    prepared: Promise<void> | undefined,
    sdk: OneSignalSDK | undefined;
  const listeners = new Set<(data: unknown) => void>();
  function isPushWorker(source: MessageEventSource | null) {
    if (!source || !('scriptURL' in source)) return false;
    const url = new URL(source.scriptURL);
    return (
      url.origin === env.window.location.origin && url.pathname === workerPath
    );
  }
  function relayWorkerProof(event: MessageEvent) {
    if (event.data?.type === 'cycrio.push.proof' && isPushWorker(event.source))
      listeners.forEach((fn) => fn(event.data.proof));
  }
  async function rootRegistration() {
    const registration =
      await env.window.navigator?.serviceWorker?.getRegistration?.('/');
    return isPushWorker(registration?.active ?? null)
      ? registration
      : undefined;
  }
  const load =
    env.load ??
    (() =>
      new Promise<OneSignalSDK>((resolve, reject) => {
        const w = env.window as PushWindow;
        let settled = false;
        const timer = env.window.setTimeout(() => {
          settled = true;
          reject(Error('reload'));
        }, 20000);
        w.OneSignalDeferred ??= [] as ((sdk: OneSignalSDK) => void)[];
        w.OneSignalDeferred.push((loaded) => {
          if (settled) return;
          settled = true;
          env.window.clearTimeout(timer);
          resolve(loaded);
        });
        const script = env.document.createElement('script');
        script.src = '/vendor/onesignal/160800/OneSignalSDK.page.es6.js';
        script.defer = true;
        script.onerror = () => {
          if (settled) return;
          settled = true;
          env.window.clearTimeout(timer);
          script.remove();
          reject(new PushSDKLoadError('sdk unavailable'));
        };
        env.document.head.append(script);
      }));
  return {
    currentPermission() {
      return (
        (env.window as Window & { Notification?: typeof Notification })
          .Notification?.permission ?? 'unsupported'
      );
    },
    availability() {
      const w = env.window;
      const ios =
        /iPad|iPhone|iPod/.test(w.navigator.userAgent) ||
        (w.navigator.platform === 'MacIntel' && w.navigator.maxTouchPoints > 1);
      if (
        ios &&
        !w.matchMedia('(display-mode: standalone)').matches &&
        !(w.navigator as Navigator & { standalone?: boolean }).standalone
      )
        return 'home_screen';
      return w.isSecureContext &&
        'Notification' in w &&
        'serviceWorker' in w.navigator &&
        'PushManager' in w
        ? 'supported'
        : 'unsupported';
    },
    prepare(appId) {
      if (app && app !== appId) return Promise.reject(Error('reload'));
      if (prepared) return prepared;
      app = appId;
      prepared = (async () => {
        sdk = await load();
        await sdk.init({
          appId,
          serviceWorkerPath: workerPath,
          serviceWorkerParam: { scope: '/' },
          autoResubscribe: false,
          notifyButton: { enable: false },
          welcomeNotification: { disable: true },
          promptOptions: {
            native: { enabled: false, autoPrompt: false },
            slidedown: { prompts: [{ type: 'push', autoPrompt: false }] },
          },
        });
        env.window.navigator?.serviceWorker?.addEventListener?.(
          'message',
          relayWorkerProof,
        );
        for (const name of ['foregroundWillDisplay', 'click'])
          sdk.Notifications.addEventListener(name, (event) => {
            const data = event?.notification?.additionalData;
            if (data?.cycrio_challenge_id) {
              if (name === 'foregroundWillDisplay') event.preventDefault?.();
              listeners.forEach((fn) => fn(data));
            }
          });
        // 既存の購読でも、次の受信確認に備えてルートworkerを更新する。
        try {
          await (await rootRegistration())?.update();
        } catch {
          // 更新できないときも、起動済みSDKの受信イベントを使える。
        }
      })().catch((error) => {
        if (!sdk && error instanceof PushSDKLoadError) {
          prepared = undefined;
          app = undefined;
          throw error;
        }
        throw Error('reload');
      });
      return prepared;
    },
    async permission() {
      if (!sdk) throw Error('prepare required');
      if (!sdk.Notifications.isPushSupported()) throw Error('unsupported');
      await sdk.Notifications.requestPermission();
      return (env.window as Window & { Notification: typeof Notification })
        .Notification.permission;
    },
    async subscription() {
      if (!sdk) throw Error('prepare required');
      const subscription = sdk.User.PushSubscription;
      if (!subscription.optedIn) await subscription.optIn();
      if (subscription.id) return subscription.id;
      return new Promise<string>((resolve, reject) => {
        const changed = () => {
          if (subscription.id) {
            env.window.clearTimeout(timer);
            subscription.removeEventListener('change', changed);
            resolve(subscription.id);
          }
        };
        const timer = env.window.setTimeout(() => {
          subscription.removeEventListener('change', changed);
          reject(Error('subscription timeout'));
        }, 20000);
        subscription.addEventListener('change', changed);
        changed();
      });
    },
    async displayedProof(challengeID) {
      try {
        const registration = await rootRegistration();
        const notifications = await registration?.getNotifications();
        return notifications?.find(
          ({ data }) =>
            data?.launchURL ===
              env.window.location.origin + '/settings/notifications' &&
            data.additionalData?.cycrio_challenge_id === challengeID,
        )?.data.additionalData;
      } catch {
        return undefined;
      }
    },
    listen(fn) {
      listeners.add(fn);
      return () => {
        listeners.delete(fn);
      };
    },
  };
}
