import assert from 'node:assert/strict';
import test from 'node:test';
import { createPushController, type PushStorage } from './push-controller.ts';
import { deriveRevocationToken } from './push-api.ts';
import { ApiError } from './api.ts';
const a = '11111111-1111-4111-8111-111111111111',
  b = '22222222-2222-4222-8222-222222222222',
  challenge = '33333333-3333-4333-8333-333333333333';
const secret = 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA';
const deferred = <T>() => {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
};
function fixture(shared?: PushStorage) {
  const map = new Map<string, string>();
  const storage = shared ?? {
    get length() {
      return map.size;
    },
    key: (i: number) => [...map.keys()][i] ?? null,
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => {
      map.set(k, v);
    },
    removeItem: (k: string) => {
      map.delete(k);
    },
  };
  let listener: (v: unknown) => void = () => {};
  const calls: string[] = [];
  let active = false,
    failRevoke = false,
    clock = 0;
  const registration = deferred<any>();
  const confirmation = deferred<any>();
  const api = {
    list: async () => ({
      app_id: a,
      available: true,
      reason: '' as const,
      devices: active
        ? [
            {
              id: b,
              state: 'active' as const,
              created_at: '2026-11-10T00:00:00Z',
              updated_at: '2026-11-10T00:00:00Z',
            },
          ]
        : [],
    }),
    register: async () => {
      calls.push('register');
      return registration.promise;
    },
    confirm: async () => {
      calls.push('confirm');
      assert.ok(
        [...Array(storage.length)].some((_, i) =>
          storage.getItem(storage.key(i)!)?.includes('revocation_token'),
        ),
      );
      active = true;
      return confirmation.promise;
    },
    disable: async () => {
      active = false;
      calls.push('disable');
    },
    revoke: async () => {
      calls.push('revoke');
      if (failRevoke) throw Error('offline');
      active = false;
    },
  };
  const sdk = {
    currentPermission: (): NotificationPermission => 'granted',
    availability: () => 'supported' as const,
    prepare: async () => {
      calls.push('prepare');
    },
    permission: async () => {
      calls.push('permission');
      return 'granted' as const;
    },
    subscription: async () => {
      calls.push('subscription');
      return b;
    },
    listen: (cb: (v: unknown) => void) => {
      listener = cb;
      return () => {};
    },
  };
  const c = createPushController({ api, sdk, storage, now: () => clock });
  const token = async () => 'token';
  c.bind(a, token);
  return {
    c,
    api,
    sdk,
    calls,
    storage,
    registration,
    confirmation,
    token,
    emit: (
      data: unknown = {
        cycrio_challenge_id: challenge,
        cycrio_challenge_secret: secret,
      },
    ) => listener(data),
    get active() {
      return active;
    },
    set failRevoke(v: boolean) {
      failRevoke = v;
    },
    set clock(v: number) {
      clock = v;
    },
  };
}
const flush = () => new Promise((r) => setTimeout(r, 15));
test('初回defaultは解除やSDK起動を行わない', async () => {
  const f = fixture();
  f.sdk.currentPermission = () => 'default';
  await f.c.load();
  await f.c.synchronize();
  assert.equal(f.c.state().status, 'idle');
  assert.deepEqual(f.calls, []);
});
test('一覧取得に失敗しても許可取消の解除証明を停止待ちとして保持する', async () => {
  const f = fixture();
  f.storage.setItem(
    'cycrio.push.receipt.v1.' + challenge,
    JSON.stringify({
      owner: a,
      operation: challenge,
      device_id: b,
      revocation_token: secret,
      stop: false,
    }),
  );
  f.sdk.currentPermission = () => 'denied';
  f.api.list = async () => {
    throw Error('offline');
  };
  f.failRevoke = true;
  await f.c.load();
  assert.equal(f.c.state().cleanupPending, true);
  assert.ok(
    f.storage
      .getItem('cycrio.push.receipt.v1.' + challenge)
      ?.includes('"stop":true'),
  );
  assert.equal(f.c.state().status, 'denied');
});
test('許可取消をSDKなしで本人の現在端末だけへ反映し解除失敗を再試行する', async () => {
  const f = fixture();
  f.storage.setItem(
    'cycrio.push.receipt.v1.' + challenge,
    JSON.stringify({
      owner: a,
      operation: challenge,
      device_id: b,
      revocation_token: secret,
      stop: false,
    }),
  );
  const other = {
    id: challenge,
    state: 'active' as const,
    created_at: '2026-11-10T00:00:00Z',
    updated_at: '2026-11-10T00:00:00Z',
  };
  f.api.list = async () => ({
    app_id: a,
    available: true,
    reason: '',
    devices: [{ ...other, id: b }, other],
  });
  f.sdk.currentPermission = () => 'denied';
  f.failRevoke = true;
  await f.c.load();
  assert.equal(f.c.state().status, 'denied');
  assert.equal(f.c.state().cleanupPending, true);
  assert.deepEqual(f.calls, ['revoke']);
  assert.ok(
    f.storage
      .getItem('cycrio.push.receipt.v1.' + challenge)
      ?.includes('"stop":true'),
  );
  f.failRevoke = false;
  await f.c.load();
  assert.equal(f.c.state().cleanupPending, false);
  assert.equal(
    f.c.state().page?.devices.find((d) => d.id === challenge)?.state,
    'active',
  );
  assert.deepEqual(f.calls, ['revoke', 'revoke']);
});
test('3分後の受信証明を5分期限内に確認する', async () => {
  const f = fixture();
  await f.c.load();
  await f.c.prepare();
  f.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await f.c.allow();
  f.clock = 180000;
  f.emit();
  await flush();
  assert.ok(f.calls.includes('confirm'));
});
for (const permission of ['denied', 'default'] as const)
  test(`復帰時の${permission}は現在端末を停止して一覧へ反映する`, async () => {
    const f = fixture();
    await f.c.load();
    await f.c.prepare();
    f.registration.resolve({
      device_id: b,
      challenge_id: challenge,
      state: 'pending',
      expires_at: new Date(300000).toISOString(),
    });
    await f.c.allow();
    f.emit();
    await flush();
    f.confirmation.resolve({
      device: { id: b, state: 'active' },
      revocation_token: await deriveRevocationToken(secret),
    });
    await flush();
    f.sdk.currentPermission = () => permission;
    await f.c.synchronize();
    assert.equal(f.active, false);
    assert.equal(f.c.state().status, 'denied');
    assert.ok(f.c.state().page?.devices.every((d) => d.state !== 'active'));
  });
