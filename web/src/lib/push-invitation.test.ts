import assert from 'node:assert/strict';
import test from 'node:test';
import { notificationSettings } from './notifications-fixtures.ts';
import { ApiError } from './api.ts';
import {
  createPushInvitation,
  invitationPreferences,
} from './push-invitation.ts';
import type { PushView } from './push-controller.ts';

function fixture() {
  const values = new Map<string, string>();
  let view: PushView = {
    page: {
      app_id: '11111111-1111-4111-8111-111111111111',
      available: true,
      reason: '',
      devices: [],
    },
    status: 'idle',
    busy: false,
    cleanupPending: false,
    currentDevice: null,
  };
  let changed: (view: PushView) => void = () => {};
  const calls: string[] = [];
  const page = notificationSettings();
  page.push = { available: true, reason: '' };
  page.preferences.email_opt_in = true;
  page.preferences.kinds.annual_charge.email = true;
  let availability = 'supported' as 'supported' | 'home_screen' | 'unsupported';
  let permission: NotificationPermission = 'default';
  let fail = false;
  const options = {
    owner: 'owner-a',
    token: async () => 'token',
    pathname: '/',
    storage: {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => {
        values.set(key, value);
      },
    },
    browser: {
      availability: () => availability,
      currentPermission: () => permission,
    },
    push: {
      subscribe: (fn: (view: PushView) => void) => {
        changed = fn;
        fn(view);
        return () => {};
      },
      prepare: async () => {
        calls.push('prepare');
      },
    },
    notifications: {
      settings: async () => {
        calls.push('read');
        return structuredClone(page);
      },
      updateSettings: async (input: any) => {
        calls.push('save');
        if (fail) throw new ApiError('conflict');
        page.preferences = { ...input, version: input.expected_version + 1 };
        return structuredClone(page);
      },
    },
    denied: () => {
      calls.push('denied');
    },
    blocked: () => false,
  };
  return {
    options,
    calls,
    values,
    page,
    status: (status: PushView['status']) => {
      view = { ...view, status };
      changed(view);
    },
    availability: (value: typeof availability) => {
      availability = value;
    },
    permission: (value: NotificationPermission) => {
      permission = value;
    },
    fail: () => {
      fail = true;
    },
  };
}

