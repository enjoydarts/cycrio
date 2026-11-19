import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createPushBrowser,
  PushSDKLoadError,
  type OneSignalSDK,
} from './push-browser.ts';
test('native permission読取はSDKも許可要求も実行しない', () => {
  const b = createPushBrowser({
    window: { Notification: { permission: 'denied' } } as any,
    document: {} as Document,
    load: async () => {
      throw Error('読込禁止');
    },
  });
  assert.equal(b.currentPermission(), 'denied');
});
test('SDK未読込の確定失敗は再試行し初期化失敗はreloadを要求する', async () => {
  let loads = 0,
    inits = 0;
  const b = createPushBrowser({
    window: {} as Window,
    document: {} as Document,
    load: async () => {
      loads++;
      if (loads === 1) throw new PushSDKLoadError('sdk unavailable');
      return {
        init: async () => {
          inits++;
          throw Error('init uncertain');
        },
      } as any;
    },
  });
  await assert.rejects(b.prepare('a'), /sdk unavailable/);
  await assert.rejects(b.prepare('a'), /reload/);
  await assert.rejects(b.prepare('a'), /reload/);
  assert.equal(loads, 2);
  assert.equal(inits, 1);
});
test('SDK準備は本人操作まで0回でApp IDごとに一度・自動promptを無効化する', async () => {
  let loads = 0,
    permissions = 0;
  const options: any[] = [],
    events = new Map<string, (v: unknown) => void>();
  const sdk: any = {
    init: async (o: any) => {
      options.push(o);
    },
    Notifications: {
      isPushSupported: () => true,
      requestPermission: async () => {
        permissions++;
      },
      addEventListener: (name: string, fn: any) => events.set(name, fn),
    },
    User: { PushSubscription: { id: 'id', optedIn: true } },
  };
  const window: any = {
    isSecureContext: true,
    Notification: {
      permission: 'default',
      requestPermission: async () => {
        permissions++;
        return 'granted';
      },
    },
    navigator: { userAgent: 'synthetic', platform: 'Linux', serviceWorker: {} },
    PushManager: {},
    matchMedia: () => ({ matches: false }),
  };
  const browser = createPushBrowser({
    window,
    document: {} as Document,
    load: async () => {
      loads++;
      return sdk;
    },
  });
  assert.equal(browser.availability(), 'supported');
  assert.equal(loads, 0);
  await Promise.all([browser.prepare('a'), browser.prepare('a')]);
  assert.equal(loads, 1);
  assert.equal(permissions, 0);
  assert.equal(options[0].welcomeNotification.disable, true);
  assert.equal(options[0].autoResubscribe, false);
  assert.equal(options[0].notifyButton.enable, false);
  assert.ok(
    options[0].promptOptions.slidedown.prompts.every(
      (v: any) => v.autoPrompt === false,
    ),
  );
  await assert.rejects(browser.prepare('b'), /reload/);
  let received: unknown;
  browser.listen((v) => (received = v));
  events.get('foregroundWillDisplay')!({
    notification: { additionalData: { cycrio_challenge_id: 'challenge' } },
  });
  assert.deepEqual(received, { cycrio_challenge_id: 'challenge' });
  await browser.permission();
  assert.equal(permissions, 1);
});
test('待機中のページはCycrioのルートworkerから届いた受信証明だけを中継する', async () => {
  const events = new EventTarget();
  const origin = 'https://cycrio.example';
  const sdk = {
    init: async () => {},
    Notifications: { addEventListener: () => {} },
  } as unknown as OneSignalSDK;
  const browser = createPushBrowser({
    window: {
      location: { origin },
      navigator: { serviceWorker: events },
    } as unknown as Window,
    document: {} as Document,
    load: async () => sdk,
  });
  const received: unknown[] = [];
  browser.listen((proof) => received.push(proof));
  await browser.prepare('app');
  const proof = { cycrio_challenge_id: 'challenge' };
  function emit(path: string, type = 'cycrio.push.proof') {
    const event = new Event('message');
    Object.assign(event, {
      data: { type, proof },
      source: { scriptURL: new URL(path, origin).href },
    });
    events.dispatchEvent(event);
  }
  emit('https://other.example/OneSignalSDKWorker.js');
  emit('/another-worker.js');
  emit('/OneSignalSDKWorker.js', 'unrelated');
  emit('/OneSignalSDKWorker.js?appId=app&sdkVersion=160800');
  assert.deepEqual(received, [proof]);
});
test('ルートworkerが表示した確認通知の証明だけを取得する', async () => {
  const origin = 'https://cycrio.example';
  const proof = { cycrio_challenge_id: 'challenge' };
  const registration = {
    active: { scriptURL: origin + '/OneSignalSDKWorker.js?appId=app' },
    getNotifications: async () => [
      { data: { launchURL: origin + '/mail', additionalData: proof } },
      {
        data: {
          launchURL: origin + '/settings/notifications',
          additionalData: proof,
        },
      },
    ],
  };
  const browser = createPushBrowser({
    window: {
      location: { origin },
      navigator: {
        serviceWorker: { getRegistration: async () => registration },
      },
    } as unknown as Window,
    document: {} as Document,
  });
  assert.deepEqual(await browser.displayedProof('challenge'), proof);
  assert.equal(await browser.displayedProof('another-challenge'), undefined);
  registration.active.scriptURL = origin + '/other-worker.js';
  assert.equal(await browser.displayedProof('challenge'), undefined);
});
test('端末登録はルートworkerが保有するWeb Push購読の認証情報を使う', async () => {
  const origin = 'https://cycrio.example';
  const registration = {
    active: { scriptURL: origin + '/OneSignalSDKWorker.js?appId=app' },
    update: async () => {},
    pushManager: {
      getSubscription: async () => ({
        getKey: () => new Uint8Array(16).buffer,
      }),
    },
  };
  const sdk = {
    init: async () => {},
    Notifications: { addEventListener: () => {} },
    User: { onesignalId: 'external-user' },
  } as unknown as OneSignalSDK;
  const browser = createPushBrowser({
    window: {
      location: { origin },
      navigator: {
        serviceWorker: { getRegistration: async () => registration },
      },
    } as unknown as Window,
    document: {} as Document,
    load: async () => sdk,
  });
  await browser.prepare('app');
  assert.deepEqual(await browser.subscriptionProof!(), {
    onesignal_user_id: 'external-user',
    web_auth: 'AAAAAAAAAAAAAAAAAAAAAA==',
  });
  registration.active.scriptURL = origin + '/different-worker.js';
  await assert.rejects(
    browser.subscriptionProof!(),
    /subscription unavailable/,
  );
});
test('script読込タイムアウト後の遅着は初期化せず再読込を要求する', async () => {
  let timeout!: () => void;
  const scripts: any[] = [];
  const window: any = {
    setTimeout: (fn: () => void) => {
      timeout = fn;
      return 1;
    },
    clearTimeout: () => {},
  };
  const document: any = {
    createElement: () => ({ remove() {} }),
    head: { append: (script: any) => scripts.push(script) },
  };
  const b = createPushBrowser({ window, document });
  const first = b.prepare('a');
  timeout();
  await assert.rejects(first, /reload/);
  let inits = 0;
  window.OneSignalDeferred[0]({
    init: async () => {
      inits++;
    },
  });
  await assert.rejects(b.prepare('a'), /reload/);
  assert.equal(inits, 0);
  assert.equal(scripts.length, 1);
});
test('scriptの確定した通信エラー後だけ新しいscriptを挿入する', async () => {
  const scripts: any[] = [];
  const window: any = { setTimeout: () => 1, clearTimeout: () => {} };
  const document: any = {
    createElement: () => ({ remove() {} }),
    head: { append: (script: any) => scripts.push(script) },
  };
  const b = createPushBrowser({ window, document });
  const first = b.prepare('a');
  scripts[0].onerror();
  await assert.rejects(first, /sdk unavailable/);
  const second = b.prepare('a');
  let inits = 0;
  const sdk: any = {
    init: async () => {
      inits++;
    },
    Notifications: { addEventListener: () => {} },
  };
  window.OneSignalDeferred[0](sdk);
  window.OneSignalDeferred[1](sdk);
  await second;
  assert.equal(inits, 1);
  assert.equal(scripts.length, 2);
});
test('iPhoneのホーム画面追加と非対応をSDKなしで案内する', () => {
  let loads = 0;
  const window: any = {
    navigator: { userAgent: 'iPhone' },
    matchMedia: () => ({ matches: false }),
  };
  const b = createPushBrowser({
    window,
    document: {} as Document,
    load: async () => {
      loads++;
      throw Error();
    },
  });
  assert.equal(b.availability(), 'home_screen');
  window.navigator.userAgent = 'other';
  assert.equal(b.availability(), 'unsupported');
  assert.equal(loads, 0);
});
