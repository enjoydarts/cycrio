import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  BillingError,
  type BillingAPI,
  type BillingEntry,
} from './billing-api.ts';
import {
  createBillingEditor,
  blankBilling,
  currentBillingMonth,
  createBillingFeed,
  createBillingSummaryController,
} from './billing-controller.ts';
import { subscription } from './subscriptions-fixtures.ts';
import type { SubscriptionsAPI } from './subscriptions-api.ts';
const sid = '11111111-1111-4111-8111-111111111111',
  eid = '22222222-2222-4222-8222-222222222222',
  rid = '66666666-6666-4666-8666-666666666666';
const values = {
  kind: 'payment' as const,
  occurred_on: '2026-10-01',
  amount: '10.000000000001',
  currency: 'JPY',
  refund_of_id: null,
  state: 'active' as const,
};
const entry = (patch: Partial<BillingEntry> = {}): BillingEntry => ({
  ...values,
  id: eid,
  subscription_id: sid,
  version: 1,
  sources: [],
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z',
  ...patch,
});
const page = (
  events: BillingEntry[] = [],
  offset = 0,
  total = events.length,
) => ({ events, total, limit: 20, offset });
const api = (patch: Partial<BillingAPI> = {}): BillingAPI => ({
  list: async () => page(),
  get: async () => entry(),
  create: async () => entry(),
  update: async () => entry(),
  attach: async () => entry(),
  detach: async () => entry(),
  duplicates: async () => ({ ...page(), token: 'a'.repeat(64) }),
  summary: async () => {
    throw new Error('unused');
  },
  ...patch,
});
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}
test('billing manual draft starts without invented facts and JST month crosses UTC boundary', () => {
  assert.deepEqual(blankBilling(), {
    kind: '',
    occurred_on: '',
    amount: '',
    currency: '',
    refund_of_id: null,
    state: 'active',
  });
  assert.equal(
    currentBillingMonth(new Date('2026-09-30T15:00:00Z')),
    '2026-10',
  );
  assert.equal(
    currentBillingMonth(new Date('2026-09-30T14:59:59Z')),
    '2026-09',
  );
});
test('create freezes exact body and UUID after loss; replay works even if preview is unavailable and suppresses double submit', async () => {
  const bodies: unknown[] = [];
  let unavailable = false;
  let release: (() => void) | undefined;
  const controller = createBillingEditor(
    api({
      create: async (_sid, body) => {
        bodies.push(structuredClone(body));
        if (bodies.length === 1) {
          await new Promise<void>((resolve) => {
            release = resolve;
          });
          throw new ApiError('dependency_unavailable');
        }
        return entry();
      },
      duplicates: async () => {
        if (unavailable) throw new ApiError('dependency_unavailable');
        return { ...page(), token: 'a'.repeat(64) };
      },
    }),
    async () => 'token',
    () => {},
    () => {},
    { subscription_id: sid, randomUUID: () => rid },
  );
  controller.edit(values);
  await controller.preview();
  controller.confirm(true);
  const saving = controller.save();
  await tick();
  await controller.save();
  assert.equal(bodies.length, 1);
  release!();
  await saving;
  assert.equal(controller.state().uncertain, true);
  controller.edit({ amount: '999' });
  unavailable = true;
  await controller.replay();
  assert.deepEqual(bodies[0], bodies[1]);
  assert.equal((bodies[1] as { request_id: string }).request_id, rid);
  assert.equal(controller.state().saved?.id, eid);
});
test('uncertain manual receipt survives close and reopen while denied clears every retained draft', async () => {
  const store = new Map();
  const backend = api({
    create: async () => {
      throw new ApiError('dependency_unavailable');
    },
  });
  const options = {
    subscription_id: sid,
    drafts: store,
    draftKey: 'manual',
    randomUUID: () => rid,
  };
  const c = createBillingEditor(
    backend,
    async () => 'token',
    () => {},
    () => {},
    options,
  );
  c.edit(values);
  await c.preview();
  c.confirm(true);
  await c.save();
  c.dispose();
  const reopened = createBillingEditor(
    backend,
    async () => 'token',
    () => {},
    () => {},
    options,
  );
  assert.equal(reopened.state().values.amount, values.amount);
  assert.equal(reopened.state().uncertain, true);
  backend.create = async () => {
    throw new ApiError('access_denied');
  };
  await reopened.replay();
  assert.equal(store.size, 0);
  assert.equal(reopened.state().baseline, null);
  assert.deepEqual(reopened.state().values, blankBilling());
});
test('source zero lookup includes voided records and pagination before any new create attempt', async () => {
  let creates = 0;
  const filters: unknown[] = [];
  const source = {
    result_id: rid,
    event_index: 0,
    original_event: {
      service: 'AI',
      plan: null,
      event_type: 'payment' as const,
      amount: null,
      currency: 'JPY',
      billed_at: null,
      period_start: null,
      period_end: null,
      next_billing_at: null,
      interval_unit: null,
      interval_count: null,
    },
  };
  const c = createBillingEditor(
    api({
      list: async (_sid, filter) => {
        filters.push(filter);
        return filter.offset === 0
          ? page(
              Array.from({ length: 20 }, () => entry()),
              0,
              21,
            )
          : page([entry({ state: 'voided', sources: [source] })], 20, 21);
      },
      create: async () => {
        creates++;
        return entry();
      },
    }),
    async () => 't',
    () => {},
    () => {},
    { subscription_id: sid, source: { result_id: rid, event_index: 0 } },
  );
  await c.resolveSource();
  assert.equal(c.state().linked?.id, eid);
  assert.equal(c.state().linked?.state, 'voided');
  assert.equal(creates, 0);
  assert.equal((filters[0] as { state: string }).state, 'all');
  assert.equal(
    (filters[0] as { source_event_index: number }).source_event_index,
    0,
  );
});
test('delayed source initialization preserves user input and only known AI facts become unconfirmed suggestions', async () => {
  const event = {
    service: 'AI',
    plan: null,
    event_type: 'renewal' as const,
    amount: null,
    currency: 'JPY',
    billed_at: '2026-10-02',
    period_start: null,
    period_end: null,
    next_billing_at: '2026-11-02',
    interval_unit: null,
    interval_count: null,
  };
  let resolve: ((value: ReturnType<typeof page>) => void) | undefined;
  const c = createBillingEditor(
    api({ list: async () => new Promise((done) => (resolve = done)) }),
    async () => 't',
    () => {},
    () => {},
    { subscription_id: sid, source: { result_id: rid, event_index: 0 } },
  );
  const loading = c.initializeSource(event);
  c.edit({ amount: '42' });
  resolve!(page());
  await loading;
  assert.equal(c.state().values.amount, '42');
  assert.equal(c.state().values.occurred_on, '');
  const fresh = createBillingEditor(
    api(),
    async () => 't',
    () => {},
    () => {},
    { subscription_id: sid, source: { result_id: rid, event_index: 0 } },
  );
  await fresh.initializeSource(event);
  assert.equal(fresh.state().values.amount, '');
  assert.equal(fresh.state().values.kind, '');
  assert.equal(fresh.state().values.currency, 'JPY');
  assert.equal(fresh.state().values.occurred_on, '2026-10-02');
  assert.equal(fresh.state().confirmed, false);
});
test('dirty edits and original CAS baseline survive latest reads, with explicit rebase and reconfirmation', async () => {
  let failRead = true;
  const c = createBillingEditor(
    api({
      update: async () => {
        throw new BillingError('conflict', 'version_conflict');
      },
      get: async () => {
        if (failRead) throw new ApiError('dependency_unavailable');
        return entry({ version: 2, amount: '12' });
      },
    }),
    async () => 't',
    () => {},
    () => {},
    { subscription_id: sid, initial: entry() },
  );
  c.edit({ amount: '11' });
  await c.preview();
  c.confirm(true);
  await c.save();
  assert.equal(c.state().baseline?.version, 1);
  assert.equal(c.state().values.amount, '11');
  assert.equal(c.state().conflict, true);
  failRead = false;
  await c.refreshLatest();
  assert.equal(c.state().latest?.version, 2);
  assert.equal(c.state().values.amount, '11');
  c.rebase();
  assert.equal(c.state().baseline?.version, 2);
  assert.equal(c.state().confirmed, false);
  assert.equal(c.state().duplicates, null);
});
test('denial on a conflict follow-up or any picker read purges all private data and cannot revive on late responses', async () => {
  for (const read of [
    'latest',
    'payments',
    'records',
    'duplicates',
    'source',
  ] as const) {
    let denied = 0;
    const forbidden = async () => {
      throw new ApiError('access_denied');
    };
    const c = createBillingEditor(
      api({
        update: async () => {
          throw new BillingError('conflict', 'version_conflict');
        },
        get: forbidden,
        ...(read === 'duplicates' ? { duplicates: forbidden } : {}),
        list: forbidden,
      }),
      async () => 't',
      () => {},
      () => {
        denied++;
      },
      {
        subscription_id: sid,
        initial: entry(),
        source: { result_id: rid, event_index: 0 },
      },
    );
    c.edit({ amount: 'private' });
    if (read === 'latest') await c.refreshLatest();
    if (read === 'payments') await c.loadPayments();
    if (read === 'records') await c.loadRecords();
    if (read === 'duplicates') {
      c.edit(values);
      await c.preview();
    }
    if (read === 'source') await c.resolveSource();
    assert.equal(denied, 1, read);
    assert.equal(c.state().status, 'denied', read);
    assert.deepEqual(c.state().values, blankBilling(), read);
    assert.equal(c.state().payments, null);
    assert.equal(c.state().records, null);
    assert.equal(c.state().linked, null);
  }
});
test('duplicate candidates need current preview and separate acknowledgement; edits and stale tokens reset it', async () => {
  let writes = 0;
  const c = createBillingEditor(
    api({
      duplicates: async () => ({ ...page([entry()]), token: 'a'.repeat(64) }),
      create: async () => {
        writes++;
        throw new BillingError('conflict', 'possible_duplicate');
      },
    }),
    async () => 't',
    () => {},
    () => {},
    { subscription_id: sid },
  );
  c.edit(values);
  c.confirm(true);
  await c.save();
  assert.equal(writes, 0);
  await c.preview();
  c.confirm(true);
  await c.save();
  assert.equal(writes, 0);
  c.acknowledgeDuplicate(true);
  await c.save();
  assert.equal(writes, 1);
  assert.equal(c.state().duplicateConfirmed, false);
  assert.equal(c.state().duplicates, null);
  await c.preview();
  c.acknowledgeDuplicate(true);
  c.edit({ amount: '12' });
  assert.equal(c.state().duplicates, null);
  assert.equal(c.state().duplicateConfirmed, false);
});
test('selected refund payment is fetched beyond first page and wrong currency or target cannot be adopted', async () => {
  const c = createBillingEditor(
    api(),
    async () => 't',
    () => {},
    () => {},
    { subscription_id: sid },
  );
  c.edit({ ...values, kind: 'refund', refund_of_id: null });
  await c.selectPayment(eid);
  assert.equal(c.state().values.refund_of_id, eid);
  c.edit({ currency: 'USD' });
  assert.equal(c.state().values.refund_of_id, null);
  await c.selectPayment(eid);
  assert.equal(c.state().values.refund_of_id, null);
  await c.attachTo(entry({ subscription_id: rid }), true);
  assert.equal(c.state().saved, null);
});
test('clearing or changing a refund parent while its read is pending cannot restore the old selection', async () => {
  let resolve: ((value: BillingEntry) => void) | undefined;
  const c = createBillingEditor(
    api({
      get: async () => new Promise<BillingEntry>((done) => (resolve = done)),
    }),
    async () => 't',
    () => {},
    () => {},
    {
      subscription_id: sid,
      initial: entry({ kind: 'refund', refund_of_id: eid }),
    },
  );
  const loading = c.loadSelectedPayment();
  c.edit({ currency: 'USD' });
  resolve!(entry());
  await loading;
  assert.equal(c.state().values.refund_of_id, null);
  assert.equal(c.state().selectedPayment, null);
  c.edit({ ...values, kind: 'refund', refund_of_id: null });
  const selecting = c.selectPayment(eid);
  c.edit({ refund_of_id: null });
  resolve!(entry());
  await selecting;
  assert.equal(c.state().values.refund_of_id, null);
});
test('source mutation loss retries the same desired state and version, with no implicit financial edits', async () => {
  const bodies: unknown[] = [];
  const c = createBillingEditor(
    api({
      detach: async (_sid, _eid, body) => {
        bodies.push(body);
        if (bodies.length === 1) throw new ApiError('dependency_unavailable');
        return entry({ version: 2 });
      },
    }),
    async () => 't',
    () => {},
    () => {},
    { subscription_id: sid, initial: entry() },
  );
  await c.changeSource('detach', { result_id: rid, event_index: 0 }, true);
  assert.equal(c.state().uncertain, true);
  await c.replay();
  assert.deepEqual(bodies[0], bodies[1]);
  assert.equal(c.state().baseline?.version, 2);
});
test('changing an existing history source preserves unsaved financial inputs and does not close the editor', async () => {
  const c = createBillingEditor(
    api({ attach: async () => entry({ version: 2 }) }),
    async () => 't',
    () => {},
    () => {},
    { subscription_id: sid, initial: entry() },
  );
  c.edit({ amount: '12' });
  await c.changeSource('attach', { result_id: rid, event_index: 0 }, true);
  assert.equal(c.state().values.amount, '12');
  assert.equal(c.state().dirty, true);
  assert.equal(c.state().saved, null);
  assert.equal(c.state().baseline?.version, 2);
});
test('attaching to an existing candidate revision conflict fetches that candidate without rebasing the financial draft onto it', async () => {
  const c = createBillingEditor(
    api({
      attach: async () => {
        throw new BillingError('conflict', 'version_conflict');
      },
      get: async () => entry({ version: 2 }),
    }),
    async () => 't',
    () => {},
    () => {},
    { subscription_id: sid, source: { result_id: rid, event_index: 0 } },
  );
  c.edit(values);
  await c.attachTo(entry(), true);
  assert.equal(c.state().baseline, null);
  assert.equal(c.state().conflict, false);
  assert.equal(c.state().latest?.version, 2);
  assert.equal(c.state().values.amount, values.amount);
});
test('a foreign initial target cannot become an accidental new create', async () => {
  let writes = 0;
  const c = createBillingEditor(
    api({
      create: async () => {
        writes++;
        return entry();
      },
    }),
    async () => 't',
    () => {},
    () => {},
    { subscription_id: sid, initial: entry({ subscription_id: rid }) },
  );
  c.edit(values);
  await c.preview();
  c.confirm(true);
  await c.save();
  assert.equal(writes, 0);
});
test('a retained edit receipt for a different event cannot retarget a reopened detail dialog', async () => {
  const store = new Map();
  const backend = api({
    update: async () => {
      throw new ApiError('dependency_unavailable');
    },
  });
  const a = createBillingEditor(
    backend,
    async () => 't',
    () => {},
    () => {},
    {
      subscription_id: sid,
      initial: entry(),
      drafts: store,
      draftKey: 'detail',
    },
  );
  a.edit({ amount: '11' });
  await a.preview();
  a.confirm(true);
  await a.save();
  a.dispose();
  const b = createBillingEditor(
    backend,
    async () => 't',
    () => {},
    () => {},
    {
      subscription_id: sid,
      initial: entry({ id: rid }),
      drafts: store,
      draftKey: 'detail',
    },
  );
  assert.equal(b.state().baseline?.id, rid);
  assert.equal(b.state().uncertain, false);
});
test('disposing during an in-flight write retains an uncertain receipt only for its original source', async () => {
  let rejectFirst: ((error: unknown) => void) | undefined;
  const bodies: unknown[] = [];
  const store = new Map();
  const backend = api({
    create: async (_sid, body) => {
      bodies.push(structuredClone(body));
      if (bodies.length === 1)
        return new Promise((_resolve, reject) => (rejectFirst = reject));
      return entry();
    },
  });
  const options = {
    subscription_id: sid,
    drafts: store,
    source: { result_id: rid, event_index: 0 },
    draftKey: 'source-0',
    randomUUID: () => rid,
  };
  const a = createBillingEditor(
    backend,
    async () => 't',
    () => {},
    () => {},
    options,
  );
  a.edit({ ...values, amount: '11' });
  await a.preview();
  a.confirm(true);
  const sending = a.save();
  await tick();
  a.dispose();
  const b = createBillingEditor(
    backend,
    async () => 't',
    () => {},
    () => {},
    {
      ...options,
      source: { result_id: rid, event_index: 1 },
      draftKey: 'source-1',
      randomUUID: () => eid,
    },
  );
  b.edit({ ...values, amount: '55' });
  await b.preview();
  b.confirm(true);
  await b.save();
  rejectFirst!(new ApiError('dependency_unavailable'));
  await sending;
  assert.equal(a.state().status, 'disposed');
  assert.equal(b.state().saved?.id, eid);
  const reopened = createBillingEditor(
    backend,
    async () => 't',
    () => {},
    () => {},
    options,
  );
  assert.equal(reopened.state().uncertain, true);
  assert.equal(reopened.state().status, 'error');
  assert.equal(reopened.state().values.amount, '11');
  await reopened.replay();
  assert.deepEqual(bodies[0], bodies[2]);
  assert.equal(
    (bodies[1] as { source_event_index: number }).source_event_index,
    1,
  );
  assert.equal((bodies[1] as { amount: string }).amount, '55');
});
test('newest feed filters win and disposal aborts pending reads and clears data', async () => {
  const resolvers: ((v: string) => void)[] = [];
  const signals: (AbortSignal | undefined)[] = [];
  const c = createBillingFeed(
    async (_filter: string, _token, signal) => {
      signals.push(signal);
      return new Promise<string>((resolve) => resolvers.push(resolve));
    },
    async () => 't',
    () => {},
    () => {},
  );
  const a = c.load('old'),
    b = c.load('new');
  resolvers[1]('new');
  await b;
  resolvers[0]('old');
  await a;
  assert.equal(c.state().page, 'new');
  assert.equal(signals[0]?.aborted, true);
  c.dispose();
  assert.equal(c.state().page, null);
});
test('uncertain source create reconciles the frozen source before replay; found avoids POST and denial purges', async () => {
  for (const outcome of ['found', 'unavailable', 'denied'] as const) {
    let creates = 0,
      reads = 0;
    const source = {
      result_id: rid,
      event_index: 0,
      original_event: {
        service: 'AI',
        plan: null,
        event_type: 'payment' as const,
        amount: null,
        currency: 'JPY',
        billed_at: null,
        period_start: null,
        period_end: null,
        next_billing_at: null,
        interval_unit: null,
        interval_count: null,
      },
    };
    const c = createBillingEditor(
      api({
        list: async () => {
          reads++;
          if (reads === 1) return page();
          if (outcome === 'denied') throw new ApiError('access_denied');
          if (outcome === 'unavailable')
            throw new ApiError('dependency_unavailable');
          return page([entry({ sources: [source] })]);
        },
        create: async () => {
          creates++;
          if (creates === 1) throw new ApiError('dependency_unavailable');
          return entry();
        },
      }),
      async () => 't',
      () => {},
      () => {},
      { subscription_id: sid, source },
    );
    c.edit(values);
    await c.preview();
    c.confirm(true);
    await c.save();
    await c.replay();
    assert.equal(reads, 2, outcome);
    assert.equal(creates, outcome === 'unavailable' ? 2 : 1, outcome);
    assert.equal(
      c.state().status,
      outcome === 'denied' ? 'denied' : 'ready',
      outcome,
    );
  }
});
test('summary loads a selected contract directly by ID independently of paginated search including ended contracts', async () => {
  const calls: unknown[] = [];
  const contracts = {
    get: async (id: string) => {
      calls.push(id);
      return { ...subscription(), status: 'ended' as const };
    },
    list: async (filter: unknown) => {
      calls.push(filter);
      return { subscriptions: [], total: 0, limit: 20, offset: 100 };
    },
  } as unknown as SubscriptionsAPI;
  const summary = {
    month: '2026-10',
    start_date: '2026-10-01',
    end_date: '2026-11-01',
    timezone: 'Asia/Tokyo' as const,
    subscription_id: sid,
    currencies: [],
    invoice_count: 0,
    voided_count: 0,
  };
  const c = createBillingSummaryController(
    api({ summary: async () => summary }),
    contracts,
    async () => 't',
    () => {},
    () => {},
  );
  await c.load({ month: '2026-10', subscription_id: sid });
  await c.search('ended', 100);
  assert.equal(c.state().selected?.status, 'ended');
  assert.equal(calls[0], sid);
  assert.equal((calls[1] as { status: string }).status, 'all');
  assert.equal(c.state().summary?.subscription_id, sid);
  await c.load({ month: '2026-10', subscription_id: null });
  assert.equal(c.state().selected, null);
});
test('summary newest URL filters win and denied picker clears all private summary and selection fields', async () => {
  const resolve: ((value: never) => void)[] = [];
  const contracts = {
    list: async () => {
      throw new ApiError('access_denied');
    },
  } as unknown as SubscriptionsAPI;
  const c = createBillingSummaryController(
    api({
      summary: async () =>
        new Promise((resolvePromise) => resolve.push(resolvePromise)),
    }),
    contracts,
    async () => 't',
    () => {},
    () => {},
  );
  const old = c.load({ month: '2026-09', subscription_id: null }),
    latest = c.load({ month: '2026-10', subscription_id: null });
  resolve[1]({ month: '2026-10' } as never);
  await latest;
  resolve[0]({ month: '2026-09' } as never);
  await old;
  assert.equal(c.state().summary?.month, '2026-10');
  await c.search('private', 0);
  assert.equal(c.state().summary, null);
  assert.equal(c.state().selected, null);
  assert.equal(c.state().contracts, null);
  assert.equal(c.state().query, '');
  assert.equal(c.state().status, 'denied');
});
test('every billing write cancels auxiliary reads and keeps its lock until the current request settles', async (t) => {
  for (const operation of [
    'create',
    'preflight',
    'update',
    'replay',
    'attach',
    'detach',
    'attachTo',
  ] as const)
    for (const outcome of ['unavailable', 'denied', 'success'] as const)
      await t.test(
        `${operation} ignores cancelled auxiliary ${outcome}`,
        async () => {
          const auxiliary = deferred<ReturnType<typeof page>>(),
            lookup = deferred<ReturnType<typeof page>>(),
            result = deferred<BillingEntry>();
          const calls: { body: object; signal?: AbortSignal }[] = [];
          let auxiliarySignal: AbortSignal | undefined,
            denials = 0,
            uuids = 0;
          const request = async (body: object, signal?: AbortSignal) => {
            calls.push({ body, signal });
            if (operation === 'replay' && calls.length === 1)
              throw new ApiError('dependency_unavailable');
            return result.promise;
          };
          const backend = api({
            list: async (_sid, filter, _token, signal) => {
              if (filter.source_result_id)
                return operation === 'preflight' ? lookup.promise : page();
              auxiliarySignal = signal;
              return auxiliary.promise;
            },
            create: async (_sid, body, _token, signal) => request(body, signal),
            update: async (_sid, _id, body, _token, signal) =>
              request(body, signal),
            attach: async (_sid, _id, body, _token, signal) =>
              request(body, signal),
            detach: async (_sid, _id, body, _token, signal) =>
              request(body, signal),
          });
          const c = createBillingEditor(
            backend,
            async () => 't',
            () => {},
            () => {
              denials++;
            },
            {
              subscription_id: sid,
              initial: ['update', 'attach', 'detach'].includes(operation)
                ? entry()
                : null,
              source: ['preflight', 'attachTo'].includes(operation)
                ? { result_id: rid, event_index: 0 }
                : null,
              randomUUID: () => {
                uuids++;
                return rid;
              },
            },
          );
          c.edit(values);
          await c.preview();
          c.confirm(true);
          if (operation === 'replay') await c.save();
          const reading = c.loadRecords();
          await tick();
          const saving =
            operation === 'attachTo'
              ? c.attachTo(entry(), true)
              : operation === 'attach' || operation === 'detach'
                ? c.changeSource(
                    operation,
                    { result_id: rid, event_index: 0 },
                    true,
                  )
                : operation === 'replay'
                  ? c.replay()
                  : c.save();
          await tick();
          if (outcome === 'success') auxiliary.resolve(page([entry()]));
          else
            auxiliary.reject(
              new ApiError(
                outcome === 'denied'
                  ? 'access_denied'
                  : 'dependency_unavailable',
              ),
            );
          await reading;
          assert.equal(c.state().status, 'saving');
          assert.equal(c.state().records, null);
          assert.deepEqual(c.state().loading, []);
          assert.equal(denials, 0);
          assert.equal(auxiliarySignal?.aborted, true);
          c.edit({ amount: '55' });
          c.confirm(true);
          await c.save();
          assert.equal(c.state().values.amount, values.amount);
          assert.equal(
            calls.length,
            operation === 'preflight' ? 0 : operation === 'replay' ? 2 : 1,
          );
          if (operation === 'preflight') {
            lookup.resolve(page());
            await tick();
            assert.equal(c.state().status, 'saving');
            assert.equal(calls.length, 1);
            assert.equal(
              (calls[0].body as { amount: string }).amount,
              values.amount,
            );
          }
          assert.equal(calls.at(-1)?.signal?.aborted, false);
          if (['create', 'preflight', 'replay'].includes(operation))
            assert.equal(uuids, 1);
          result.resolve(entry());
          await saving;
          assert.equal(c.state().status, 'ready');
          assert.equal(denials, 0);
        },
      );
});
test('current write and source preflight authorization refusals still purge every draft', async (t) => {
  for (const code of ['authentication_required', 'access_denied'] as const)
    for (const phase of ['write', 'preflight'] as const)
      await t.test(`${phase} ${code}`, async () => {
        const store = new Map();
        let denials = 0;
        const c = createBillingEditor(
          api({
            list: async () => {
              throw new ApiError(code);
            },
            create: async () => {
              throw new ApiError(code);
            },
          }),
          async () => 't',
          () => {},
          () => {
            denials++;
          },
          {
            subscription_id: sid,
            drafts: store,
            draftKey: 'private',
            source:
              phase === 'preflight' ? { result_id: rid, event_index: 0 } : null,
          },
        );
        c.edit(values);
        await c.preview();
        c.confirm(true);
        await c.save();
        assert.equal(c.state().status, 'denied');
        assert.deepEqual(c.state().values, blankBilling());
        assert.equal(store.size, 0);
        assert.equal(denials, 1);
      });
});
test('summary paired reads observe each current authorization failure regardless of response order', async (t) => {
  type Summary = Awaited<ReturnType<BillingAPI['summary']>>;
  const summary: Summary = {
    month: '2026-10',
    start_date: '2026-10-01',
    end_date: '2026-11-01',
    timezone: 'Asia/Tokyo',
    subscription_id: sid,
    currencies: [],
    invoice_count: 0,
    voided_count: 0,
  };
  for (const first of ['summary', 'selected'] as const)
    for (const a of [
      'success',
      'unavailable',
      'authentication_required',
      'access_denied',
    ] as const)
      for (const b of [
        'success',
        'unavailable',
        'authentication_required',
        'access_denied',
      ] as const)
        await t.test(`${first} first: ${a}, then ${b}`, async () => {
          const sum = deferred<Summary>(),
            selected = deferred<ReturnType<typeof subscription>>();
          let denials = 0;
          const c = createBillingSummaryController(
            api({ summary: async () => sum.promise }),
            {
              get: async () => selected.promise,
              list: async () => ({
                subscriptions: [subscription()],
                total: 1,
                limit: 20,
                offset: 0,
              }),
            } as unknown as SubscriptionsAPI,
            async () => 't',
            () => {},
            () => {
              denials++;
            },
          );
          await c.search('private filter');
          const loading = c.load({ month: '2026-10', subscription_id: sid });
          const settle = (side: 'summary' | 'selected', outcome: typeof a) => {
            if (outcome === 'success') {
              if (side === 'summary') sum.resolve(summary);
              else selected.resolve(subscription());
            } else
              (side === 'summary' ? sum : selected).reject(
                new ApiError(
                  outcome === 'unavailable'
                    ? 'dependency_unavailable'
                    : outcome,
                ),
              );
          };
          settle(first, a);
          await tick();
          settle(first === 'summary' ? 'selected' : 'summary', b);
          await loading;
          await tick();
          const refused = [a, b].some(
            (value) =>
              value === 'authentication_required' || value === 'access_denied',
          );
          assert.equal(
            c.state().status,
            refused
              ? 'denied'
              : a === 'success' && b === 'success'
                ? 'ready'
                : 'error',
          );
          assert.equal(denials, refused ? 1 : 0);
          if (refused) {
            assert.equal(c.state().contracts, null);
            assert.equal(c.state().summary, null);
            assert.equal(c.state().selected, null);
            assert.equal(c.state().query, '');
          }
        });
});
test('superseded or disposed paired reads cannot apply late authorization failures to a new summary scope', async (t) => {
  for (const disposed of [false, true])
    await t.test(disposed ? 'disposed' : 'newer filter', async () => {
      const oldSummary = deferred<Awaited<ReturnType<BillingAPI['summary']>>>(),
        oldSelected = deferred<ReturnType<typeof subscription>>();
      let denials = 0;
      const current = {
        month: '2026-11',
        start_date: '2026-11-01',
        end_date: '2026-12-01',
        timezone: 'Asia/Tokyo' as const,
        subscription_id: null,
        currencies: [],
        invoice_count: 0,
        voided_count: 0,
      };
      const c = createBillingSummaryController(
        api({
          summary: async (month) =>
            month === '2026-10' ? oldSummary.promise : current,
        }),
        { get: async () => oldSelected.promise } as unknown as SubscriptionsAPI,
        async () => 't',
        () => {},
        () => {
          denials++;
        },
      );
      const loading = c.load({ month: '2026-10', subscription_id: sid });
      oldSummary.reject(new ApiError('dependency_unavailable'));
      await tick();
      if (disposed) c.dispose();
      else await c.load({ month: '2026-11', subscription_id: null });
      oldSelected.reject(new ApiError('access_denied'));
      await loading;
      await tick();
      assert.equal(denials, 0);
      assert.equal(c.state().status, disposed ? 'disposed' : 'ready');
      assert.equal(c.state().summary?.month, disposed ? undefined : '2026-11');
    });
});
