import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  createSubscriptionsAPI,
  parseSubscription,
  parseValues,
} from './subscriptions-api.ts';
import { id, values, subscription } from './subscriptions-fixtures.ts';
test('subscription exact decimals, nulls, leap days and pairs are validated', () => {
  assert.equal(parseSubscription(subscription()).amount, '0');
  const large = '123456789012345678901234.123456789012';
  assert.equal(parseValues({ ...values, amount: large }).amount, large);
  for (const patch of [
    { amount: 0 },
    { amount: '1.1234567890123' },
    { currency: null },
    { next_billing_date: '2026-02-29' },
    { interval_unit: 'month' },
    { service: 'x'.repeat(201) },
    { status: 'cancelled' },
  ])
    assert.throws(() => parseValues({ ...values, ...patch }), ApiError);
  assert.equal(
    parseValues({
      ...values,
      amount: null,
      currency: null,
      next_billing_date: null,
    }).amount,
    null,
  );
});
test('subscription validation accepts Go decimal and label boundaries without inventing stricter formatting', () => {
  assert.equal(parseValues({ ...values, amount: '0001.00' }).amount, '0001.00');
  assert.equal(
    parseValues({ ...values, service: ' line\nbreak ' }).service,
    'line\nbreak',
  );
  assert.equal(
    [...parseValues({ ...values, service: '😀'.repeat(200) }).service].length,
    200,
  );
  assert.throws(() => parseValues({ ...values, service: '😀'.repeat(201) }));
  assert.throws(() => parseValues({ ...values, service: 'nul\0value' }));
});
test('subscription DTO rejects invalid versions, missing nullable fields, dates and source indices', () => {
  for (const patch of [
    { version: 0 },
    { version: 1.5 },
    { amount: undefined },
    { next_billing_date: '2026-04-31' },
    { updated_at: '2026-02-30T00:00:00Z' },
    { sources: null },
    { sources: [{ result_id: id, event_index: 32, original_event: {} }] },
  ])
    assert.throws(
      () => parseSubscription({ ...subscription(), ...patch }),
      ApiError,
    );
});
test('updates and source links reject a response for another subscription identity', async () => {
  const other = '22222222-2222-4222-8222-222222222222';
  const api = createSubscriptionsAPI(
    '',
    async () => new Response(JSON.stringify({ ...subscription(), id: other })),
  );
  await assert.rejects(
    api.update(
      id,
      { ...values, expected_version: 1, confirmed: true },
      async () => 'token',
    ),
    { code: 'dependency_unavailable' },
  );
  await assert.rejects(
    api.attach(
      id,
      {
        expected_version: 1,
        source_result_id: id,
        source_event_index: 0,
        confirmed: true,
      },
      async () => 'token',
    ),
    { code: 'dependency_unavailable' },
  );
});
test('subscription API builds known fields, auth/no-store, index zero, and reports duplicate source conflict', async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  const api = createSubscriptionsAPI('https://api.test/', async (url, init) => {
    calls.push({ url: String(url), init });
    return new Response(JSON.stringify(subscription()), {
      status: calls.length === 2 ? 409 : 201,
    });
  });
  await api.create(
    {
      ...values,
      request_id: id,
      source_result_id: id,
      source_event_index: 0,
      confirmed: true,
      secret: 'omit',
    } as never,
    async () => 'token',
  );
  const c = calls[0];
  assert.equal(c.url, 'https://api.test/v1/subscriptions');
  assert.equal(c.init?.credentials, 'omit');
  assert.equal(c.init?.cache, 'no-store');
  assert.equal(
    new Headers(c.init?.headers).get('Authorization'),
    'Bearer token',
  );
  assert.equal(JSON.parse(String(c.init?.body)).source_event_index, 0);
  assert.equal(JSON.parse(String(c.init?.body)).secret, undefined);
  await assert.rejects(
    api.attach(
      id,
      {
        expected_version: 1,
        source_result_id: id,
        source_event_index: 0,
        confirmed: true,
      },
      async () => 'token',
    ),
    { code: 'conflict' },
  );
});
