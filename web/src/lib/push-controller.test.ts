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
  c.bind(a, async () => 'token');
  return {
    c,
    api,
    sdk,
    calls,
    storage,
    registration,
    confirmation,
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
  });
  await run;
  await flush();
  assert.ok(!old.calls.includes('confirm'));
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
  });
  await run;
  f.emit({ cycrio_challenge_id: a, cycrio_challenge_secret: secret });
  await flush();
  assert.equal(f.calls.includes('confirm'), false);
  f.clock = 120001;
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
