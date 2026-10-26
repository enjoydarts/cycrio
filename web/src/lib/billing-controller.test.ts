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
