import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  createRuntimeController,
  createUsageController,
  monthJST,
  outcomeText,
  canActivate,
} from './ai-runtime-controller.ts';
import { id, runtime, run, report, call } from './airuntime-test-fixtures.ts';
import type { AIRuntimeAPI } from './airuntime-api.ts';
import type { CatalogAPI } from './catalog-api.ts';
const deferred = <T>() => {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
};
const fakeAPI = (overrides: object = {}) =>
  ({
    state: async () => runtime(),
    save: async () => runtime(),
    requestTest: async () => run(),
    test: async () => run(),
    activate: async () => runtime(),
    deactivate: async () => runtime(),
    costs: async () => report(),
    calls: async () => ({ calls: [], total: 0 }),
    revisions: async () => [],
    reconcile: async () => call(),
    ...overrides,
  }) as unknown as AIRuntimeAPI;
const catalog = {
  settings: async () => ({
    id,
    version: 1,
    primary_model_id: id,
    secondary_model_id: id,
    primary_price_id: null,
    secondary_price_id: null,
    updated_at: null,
  }),
  get: async () => ({
    id,
    provider: 'openai',
    display_name: 'Test',
    model_id: 'test',
  }),
} as unknown as CatalogAPI;
const token = async () => 'token';
test('OpenRouter approval slugs are rejected before saving or paid test when malformed', async () => {
  for (const slug of [
    'vendor/a/b',
    'vendor//a',
    'vendor/',
    '/vendor',
    'Vendor/a',
    'vendor/a b',
    'a'.repeat(129),
    '',
  ]) {
    let saves = 0,
      posts = 0;
    const draft = {
      ...runtime().draft,
      secondary: { ...runtime().draft.secondary, provider: 'openrouter' },
      openrouter_provider: slug,
    };
    const c = createRuntimeController(
      fakeAPI({
        state: async () => ({ ...runtime(), draft }),
        save: async () => {
          saves++;
          return runtime();
        },
        requestTest: async () => {
          posts++;
          return run();
        },
      }),
      {
        ...catalog,
        get: async () => ({
          ...(await catalog.get(id, token)),
          provider: 'openrouter',
        }),
      },
      token,
      () => {},
      () => {},
    );
    await c.load();
    await c.requestTest(true);
    assert.equal(posts, 0, slug);
    await c.save();
    assert.equal(saves, 0, slug);
    assert.equal(c.state().error, 'invalid_request');
    c.dispose();
  }
});
test('OpenRouter approval accepts provider and one variant with a maximum of 128 characters', async () => {
  for (const slug of [
    'vendor',
    'vendor/a',
    'vendor-1/a_1.b',
    'a'.repeat(128),
    'a/'.padEnd(128, 'b'),
  ]) {
    let posts = 0;
    const c = createRuntimeController(
      fakeAPI({
        state: async () => ({
          ...runtime(),
          draft: {
            ...runtime().draft,
            secondary: { ...runtime().draft.secondary, provider: 'openrouter' },
            openrouter_provider: slug,
          },
        }),
        requestTest: async () => {
          posts++;
          return run();
        },
      }),
      catalog,
      token,
      () => {},
      () => {},
    );
    await c.load();
    await c.requestTest(true);
    assert.equal(posts, 1, slug);
    c.dispose();
  }
});
test('execution availability failures remain unknown and cannot activate or automatically retry', async () => {
  const value = {
    ...run(),
    state: 'unknown',
    primary_outcome: 'execution_unavailable',
  };
  let posts = 0;
  const c = createRuntimeController(
    fakeAPI({
      state: async () => ({ ...runtime(), latest_test: value }),
      requestTest: async () => {
        posts++;
        return run();
      },
    }),
    catalog,
    token,
    () => {},
    () => {},
  );
  await c.load();
  await c.poll();
  assert.equal(c.state().runtime?.latest_test?.state, 'unknown');
  assert.equal(posts, 0);
  assert.equal(canActivate(c.state()), false);
  assert.equal(
    outcomeText('execution_unavailable'),
    '実行状態を確認できません',
  );
  c.dispose();
});
test('runtime load uses GET only and edits retain their baseline during refresh and conflict', async () => {
  let value = runtime();
  let saved: unknown;
  const api = fakeAPI({
    state: async () => value,
    save: async (input: unknown) => {
      saved = input;
      throw new ApiError('conflict');
    },
  });
  const c = createRuntimeController(
    api,
    catalog,
    token,
    () => {},
    () => {},
  );
  await c.load();
  c.edit({ timeout_seconds: 44 });
  value = { ...runtime(), version: 2 };
  await c.refresh();
  assert.equal(c.state().form?.values.timeout_seconds, 44);
  assert.equal(c.state().form?.baseline_version, 1);
  await c.save();
  assert.equal((saved as { expected_version: number }).expected_version, 1);
  assert.equal(c.state().form?.values.timeout_seconds, 44);
  assert.equal(c.state().error, 'conflict');
  await c.refresh();
  assert.equal(c.state().form?.values.timeout_seconds, 44);
  assert.equal(c.state().form?.baseline_version, 1);
  assert.equal(c.state().runtime?.version, 2);
  await c.load();
  assert.equal(c.state().form?.baseline_version, 2);
  c.dispose();
});
test('test request prevents double clicks and reuses UUID after response loss until explicit new test', async () => {
  const pending = deferred<ReturnType<typeof run>>();
  const bodies: { request_id: string }[] = [];
  let failed = true;
  const api = fakeAPI({
    requestTest: async (input: { request_id: string }) => {
      bodies.push(input);
      if (failed) {
        failed = false;
        throw new ApiError('dependency_unavailable');
      }
      return pending.promise;
    },
  });
  const c = createRuntimeController(
    api,
    catalog,
    token,
    () => {},
    () => {},
    () => id,
  );
  await c.load();
  await c.requestTest(true);
  const again = c.requestTest(true);
  await c.requestTest(true);
  assert.equal(bodies.length, 2);
  assert.equal(bodies[0].request_id, bodies[1].request_id);
  pending.resolve(run());
  await again;
  await c.requestTest(true);
  assert.equal(bodies.length, 2);
  c.dispose();
});
test('unsaved runtime edits and changed model settings prevent test and activation', async () => {
  let posts = 0;
  const api = fakeAPI({
    requestTest: async () => {
      posts++;
      return run();
    },
    activate: async () => {
      posts++;
      return runtime();
    },
  });
  const c = createRuntimeController(
    api,
    catalog,
    token,
    () => {},
    () => {},
  );
  await c.load();
  c.edit({ timeout_seconds: 40 });
  await c.requestTest(true);
  await c.activate();
  assert.equal(posts, 0);
  c.dispose();
  const stale = createRuntimeController(
    api,
    {
      ...catalog,
      settings: async () => ({
        ...(await catalog.settings(token)),
        id: '00000000-0000-4000-8000-000000000002',
        version: 2,
      }),
    },
    token,
    () => {},
    () => {},
  );
  await stale.load();
  await stale.requestTest(true);
  assert.equal(posts, 0);
  stale.dispose();
});
test('role loss aborts requests and removes runtime, forms and usage data', async () => {
  let denied = 0;
  const c = createRuntimeController(
    fakeAPI({
      save: async () => {
        throw new ApiError('access_denied');
      },
    }),
    catalog,
    token,
    () => {},
    () => denied++,
  );
  await c.load();
  await c.save();
  assert.equal(c.state().status, 'denied');
  assert.equal(c.state().runtime, null);
  assert.equal(c.state().form, null);
  assert.equal(denied, 1);
  c.dispose();
  const u = createUsageController(
    fakeAPI({
      calls: async () => {
        throw new ApiError('authentication_required');
      },
    }),
    token,
    () => {},
    () => denied++,
  );
  await u.load();
  assert.equal(u.state().report, null);
  assert.equal(u.state().calls, null);
  assert.equal(u.state().status, 'denied');
  u.dispose();
});
test('disposed and older runtime responses cannot restore data', async () => {
  const pending = deferred<ReturnType<typeof runtime>>();
  const c = createRuntimeController(
    fakeAPI({ state: () => pending.promise }),
    catalog,
    token,
    () => {},
    () => {},
  );
  const loading = c.load();
  c.dispose();
  pending.resolve(runtime());
  await loading;
  assert.equal(c.state().runtime, null);
  assert.equal(c.state().status, 'disposed');
});
test('usage latest filters win, reset independent offsets, and exact money stays textual', async () => {
  const old = deferred<ReturnType<typeof report>>();
  let n = 0;
  const requests: unknown[] = [];
  const u = createUsageController(
    fakeAPI({
      costs: async (filter: unknown) => {
        requests.push(filter);
        return n++ === 0 ? old.promise : { ...report(), month: '2026-11' };
      },
    }),
    token,
    () => {},
    () => {},
  );
  const first = u.load();
  const second = u.filter({ month: '2026-11', user_id: id });
  await second;
  old.resolve(report());
  await first;
  assert.equal(u.state().report?.month, '2026-11');
  assert.equal(u.state().filter.user_id, id);
  await u.usersPage(50);
  assert.equal(u.state().usersOffset, 50);
  assert.equal(u.state().callsOffset, 0);
  await u.callsPage(50);
  assert.equal(u.state().usersOffset, 50);
  assert.equal(u.state().callsOffset, 50);
  await u.filter({ provider: 'openai' });
  assert.equal(u.state().usersOffset, 0);
  assert.equal(u.state().callsOffset, 0);
  u.dispose();
});
test('month initializes in Asia/Tokyo across the UTC month boundary', () =>
  assert.equal(monthJST(new Date('2026-10-31T15:01:00Z')), '2026-11'));