test('古い一覧読取は所有者切替後の新しい端末を許可取消で停止しない', async () => {
  const f = fixture();
  const pending = deferred<Awaited<ReturnType<typeof f.api.list>>>();
  f.api.list = () => pending.promise;
  f.sdk.currentPermission = () => 'denied';
  const old = f.c.load();
  await flush();
  await f.c.bind(b, f.token);
  f.storage.setItem(
    'cycrio.push.receipt.v1.' + challenge,
    JSON.stringify({
      owner: b,
      operation: challenge,
      device_id: b,
      revocation_token: secret,
      stop: false,
    }),
  );
  pending.resolve({ app_id: a, available: true, reason: '', devices: [] });
  await old;
  assert.equal(f.calls.includes('revoke'), false);
});
test('確認中の許可取消は遅い確認応答で復活しない', async () => {
  const f = fixture();
  await f.c.load();
  await f.c.prepare();
  f.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await f.c.allow();
  f.emit();
  await flush();
  f.sdk.currentPermission = () => 'denied';
  await f.c.load();
  f.confirmation.resolve({
    device: { id: b, state: 'active' },
    revocation_token: await deriveRevocationToken(secret),
  });
  await flush();
  assert.equal(f.active, false);
  assert.equal(f.c.state().status, 'denied');
});
test('受信期限後の再取得は期限切れを表示し明示準備だけを再開する', async () => {
  const f = fixture();
  await f.c.load();
  await f.c.prepare();
  f.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await f.c.allow();
  f.clock = 300000;
  await f.c.load();
  assert.equal(f.c.state().status, 'expired');
  f.emit();
  await flush();
  assert.equal(f.calls.filter((c) => c === 'register').length, 1);
  assert.equal(f.calls.includes('confirm'), false);
  await f.c.prepare();
  assert.equal(f.c.state().status, 'permission_ready');
});
test('表示と通常token更新ではSDKに触れず本人の準備・許可を分ける', async () => {
  const f = fixture();
  await f.c.load();
  f.c.bind(a, async () => 'refreshed');
  assert.deepEqual(f.calls, []);
  await f.c.prepare();
  assert.deepEqual(f.calls, ['prepare']);
  assert.equal(f.c.state().status, 'permission_ready');
  void f.c.allow();
  await flush();
  assert.deepEqual(f.calls, [
    'prepare',
    'permission',
    'subscription',
    'register',
  ]);
});
for (const early of [true, false])
  test(`通知が登録応答${early ? '前' : '後'}でも確認し解除情報を先に保存する`, async () => {
    const f = fixture();
    await f.c.load();
    await f.c.prepare();
    const run = f.c.allow();
    await flush();
    if (early) f.emit();
    f.registration.resolve({
      device_id: b,
      challenge_id: challenge,
      state: 'pending',
      expires_at: new Date(300000).toISOString(),
    });
    await run;
    if (!early) f.emit();
    await flush();
    assert.ok(f.calls.includes('confirm'));
    f.confirmation.resolve({
      device: {
        id: b,
        state: 'active',
        created_at: '2026-11-10T00:00:00Z',
        updated_at: '2026-11-10T00:00:00Z',
      },
      revocation_token: await deriveRevocationToken(secret),
    });
    await flush();
    assert.equal(f.c.state().status, 'active');
    await f.c.bind(null);
    assert.equal(f.active, false);
  });
