import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { overviewFixture } from './overview-fixtures.ts';
import {
  parseOverview,
  createOverviewAPI,
  createOverviewController,
} from './overview-api.ts';

test('overview keeps unknown and zero upcoming amounts without requiring monthly payment totals', () => {
  const value = overviewFixture();
  delete (value as Record<string, unknown>).billing_month;
  const report = parseOverview(value);
  assert.equal(report.upcoming[0].amount, null);
  assert.equal(report.upcoming[1].amount, '0');
  assert.equal(Object.hasOwn(report, 'billing_month'), false);
  assert.equal(report.subscriptions.upcoming, 2);
  assert.equal(report.timezone, 'Asia/Tokyo');
});
test('overview rejects inconsistent totals, periods, ownership shapes and duplicate IDs', () => {
  const changes = [
    (v: any) => v.subscriptions.total++,
    (v: any) => v.subscriptions.upcoming++,
    (v: any) => (v.window_end = '2024-04-01'),
    (v: any) => (v.upcoming[0].next_billing_date = '2024-02-29'),
    (v: any) => (v.upcoming[0].status = 'ended'),
    (v: any) => (v.upcoming[0].amount = '0'),
    (v: any) => (v.upcoming[1].id = v.upcoming[0].id),
    (v: any) => v.connections.push(v.connections[0]),
    (v: any) => (v.connections[0].state = 'connected'),
    (v: any) => v.upcoming.reverse(),
    (v: any) => v.upcoming.pop(),
    (v: any) => (v.attention_messages = -1),
  ];
  for (const change of changes) {
    const value = overviewFixture();
    change(value);
    assert.throws(() => parseOverview(value), ApiError);
  }
});
test('overview GET uses bearer only and accepts no caller-selected owner or query', async () => {
  const calls: { url: string; options?: RequestInit }[] = [];
  const api = createOverviewAPI('https://api.test/', (async (url, options) => {
    calls.push({ url: String(url), options });
    return new Response(JSON.stringify(overviewFixture()));
  }) as typeof fetch);
  await api.read(async () => 'owner-token');
  assert.equal(calls[0].url, 'https://api.test/v1/overview');
  assert.equal(calls[0].options?.method, 'GET');
  assert.equal(calls[0].options?.credentials, 'omit');
  assert.equal(calls[0].options?.body, undefined);
  assert.equal(
    new Headers(calls[0].options?.headers).get('Authorization'),
    'Bearer owner-token',
  );
  await assert.rejects(
    api.read(async () => null),
    ApiError,
  );
  assert.equal(calls.length, 1);
});
test('overview controller discards stale data on denial and ignores late reads after dispose', async () => {
  let denied = 0,
    fail = false;
  const c = createOverviewController(
    {
      read: async () => {
        if (fail) throw new ApiError('access_denied');
        return parseOverview(overviewFixture());
      },
    },
    async () => 'token',
    () => {},
    () => denied++,
  );
  await c.load();
  fail = true;
  await c.load();
  assert.equal(c.state().status, 'denied');
  assert.equal(c.state().page, null);
  assert.equal(denied, 1);
  let resolve!: (v: ReturnType<typeof parseOverview>) => void;
  const late = createOverviewController(
    { read: () => new Promise((done) => (resolve = done)) },
    async () => 'token',
    () => {},
    () => {},
  );
  const pending = late.load();
  late.dispose();
  resolve(parseOverview(overviewFixture()));
  await pending;
  assert.equal(late.state().status, 'disposed');
  assert.equal(late.state().page, null);
});