test('stopping active execution preserves dirty input with its original baseline', async () => {
  const c = createRuntimeController(
    fakeAPI({
      state: async () => ({ ...runtime(), active: runtime().draft }),
      deactivate: async () => ({ ...runtime(), version: 2 }),
    }),
    catalog,
    token,
    () => {},
    () => {},
  );
  await c.load();
  c.edit({ timeout_seconds: 42 });
  await c.deactivate();
  assert.equal(c.state().form?.values.timeout_seconds, 42);
  assert.equal(c.state().form?.baseline_version, 1);
  assert.equal(c.state().runtime?.version, 2);
  c.dispose();
});
test('a changed profile resolves an uncertain request without reusing its ID against a new profile', async () => {
  let value = runtime();
  const c = createRuntimeController(
    fakeAPI({
      state: async () => value,
      requestTest: async () => {
        throw new ApiError('dependency_unavailable');
      },
    }),
    catalog,
    token,
    () => {},
    () => {},
  );
  await c.load();
  await c.requestTest(true);
  assert.equal(c.state().requestUncertain, true);
  value = {
    ...runtime(),
    draft: { ...runtime().draft, id: '00000000-0000-4000-8000-000000000002' },
    version: 2,
  };
  await c.load();
  assert.equal(c.state().requestUncertain, false);
  c.dispose();
});
test('definitive test rejection releases the request while response loss remains uncertain', async () => {
  for (const code of ['conflict', 'invalid_request'] as const) {
    const c = createRuntimeController(
      fakeAPI({
        requestTest: async () => {
          throw new ApiError(code);
        },
      }),
      catalog,
      token,
      () => {},
      () => {},
    );
    await c.load();
    await c.requestTest(true);
    assert.equal(c.state().requestUncertain, false);
    assert.equal(c.state().error, code);
    c.dispose();
  }
});
test('poll observes queued, running and terminal results using GET without a second POST', async () => {
  let posts = 0;
  let reads = 0;
  let terminal = false;
  const api = fakeAPI({
    state: async () => ({
      ...runtime(),
      latest_test: terminal
        ? {
            ...run(),
            state: 'succeeded',
            primary_outcome: 'success',
            secondary_outcome: 'success',
            classification: 'subscription_related',
            extracted_events: 1,
          }
        : null,
    }),
    requestTest: async () => {
      posts++;
      return run();
    },
    test: async () => {
      reads++;
      terminal = reads > 1;
      return { ...run(), state: terminal ? 'succeeded' : 'running' };
    },
  });
  const c = createRuntimeController(
    api,
    catalog,
    token,
    () => {},
    () => {},
  );
  await c.load();
  await c.requestTest(true);
  await c.poll();
  assert.equal(c.state().runtime?.latest_test?.state, 'running');
  await c.poll();
  assert.equal(c.state().runtime?.latest_test?.state, 'succeeded');
  assert.equal(posts, 1);
  assert.equal(reads, 2);
  c.dispose();
});
test('saving is serialized, disposal aborts mutations and late success cannot restore the form', async () => {
  const pending = deferred<ReturnType<typeof runtime>>();
  let count = 0;
  let signal: AbortSignal | undefined;
  const c = createRuntimeController(
    fakeAPI({
      save: async (_input: unknown, _token: unknown, s?: AbortSignal) => {
        count++;
        signal = s;
        return pending.promise;
      },
    }),
    catalog,
    token,
    () => {},
    () => {},
  );
  await c.load();
  const saving = c.save();
  await c.save();
  assert.equal(count, 1);
  c.dispose();
  assert.equal(signal?.aborted, true);
  pending.resolve(runtime());
  await saving;
  assert.equal(c.state().runtime, null);
  assert.equal(c.state().form, null);
});
test('usage reconciliation only accepts OpenRouter generations, deduplicates clicks and reloads adopted values', async () => {
  let count = 0;
  const pending = deferred<ReturnType<typeof call>>();
  const u = createUsageController(
    fakeAPI({
      reconcile: async () => {
        count++;
        return pending.promise;
      },
    }),
    token,
    () => {},
    () => {},
  );
  await u.load();
  await u.reconcile({ ...call(), provider: 'openai' } as never);
  await u.reconcile({ ...call(), generation_id: null } as never);
  assert.equal(count, 0);
  const reconciling = u.reconcile(call() as never);
  await u.reconcile(call() as never);
  assert.equal(count, 1);
  pending.resolve(call());
  await reconciling;
  assert.equal(u.state().reconciling, null);
  assert.equal(u.state().status, 'ready');
  u.dispose();
});