test('確認中logoutの先行解除と遅着確認の再解除で幽霊端末を残さない', async () => {
  const f = fixture();
  await f.c.load();
  await f.c.prepare();
  const run = f.c.allow();
  await flush();
  f.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await run;
  f.emit();
  await flush();
  await f.c.bind(null);
  assert.ok(f.calls.includes('revoke'));
  f.confirmation.resolve({
    device: { id: b, state: 'active' },
    revocation_token: await deriveRevocationToken(secret),
  });
  await flush();
  assert.equal(f.active, false);
  assert.ok(f.calls.filter((x) => x === 'revoke').length >= 2);
});
test('別タブの所有者変更と古い通知は新所有者の記録を上書きしない', async () => {
  const old = fixture();
  await old.c.load();
  await old.c.prepare();
  const run = old.c.allow();
  await flush();
  const next = fixture(old.storage);
  await next.c.bind(b, async () => 'other');
  old.emit();
  old.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await run;
  await flush();
  assert.ok(!old.calls.includes('confirm'));
});
test('旧タブtokenが有効でも遅着登録応答から同ownerの新世代activeを停止しない', async () => {
  const old = fixture();
  let next: ReturnType<typeof fixture>;
  old.api.disable = async (...args: any[]) => {
    assert.equal(await args[1](), 'token');
    old.calls.push('disable');
    await next.api.disable();
  };
  await old.c.load();
  await old.c.prepare();
  const oldRun = old.c.allow();
  await flush();
  await old.c.bind(null);
  next = fixture(old.storage);
  await next.c.load();
  await next.c.prepare();
  const nextRun = next.c.allow();
  await flush();
  next.registration.resolve({
    device_id: b,
    challenge_id: a,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await nextRun;
  next.emit({ cycrio_challenge_id: a, cycrio_challenge_secret: secret });
  await flush();
  next.confirmation.resolve({
    device: { id: b, state: 'active' },
    revocation_token: await deriveRevocationToken(secret),
  });
  await flush();
  assert.equal(next.c.state().status, 'active');
  assert.equal(await old.token(), 'token');
  old.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await oldRun;
  assert.equal(next.active, true);
  assert.equal(old.calls.includes('disable'), false);
});
test('購読IDの一時失敗は未送信なので一覧再取得後に本人が再試行できる', async () => {
  const f = fixture();
  let subscriptions = 0;
  f.sdk.subscription = async () => {
    if (++subscriptions === 1) throw Error('subscription timeout');
    return b;
  };
  await f.c.load();
  await f.c.prepare();
  await f.c.allow();
  assert.equal(f.calls.includes('register'), false);
  await f.c.load();
  await f.c.prepare();
  assert.equal(f.c.state().status, 'permission_ready');
  const run = f.c.allow();
  await flush();
  f.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await run;
  f.emit();
  await flush();
  f.confirmation.resolve({
    device: { id: b, state: 'active' },
    revocation_token: await deriveRevocationToken(secret),
  });
  await flush();
  assert.equal(f.c.state().status, 'active');
  assert.equal(subscriptions, 2);
  assert.equal(f.calls.filter((v) => v === 'register').length, 1);
});
for (const responseLost of [true, false])
  test(`登録送信後の${responseLost ? '応答喪失' : 'unknown応答'}は一覧再取得後も重複登録しない`, async () => {
    const f = fixture();
    f.api.register = async () => {
      f.calls.push('register');
      if (responseLost) throw Error('registration response lost');
      return {
        device_id: b,
        challenge_id: challenge,
        state: 'unknown',
        expires_at: new Date(300000).toISOString(),
      };
    };
    await f.c.load();
    await f.c.prepare();
    await f.c.allow();
    assert.equal(f.c.state().status, responseLost ? 'uncertain' : 'waiting');
    await f.c.load();
    await f.c.prepare();
    await f.c.allow();
    assert.equal(f.calls.filter((v) => v === 'register').length, 1);
    assert.equal(f.c.state().status, responseLost ? 'uncertain' : 'waiting');
  });
test('解除失敗は残し新しい有効化より前に再試行する', async () => {
  const f = fixture();
  await f.c.load();
  await f.c.prepare();
  const run = f.c.allow();
  await flush();
  f.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await run;
  f.emit();
  await flush();
  f.failRevoke = true;
  await f.c.bind(null);
  assert.ok(f.c.state().cleanupPending);
  f.failRevoke = false;
  await f.c.bind(a, async () => 'new');
  assert.equal(f.c.state().cleanupPending, false);
  assert.equal(f.active, false);
});
test('他challenge・期限切れ通知は確認せず設定部品unmountでも解除しない', async () => {
  const f = fixture();
  await f.c.load();
  await f.c.prepare();
  const run = f.c.allow();
  await flush();
  f.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await run;
  f.emit({ cycrio_challenge_id: a, cycrio_challenge_secret: secret });
  await flush();
  assert.equal(f.calls.includes('confirm'), false);
  f.clock = 300000;
  f.emit();
  await flush();
  assert.equal(f.calls.includes('confirm'), false);
  f.c.subscribe(() => {})();
  assert.equal(f.calls.includes('revoke'), false);
});
test('確認応答喪失はGETで有効状態を照合し登録や確認POSTを繰り返さない', async () => {
  const f = fixture();
  f.api.confirm = async () => {
    throw Error('lost');
  };
  f.api.list = async () => ({
    app_id: a,
    available: true,
    reason: '',
    devices: [
      {
        id: b,
        state: 'active',
        created_at: '2026-11-10T00:00:00Z',
        updated_at: '2026-11-10T00:00:00Z',
      },
    ],
  });
  await f.c.load();
  await f.c.prepare();
  const run = f.c.allow();
  await flush();
  f.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await run;
  f.emit();
  await flush();
  assert.equal(f.c.state().status, 'active');
  assert.equal(f.calls.filter((v) => v === 'register').length, 1);
});
test('共有ownerが変わったタブはstorageイベント前でも追加の許可を要求しない', async () => {
  const old = fixture();
  await old.c.load();
  await old.c.prepare();
  const next = fixture(old.storage);
  await next.c.bind(b, async () => 'new');
  await old.c.allow();
  assert.equal(old.calls.includes('permission'), false);
});
test('旧タブの遅い同一owner token更新は共有ownerを奪い返さない', async () => {
  const old = fixture();
  await old.c.load();
  const next = fixture(old.storage);
  await next.c.bind(b, async () => 'new');
  const before = old.storage.getItem('cycrio.push.owner.v1');
  await old.c.bind(a, async () => 'stale-refresh');
  assert.equal(old.storage.getItem('cycrio.push.owner.v1'), before);
});
test('Push APIで確定した利用拒否も解除capabilityで現在端末を停止する', async () => {
  const f = fixture();
  await f.c.load();
  await f.c.prepare();
  const run = f.c.allow();
  await flush();
  f.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await run;
  f.emit();
  await flush();
  f.confirmation.resolve({
    device: { id: b, state: 'active' },
    revocation_token: await deriveRevocationToken(secret),
  });
  await flush();
  f.api.list = async () => {
    throw new ApiError('access_denied');
  };
  await f.c.load();
  await flush();
  assert.equal(f.active, false);
});
test('確認成功後の重複SDKイベントは再確認せずサーバー停止を再取得で反映する', async () => {
  const f = fixture();
  await f.c.load();
  await f.c.prepare();
  const run = f.c.allow();
  await flush();
  f.registration.resolve({
    device_id: b,
    challenge_id: challenge,
    state: 'pending',
    expires_at: new Date(300000).toISOString(),
  });
  await run;
  f.emit();
  await flush();
  f.confirmation.resolve({
    device: { id: b, state: 'active' },
    revocation_token: await deriveRevocationToken(secret),
  });
  await flush();
  f.emit();
  await flush();
  assert.equal(f.calls.filter((v) => v === 'confirm').length, 1);
  f.api.list = async () => ({
    app_id: a,
    available: true,
    reason: '',
    devices: [],
  });
  await f.c.load();
  assert.equal(f.c.state().status, 'disabled');
});