test('初回はポップアップだけを表示し、同意前に保存もSDK準備もしない', async () => {
  const f = fixture(),
    invitation = createPushInvitation(f.options);
  await invitation.offer('/');
  assert.equal(invitation.state().open, true);
  assert.deepEqual(f.calls, ['read']);
  invitation.dispose();
});
test('あとでを選ぶと同じ本人・端末では再表示せず別の本人には表示する', async () => {
  const f = fixture();
  const first = createPushInvitation(f.options);
  await first.offer('/');
  first.dismiss();
  first.dispose();
  const second = createPushInvitation(f.options);
  await second.offer('/');
  assert.equal(second.state().open, false);
  second.dispose();
  const other = createPushInvitation({ ...f.options, owner: 'owner-b' });
  await other.offer('/');
  assert.equal(other.state().open, true);
  other.dispose();
});
test('有効化済み・拒否済み・非対応・設定中・未設定では勧誘しない', async () => {
  for (const state of ['active', 'waiting', 'denied'] as const) {
    const f = fixture();
    f.status(state);
    const c = createPushInvitation(f.options);
    await c.offer('/');
    assert.equal(c.state().open, false);
    assert.deepEqual(f.calls, []);
    c.dispose();
  }
  for (const reason of [
    'permission',
    'support',
    'configuration',
    'settings',
    'dialog',
  ]) {
    const f = fixture();
    if (reason === 'permission') f.permission('denied');
    if (reason === 'support') f.availability('unsupported');
    const c = createPushInvitation({
      ...f.options,
      blocked: () => reason === 'dialog',
    });
    if (reason === 'configuration')
      f.options.push.subscribe((v) => {
        v.page!.available = false;
      });
    await c.offer(reason === 'settings' ? '/settings/notifications' : '/');
    assert.equal(c.state().open, false, reason);
    c.dispose();
  }
});
test('有効にするの明示操作後だけ独立したPush設定を保存しSDK準備へ進む', async () => {
  const f = fixture(),
    original = structuredClone(f.page.preferences),
    c = createPushInvitation(f.options);
  await c.offer('/');
  await c.enable();
  assert.deepEqual(f.calls, ['read', 'save', 'prepare']);
  assert.equal(c.state().deviceReady, true);
  assert.equal(f.page.preferences.push_opt_in, true);
  assert.equal(f.page.preferences.kinds.renewal.push, true);
  assert.equal(f.page.preferences.kinds.pending_review.push, true);
  for (const [kind, channel] of Object.entries(original.kinds)) {
    assert.equal(
      f.page.preferences.kinds[kind as keyof typeof original.kinds].email,
      channel.email,
    );
    assert.equal(
      f.page.preferences.kinds[kind as keyof typeof original.kinds].in_app,
      channel.in_app,
    );
  }
  assert.equal(f.page.preferences.email_opt_in, original.email_opt_in);
  assert.equal(f.page.preferences.lead_days, original.lead_days);
  c.dispose();
});
test('保存競合ではSDKもブラウザー許可も開始しない', async () => {
  const f = fixture();
  f.fail();
  const c = createPushInvitation(f.options);
  await c.offer('/');
  await c.enable();
  assert.equal(c.state().deviceReady, false);
  assert.equal(c.state().error, true);
  assert.deepEqual(f.calls, ['read', 'save']);
  c.dispose();
});
test('iPhoneブラウザーはホーム画面案内へ進み保存せず、追加アプリでは再度案内できる', async () => {
  const f = fixture();
  f.availability('home_screen');
  const c = createPushInvitation(f.options);
  await c.offer('/');
  await c.enable();
  assert.equal(c.state().stage, 'home');
  assert.deepEqual(f.calls, ['read']);
  c.dismiss();
  c.dispose();
  f.availability('supported');
  const installed = createPushInvitation(f.options);
  await installed.offer('/');
  assert.equal(installed.state().open, true);
  installed.dispose();
});
test('既存のPush種類は保持し、設定全体を直接変更しない', () => {
  const p = notificationSettings().preferences;
  p.push_opt_in = true;
  p.kinds.import_error.push = true;
  const next = invitationPreferences(p);
  assert.equal(next.kinds.import_error.push, true);
  assert.equal(next.kinds.renewal.push, false);
  next.kinds.import_error.push = false;
  assert.equal(p.kinds.import_error.push, true);
});
test('所有者の画面が破棄された後の保存応答ではSDKを開始しない', async () => {
  const f = fixture();
  let finish!: () => void;
  f.options.notifications.updateSettings = async (input: any) => {
    f.calls.push('save');
    await new Promise<void>((resolve) => {
      finish = resolve;
    });
    return {
      ...f.page,
      preferences: { ...input, version: input.expected_version + 1 },
    };
  };
  const c = createPushInvitation(f.options);
  await c.offer('/');
  const pending = c.enable();
  c.dispose();
  finish();
  await pending;
  assert.deepEqual(f.calls, ['read', 'save']);
});
test('別タブであとでを選ぶと、まだ同意していない案内を閉じる', async () => {
  const f = fixture(),
    first = createPushInvitation(f.options),
    second = createPushInvitation(f.options);
  await first.offer('/');
  await second.offer('/');
  first.dismiss();
  second.synchronize();
  assert.equal(second.state().open, false);
  assert.deepEqual(f.calls, ['read', 'read']);
  first.dispose();
  second.dispose();
});
test('案内の保存ができない環境では通知設定を書き換えずSDKも開始しない', async () => {
  const f = fixture();
  f.options.storage.setItem = () => {
    throw Error('保存不可');
  };
  const c = createPushInvitation(f.options);
  await c.offer('/');
  await c.enable();
  assert.equal(c.state().error, true);
  assert.deepEqual(f.calls, ['read']);
  c.dispose();
});
