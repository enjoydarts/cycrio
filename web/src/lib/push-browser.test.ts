import assert from 'node:assert/strict';
import test from 'node:test';
import { createPushBrowser } from './push-browser.ts';
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
    Notification: { permission: 'default' },
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
