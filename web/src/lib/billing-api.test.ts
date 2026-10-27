import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createBillingAPI,
  parseBillingEntry,
  parseBillingValues,
  parseBillingSummary,
  BillingError,
} from './billing-api.ts';
const sid = '11111111-1111-4111-8111-111111111111';
const eid = '22222222-2222-4222-8222-222222222222';
const rid = '66666666-6666-4666-8666-666666666666';
export const billingValues = {
  kind: 'payment' as const,
  occurred_on: '2028-02-29',
  amount: '123456789012345678901234.123456789012',
  currency: 'JPY',
  refund_of_id: null,
  state: 'active' as const,
};
export const billingEntry = () => ({
  ...billingValues,
  id: eid,
  subscription_id: sid,
  version: 1,
  sources: [],
  created_at: '2026-10-26T00:00:00Z',
  updated_at: '2026-10-26T00:00:00Z',
});
const summary = () => ({
  month: '2028-02',
  start_date: '2028-02-01',
  end_date: '2028-03-01',
  timezone: 'Asia/Tokyo',
  subscription_id: null,
  currencies: [
    {
      currency: 'JPY',
      payment_amount: '123456789012345678901234567890.123456789012',
      refund_amount: '0',
      net_amount: '123456789012345678901234567890.123456789012',
      payment_count: 2,
      refund_count: 0,
    },
  ],
  invoice_count: 1,
  voided_count: 2,
});
test('billing values require real dates, exact nonnegative money, explicit nullable parent and valid states', () => {
  assert.equal(parseBillingValues(billingValues).amount, billingValues.amount);
  assert.equal(
    parseBillingValues({ ...billingValues, amount: '0000.00' }).amount,
    '0000.00',
  );
  for (const patch of [
    { amount: 0 },
    { amount: '-1' },
    { amount: '1e3' },
    { amount: '1'.repeat(25) },
    { amount: '1.1234567890123' },
    { currency: 'jpy' },
    { occurred_on: '2026-02-29' },
    { occurred_on: '0000-01-01' },
    { refund_of_id: undefined },
    { kind: 'refund' },
    { refund_of_id: eid },
    { state: 'deleted' },
    { kind: 'charge' },
  ])
    assert.throws(() => parseBillingValues({ ...billingValues, ...patch }));
  assert.equal(
    parseBillingValues({ ...billingValues, kind: 'refund', refund_of_id: eid })
      .refund_of_id,
    eid,
  );
});
test('billing sources preserve independent original AI values and event index zero', () => {
  const source = {
    provenance: 'automatic' as const,
    result_id: rid,
    event_index: 0,
    original_event: {
      service: 'AI',
      plan: null,
      event_type: 'payment',
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
  assert.deepEqual(
    parseBillingEntry({ ...billingEntry(), sources: [source] }).sources,
    [source],
  );
  for (const patch of [
    { version: 0 },
    { subscription_id: null },
    { sources: null },
    { sources: [source, source] },
    { sources: [{ ...source, event_index: 32 }] },
    { updated_at: '2026-02-30T00:00:00Z' },
  ])
    assert.throws(() => parseBillingEntry({ ...billingEntry(), ...patch }));
});
test('monthly totals accept larger sums, negative nets and actual zero rows without rounding', () => {
  assert.equal(
    parseBillingSummary(summary()).currencies[0].payment_amount,
    summary().currencies[0].payment_amount,
  );
  assert.equal(
    parseBillingSummary({
      ...summary(),
      currencies: [
        {
          currency: 'JPY',
          payment_amount: '0',
          refund_amount: '2',
          net_amount: '-2',
          payment_count: 0,
          refund_count: 1,
        },
      ],
    }).currencies[0].net_amount,
    '-2',
  );
  for (const patch of [
    { subscription_id: undefined },
    { timezone: 'UTC' },
    { end_date: '2028-02-29' },
    { month: '2028-13' },
    { currencies: [summary().currencies[0], summary().currencies[0]] },
    { currencies: [{ ...summary().currencies[0], payment_amount: 1 }] },
  ])
    assert.throws(() => parseBillingSummary({ ...summary(), ...patch }));
});
test('summary exclusive end supports Go year boundaries without changing entry date validation', () => {
  for (const [month, end_date] of [
    ['0001-01', '0001-02-01'],
    ['0099-12', '0100-01-01'],
    ['9999-12', '10000-01-01'],
  ]) {
    assert.equal(
      parseBillingSummary({
        ...summary(),
        month,
        start_date: `${month}-01`,
        end_date,
      }).end_date,
      end_date,
    );
  }
  assert.throws(() =>
    parseBillingValues({ ...billingValues, occurred_on: '10000-01-01' }),
  );
});
test('billing requests omit all/empty filters, preserve source zero and use private abortable requests', async () => {
  let captured: RequestInit | undefined;
  let url = '';
  const signal = new AbortController().signal;
  const api = createBillingAPI('https://api.test/', async (path, init) => {
    url = String(path);
    captured = init;
    return Response.json({ events: [], total: 0, limit: 20, offset: 0 });
  });
  await api.list(
    sid,
    {
      kind: 'all',
      state: 'all',
      month: '',
      source_result_id: rid,
      source_event_index: 0,
      limit: 20,
      offset: 0,
    },
    async () => 'token',
    signal,
  );
  const query = new URL(url).searchParams;
  assert.equal(query.has('kind'), false);
  assert.equal(query.has('state'), false);
  assert.equal(query.has('month'), false);
  assert.equal(query.get('source_event_index'), '0');
  assert.equal(captured?.credentials, 'omit');
  assert.equal(captured?.cache, 'no-store');
  assert.equal(captured?.signal, signal);
  assert.equal(
    new Headers(captured?.headers).get('Authorization'),
    'Bearer token',
  );
});
test('responses must match requested subscription, event, page filters and summary scope', async () => {
  const api = (body: unknown) =>
    createBillingAPI('', async () => Response.json(body));
  const token = async () => 'token';
  await assert.rejects(
    api({ ...billingEntry(), id: sid }).get(sid, eid, token),
    { code: 'dependency_unavailable' },
  );
  await assert.rejects(
    api({ ...billingEntry(), subscription_id: eid }).get(sid, eid, token),
    { code: 'dependency_unavailable' },
  );
  await assert.rejects(
    api({ events: [billingEntry()], total: 1, limit: 20, offset: 0 }).list(
      sid,
      { kind: 'refund', state: 'all', month: '', limit: 20, offset: 0 },
      token,
    ),
    { code: 'dependency_unavailable' },
  );
  await assert.rejects(api(summary()).summary('2028-02', sid, token), {
    code: 'dependency_unavailable',
  });
  await assert.rejects(
    api({
      events: [billingEntry(), billingEntry()],
      total: 2,
      limit: 20,
      offset: 0,
    }).list(
      sid,
      { kind: 'all', state: 'all', month: '', limit: 20, offset: 0 },
      token,
    ),
  );
});
test('duplicate preview binds active candidate values, excludes self and requires a valid fingerprint', async () => {
  const filter = {
    ...billingValues,
    amount: '0001.00',
    exclude_id: null,
    limit: 20,
    offset: 0,
  };
  const token = async () => 'token';
  const api = (body: unknown) =>
    createBillingAPI('', async () => Response.json(body));
  const body = {
    events: [{ ...billingEntry(), amount: '1' }],
    total: 1,
    limit: 20,
    offset: 0,
    token: 'a'.repeat(64),
  };
  assert.equal((await api(body).duplicates(sid, filter, token)).total, 1);
  for (const patch of [
    { token: '' },
    { events: [{ ...billingEntry(), state: 'voided' }] },
    { events: [{ ...billingEntry(), amount: '2' }] },
  ])
    await assert.rejects(
      api({ ...body, ...patch }).duplicates(sid, filter, token),
    );
  await assert.rejects(
    api(body).duplicates(sid, { ...filter, exclude_id: eid }, token),
  );
});
test('create and source mutations send explicit null fields and preserve validated conflict reasons', async () => {
  const calls: RequestInit[] = [];
  const api = createBillingAPI('', async (_path, init) => {
    calls.push(init!);
    return calls.length < 3
      ? Response.json(billingEntry())
      : Response.json(
          { code: 'conflict', reason: 'refund_conflict', request_id: 'trace' },
          { status: 409 },
        );
  });
  const token = async () => 'token';
  await api.create(
    sid,
    {
      ...billingValues,
      request_id: rid,
      confirmed: true,
      source_result_id: null,
      source_event_index: null,
      duplicate_confirmed: false,
      duplicate_token: null,
    },
    token,
  );
  assert.equal(JSON.parse(String(calls[0].body)).duplicate_token, null);
  const source = {
    expected_version: 1,
    source_result_id: rid,
    source_event_index: 0,
    confirmed: true,
  };
  await api.detach(sid, eid, source, token);
  assert.equal(calls[1].method, 'DELETE');
  assert.equal(JSON.parse(String(calls[1].body)).source_event_index, 0);
  await assert.rejects(
    api.attach(sid, eid, source, token),
    (error: unknown) =>
      error instanceof BillingError &&
      error.code === 'conflict' &&
      error.reason === 'refund_conflict',
  );
});
test('token absence, late abort and unknown server errors fail safely', async () => {
  let calls = 0;
  const api = createBillingAPI('', async () => {
    calls++;
    return Response.json(
      { code: 'private', reason: 'secret' },
      { status: 503 },
    );
  });
  await assert.rejects(
    api.get(sid, eid, async () => null),
    { code: 'authentication_required' },
  );
  assert.equal(calls, 0);
  await assert.rejects(
    api.get(sid, eid, async () => 'token'),
    { code: 'dependency_unavailable' },
  );
  const abort = new AbortController();
  await assert.rejects(
    api.get(
      sid,
      eid,
      async () => {
        abort.abort();
        return 'token';
      },
      abort.signal,
    ),
    { name: 'AbortError' },
  );
  assert.equal(calls, 1);
});
test('empty event targets are rejected before any protected request', async () => {
  let calls = 0;
  const api = createBillingAPI('', async () => {
    calls++;
    return Response.json(billingEntry());
  });
  await assert.rejects(
    api.get(sid, '', async () => 't'),
    { code: 'invalid_request' },
  );
  assert.equal(calls, 0);
});
