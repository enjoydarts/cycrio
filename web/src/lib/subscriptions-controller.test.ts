import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  createSubscriptionEditor,
  createSubscriptionList,
  type SubscriptionDrafts,
} from './subscriptions-controller.ts';
import { id, values, subscription } from './subscriptions-fixtures.ts';
import type { SubscriptionsAPI } from './subscriptions-api.ts';
const wait = <T>() => {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
};
const api = (patch: Partial<SubscriptionsAPI> = {}) =>
  ({
    list: async () => ({ subscriptions: [], total: 0, limit: 20, offset: 0 }),
    get: async () => subscription(),
    create: async () => subscription(),
    update: async () => subscription(),
    attach: async () => subscription(),
    ...patch,
  }) as SubscriptionsAPI;
test('response-loss freezes body and reuses request UUID while suppressing double submit', async () => {
  const gate = wait<never>();
  const bodies: unknown[] = [];
  let uuids = 0;
  const c = createSubscriptionEditor(
    api({
      create: async (input) => {
        bodies.push(input);
        if (bodies.length === 1) return gate.promise;
        return subscription();
      },
    }),
    async () => 't',
    () => {},
    () => {},
    () => {
      uuids++;
      return id;
    },
  );
  c.edit(values);
  c.confirm(true);
  const first = c.save();
  await c.save();
  assert.equal(bodies.length, 1);
  gate.resolve(Promise.reject(new ApiError('dependency_unavailable')) as never);
  await first;
  assert.equal(c.state().uncertain, true);
  c.edit({ service: 'must not replace' });
  await c.replay();
  assert.deepEqual(bodies[0], bodies[1]);
  assert.equal(uuids, 1);
  assert.equal(c.state().saved?.id, id);
});
test('conflict keeps original version and dirty values until explicit rebase; dispose purges', async () => {
  const c = createSubscriptionEditor(
    api({
      update: async () => {
        throw new ApiError('conflict');
      },
      get: async () => ({ ...subscription(), version: 2, service: 'remote' }),
    }),
    async () => 't',
    () => {},
    () => {},
    () => id,
    subscription(),
  );
  c.edit({ service: 'local' });
  c.confirm(true);
  await c.save();
  assert.equal(c.state().baseline?.version, 1);
  assert.equal(c.state().values.service, 'local');
  assert.equal(c.state().latest?.version, 2);
  c.rebase();
  assert.equal(c.state().baseline?.version, 2);
  assert.equal(c.state().values.service, 'local');
  assert.equal(c.state().confirmed, false);
  c.dispose();
  assert.equal(c.state().values.service, '');
  assert.equal(c.state().baseline, null);
});
test('newest filters win and abort/dispose prevent stale list publication', async () => {
  const old =
    wait<
      ReturnType<SubscriptionsAPI['list']> extends Promise<infer T> ? T : never
    >();
  let count = 0;
  let signal: AbortSignal | undefined;
  const c = createSubscriptionList(
    api({
      list: async (_f, _t, s) => {
        if (++count === 1) {
          signal = s;
          return old.promise;
        }
        return {
          subscriptions: [subscription()],
          total: 1,
          limit: 20,
          offset: 0,
        };
      },
    }),
    async () => 't',
    () => {},
    () => {},
  );
  const first = c.load({
    q: 'old',
    status: 'all',
    sort: 'service',
    limit: 20,
    offset: 0,
  });
  await c.load({
    q: 'new',
    status: 'all',
    sort: 'service',
    limit: 20,
    offset: 0,
  });
  assert.equal(signal?.aborted, true);
  old.resolve({ subscriptions: [], total: 0, limit: 20, offset: 0 });
  await first;
  assert.equal(c.state().page?.total, 1);
  c.dispose();
  assert.equal(c.state().page, null);
});
test('closing an uncertain AI registration retains frozen input and receipt for the same event on reopen', async () => {
  const drafts: SubscriptionDrafts = new Map();
  const bodies: unknown[] = [];
  const a = api({
    create: async (body) => {
      bodies.push(body);
      if (bodies.length === 1) throw new ApiError('dependency_unavailable');
      return subscription();
    },
  });
  const make = () =>
    createSubscriptionEditor(
      a,
      async () => 't',
      () => {},
      () => {},
      () => id,
      null,
      { result_id: id, event_index: 0 },
      { store: drafts, key: 'event0' },
    );
  const c = make();
  c.edit(values);
  c.confirm(true);
  await c.save();
  c.dispose();
  const reopened = make();
  assert.equal(reopened.state().uncertain, true);
  assert.equal(reopened.state().values.amount, '0');
  await reopened.replay();
  assert.deepEqual(bodies[0], bodies[1]);
  assert.equal(drafts.size, 0);
});
test('owner denial during saving purges private editor values and prevents future mutations', async () => {
  let denied = 0,
    calls = 0;
  const c = createSubscriptionEditor(
    api({
      create: async () => {
        calls++;
        throw new ApiError('access_denied');
      },
    }),
    async () => 't',
    () => {},
    () => denied++,
    () => id,
  );
  c.edit(values);
  c.confirm(true);
  await c.save();
  assert.equal(c.state().status, 'denied');
  assert.equal(c.state().values.service, '');
  assert.equal(denied, 1);
  c.edit(values);
  c.confirm(true);
  await c.save();
  assert.equal(calls, 1);
});
test('a failed latest read after conflict can be retried without replacing dirty input', async () => {
  let reads = 0;
  const c = createSubscriptionEditor(
    api({
      update: async () => {
        throw new ApiError('conflict');
      },
      get: async () => {
        if (++reads === 1) throw new ApiError('dependency_unavailable');
        return { ...subscription(), version: 2 };
      },
    }),
    async () => 't',
    () => {},
    () => {},
    () => id,
    subscription(),
  );
  c.edit({ service: 'keep' });
  c.confirm(true);
  await c.save();
  assert.equal(c.state().latest, null);
  await c.refreshLatest();
  assert.equal(c.state().latest?.version, 2);
  assert.equal(c.state().values.service, 'keep');
  assert.equal(c.state().baseline?.version, 1);
  c.dispose();
});
