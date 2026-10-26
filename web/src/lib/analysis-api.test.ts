import assert from 'node:assert/strict';
import test from 'node:test';
import { createAnalysisAPI, parseAnalysisResult } from './analysis-api.ts';
import {
  processingResult,
  processingIDs as ids,
} from './mail-processing-fixtures.ts';
test('analysis keeps event zero registration and rejects invalid event references', () => {
  const event = {
    service: 'A',
    plan: null,
    event_type: 'payment',
    amount: '0',
    currency: 'JPY',
    billed_at: null,
    period_start: null,
    period_end: null,
    next_billing_at: null,
    interval_unit: null,
    interval_count: null,
  };
  const row = {
    ...processingResult({
      state: 'succeeded',
      classification: 'subscription_related',
      events: [event] as never,
    }),
    account_id: ids.account,
    account_label: '受信',
    registrations: [{ event_index: 0, subscription_id: ids.result }],
  };
  assert.equal(parseAnalysisResult(row).registrations[0].event_index, 0);
  assert.throws(() =>
    parseAnalysisResult({
      ...row,
      registrations: [{ event_index: 1, subscription_id: ids.result }],
    }),
  );
  assert.throws(() =>
    parseAnalysisResult({
      ...row,
      registrations: [...row.registrations, ...row.registrations],
    }),
  );
});
test('valid independently nullable AI fields are retained rather than rejected by contract pair validation', () => {
  const events = [
    {
      service: 'A',
      plan: null,
      event_type: 'payment',
      amount: null,
      currency: 'JPY',
      billed_at: null,
      period_start: null,
      period_end: null,
      next_billing_at: null,
      interval_unit: 'month',
      interval_count: null,
    },
  ];
  const row = {
    ...processingResult({
      state: 'succeeded',
      classification: 'subscription_related',
      events: events as never,
    }),
    account_id: ids.account,
    account_label: '受信',
    registrations: [],
  };
  const result = parseAnalysisResult(row);
  assert.equal(result.events[0].currency, 'JPY');
  assert.equal(result.events[0].amount, null);
  assert.equal(result.events[0].interval_unit, 'month');
  assert.equal(result.events[0].interval_count, null);
});
test('paginated analysis API preserves total and forbids invalid review/state combinations', async () => {
  let seen = '';
  let calls = 0;
  const api = createAnalysisAPI('', async (url) => {
    calls++;
    seen = String(url);
    return new Response(
      JSON.stringify({ results: [], total: 321, limit: 20, offset: 120 }),
    );
  });
  const p = await api.results(
    {
      account_id: '',
      state: 'related',
      review: 'pending',
      limit: 20,
      offset: 120,
    },
    async () => 't',
  );
  assert.equal(p.total, 321);
  assert.match(seen, /offset=120/);
  await assert.rejects(
    api.results(
      {
        account_id: '',
        state: 'failed',
        review: 'pending',
        limit: 20,
        offset: 0,
      },
      async () => 't',
    ),
    { code: 'invalid_request' },
  );
  assert.equal(calls, 1);
});
