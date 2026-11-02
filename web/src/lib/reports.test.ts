import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  addAmounts,
  subtractAmounts,
  csvCell,
  reportsCSV,
  upcomingWithin,
  savings,
  serviceBreakdown,
  displayEstimate,
  chartPercent,
} from './reports-helpers.ts';
import {
  createReportsAPI,
  parseReport,
  createReportsController,
} from './reports-api.ts';
import { ApiError } from './api.ts';
import { reportFixture } from './reports-fixtures.ts';

test('decimal calculations retain large values, eight decimal places and zero', () => {
  assert.equal(
    addAmounts(['999999999999999999999999.1', '0.00000001']),
    '999999999999999999999999.10000001',
  );
  assert.equal(subtractAmounts('0', '0.00000001'), '-0.00000001');
  assert.equal(addAmounts(['0', '0']), '0');
});
test('current normalized estimates round to two decimals with an approximation marker only when needed', () => {
  assert.equal(displayEstimate('4083.33333333', 'JPY'), '≈ 4,083.33 JPY');
  assert.equal(
    displayEstimate('999999999999999999999999.999', 'USD'),
    '≈ 1,000,000,000,000,000,000,000,000 USD',
  );
  assert.equal(displayEstimate('0', 'JPY'), '0 JPY');
  assert.equal(displayEstimate('0.005', 'USD'), '≈ 0.01 USD');
  assert.equal(displayEstimate(null, 'JPY'), '金額不明');
});
test('CSV protects formula cells including leading whitespace and quotes all content', () => {
  assert.equal(csvCell(' =1+1'), '"\' =1+1"');
  assert.equal(csvCell('a,"b"'), '"a,""b"""');
  assert.equal(csvCell('-100'), '"\'-100"');
});
test('saved due dates have exclusive 30/90/365 day boundaries', () => {
  const rows = [
    { date: '2026-11-02' },
    { date: '2026-12-01' },
    { date: '2026-12-02' },
  ];
  assert.equal(upcomingWithin(rows, '2026-11-02', 30).length, 2);
});
test('simulation adds selected contracts per currency and never mutates originals', () => {
  const rows = [
    { id: 'a', currency: 'JPY', monthly_amount: '0.1', annual_amount: '1.2' },
    { id: 'b', currency: 'JPY', monthly_amount: '0.2', annual_amount: '2.4' },
    { id: 'c', currency: 'USD', monthly_amount: null, annual_amount: null },
  ];
  assert.deepEqual(savings(rows, ['a', 'b', 'c']), [
    { currency: 'JPY', monthly_amount: '0.3', annual_amount: '3.6' },
  ]);
  assert.equal(rows[0].monthly_amount, '0.1');
});
test('malformed API report is rejected', () => {
  for (const value of [null, {}, { month: '2026-13' }])
    assert.throws(() => parseReport(value), ApiError);
});
test('selected lower bound month permits historical buckets in 1999', () => {
  assert.equal(
    parseReport(reportFixture('2000-01')).history[0].month,
    '1999-02',
  );
});
test('service breakdown groups contracts while separating currencies and unknown amounts', () => {
  const rows = [
    {
      service: 'Cloud',
      currency: 'JPY',
      monthly_amount: '0.1',
      annual_amount: '1.2',
      status: 'active',
    },
    {
      service: 'Cloud',
      currency: 'JPY',
      monthly_amount: '0.2',
      annual_amount: '2.4',
      status: 'active',
    },
    {
      service: 'Cloud',
      currency: 'USD',
      monthly_amount: null,
      annual_amount: null,
      status: 'unknown',
    },
    {
      service: 'Cloud',
      currency: 'JPY',
      monthly_amount: null,
      annual_amount: null,
      status: 'active',
    },
  ];
  assert.deepEqual(serviceBreakdown(rows as never), [
    {
      service: 'Cloud',
      currency: 'JPY',
      monthly_amount: '0.3',
      annual_amount: '3.6',
      count: 3,
      unpriced: 1,
      unknown: 0,
    },
    {
      service: 'Cloud',
      currency: 'USD',
      monthly_amount: null,
      annual_amount: null,
      count: 1,
      unpriced: 1,
      unknown: 1,
    },
  ]);
});
test('service ranking uses exact large decimals within each currency and shares exclude other currencies', () => {
  const row = (
    service: string,
    currency: string,
    monthly_amount: string | null,
  ) => ({
    service,
    currency,
    monthly_amount,
    annual_amount: monthly_amount,
    status: 'active',
  });
  const ranked = serviceBreakdown([
    row('A', 'JPY', '9007199254740992.1'),
    row('B', 'JPY', '9007199254740992.2'),
    row('C', 'USD', '99999999999999999999999'),
    row('D', 'JPY', null),
  ] as never);
  assert.deepEqual(
    ranked.map((x) => x.service),
    ['B', 'A', 'D', 'C'],
  );
  assert.equal(
    chartPercent('9007199254740992.1', '18014398509481984.3'),
    49.99,
  );
  assert.equal(chartPercent('0', '0'), 0);
});
test('CSV exports historical payment/refund/invoice independently with unknowns empty', () => {
  const report = parseReport(reportFixture());
  report.monthly_services = [
    {
      subscription_id: '11111111-1111-4111-8111-111111111111',
      service: '=SUM(A1)',
      plan: null,
      currency: 'JPY',
      payment_amount: '10',
      refund_amount: '2',
      net_amount: '8',
      invoice_amount: null,
      payment_count: 1,
      refund_count: 1,
      invoice_count: 0,
    },
  ];
  const csv = reportsCSV(report);
  assert.ok(csv.startsWith('\uFEFF'));
  assert.ok(csv.includes('"\'=SUM(A1)"'));
  assert.ok(csv.includes('"10","2","8","","1","1","0"'));
});
test('API validates month before requesting and sends authenticated read only request', async () => {
  let calls = 0;
  const api = createReportsAPI('https://example.test/', async (url, init) => {
    calls++;
    assert.equal(String(url), 'https://example.test/v1/reports?month=2026-11');
    assert.equal(init?.method, 'GET');
    assert.equal(
      (init?.headers as Record<string, string>).Authorization,
      'Bearer token',
    );
    return new Response('{}');
  });
  await assert.rejects(
    api.read('2026-13', async () => 'token'),
    ApiError,
  );
  assert.equal(calls, 0);
  await assert.rejects(
    api.read('2026-11', async () => 'token'),
    ApiError,
  );
  assert.equal(calls, 1);
});
test('API accepts a valid report and refuses a report for a different month', async () => {
  const api = createReportsAPI(
    '',
    async () => new Response(JSON.stringify(reportFixture())),
  );
  assert.equal(
    (await api.read('2026-11', async () => 'token')).month,
    '2026-11',
  );
  await assert.rejects(
    api.read('2026-10', async () => 'token'),
    ApiError,
  );
});
test('controller retries transient failure and stops on denied access', async () => {
  let calls = 0,
    denials = 0;
  const api = {
    async read() {
      calls++;
      if (calls === 1) throw new ApiError('dependency_unavailable');
      if (calls === 3) throw new ApiError('access_denied');
      return parseReport(reportFixture());
    },
  };
  const controller = createReportsController(
    api,
    async () => 'token',
    () => {},
    () => denials++,
  );
  await controller.load('2026-11');
  assert.equal(controller.state().status, 'error');
  await controller.load('2026-11');
  assert.equal(controller.state().status, 'ready');
  await controller.load('2026-11');
  assert.equal(controller.state().status, 'denied');
  assert.equal(denials, 1);
  await controller.load('2026-11');
  assert.equal(calls, 3);
});
test('month controller ignores stale responses and does not publish after disposal', async () => {
  const pending: ((v: unknown) => void)[] = [],
    states: unknown[] = [];
  const controller = createReportsController(
    { read: () => new Promise((resolve) => pending.push(resolve)) } as never,
    async () => 'token',
    (v) => states.push(v),
    () => {},
  );
  const a = controller.load('2026-10'),
    b = controller.load('2026-11');
  pending[1]({ month: '2026-11' });
  await b;
  pending[0]({ month: '2026-10' });
  await a;
  assert.equal(controller.state().page?.month, '2026-11');
  controller.dispose();
  const length = states.length;
  await controller.load('2026-12');
  assert.equal(states.length, length);
});
