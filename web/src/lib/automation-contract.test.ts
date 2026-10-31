import assert from 'node:assert/strict';
import test from 'node:test';
import { parseAnalysisResult, createAnalysisAPI } from './analysis-api.ts';
import { parseSubscription } from './subscriptions-api.ts';
import {
  parsePolicyState,
  parseProcessingPreview,
  createMailProcessingAPI,
} from './mail-processing-api.ts';
import {
  processingResult,
  processingIDs as ids,
  processingPolicy,
  processingPreview,
  processingRun,
} from './mail-processing-fixtures.ts';
import { subscription } from './subscriptions-fixtures.ts';

test('automatic results retain no-events and current registration metadata', () => {
  const result = parseAnalysisResult({
    ...processingResult({ state: 'succeeded', events: [] }),
    account_id: ids.account,
    account_label: '受信',
    registrations: [],
    automation_state: 'no_events',
    automation: [],
    billing_automation: [],
  });
  assert.equal(result.automation_state, 'no_events');
  assert.deepEqual(result.automation, []);
  assert.deepEqual(result.billing_automation, []);
});
test('automatic contracts retain unknown status and origin without inventing a contract price', () => {
  const result = parseSubscription({
    ...subscription(),
    status: 'unknown',
    provenance: 'automatic',
    amount: null,
    currency: null,
  });
  assert.equal(result.status, 'unknown');
  assert.equal(result.provenance, 'automatic');
  assert.equal(result.amount, null);
});
test('whole-email permission survives public preview and admin parsing; legacy defaults to false', () => {
  const state = processingPolicy();
  assert.equal(
    parsePolicyState({
      ...state,
      policy: { ...state.policy, full_email_allowed: true },
    }).policy?.full_email_allowed,
    true,
  );
  assert.equal(
    parseProcessingPreview(
      { ...processingPreview(), full_email_allowed: true },
      ids.account,
    ).full_email_allowed,
    true,
  );
  assert.equal(
    parseProcessingPreview(
      { ...processingPreview(), full_email_allowed: undefined },
      ids.account,
    ).full_email_allowed,
    false,
  );
});
test('manual request preserves explicit false and omitted legacy requests while allowing whole email', async () => {
  const bodies: Record<string, unknown>[] = [];
  const api = createMailProcessingAPI('', async (_url, init) => {
    bodies.push(JSON.parse(String(init?.body)));
    return Response.json(processingRun());
  });
  const base = {
    expected_version: 1,
    runtime_version: 2,
    profile_id: ids.profile,
    policy_version: 3,
    request_id: ids.request,
    max_messages: 1,
    confirmed: true,
  };
  await api.start(ids.account, { ...base, full_email: true }, async () => 't');
  await api.start(ids.account, { ...base, full_email: false }, async () => 't');
  await api.start(ids.account, base, async () => 't');
  assert.equal(bodies[0].full_email, true);
  assert.equal(bodies[1].full_email, false);
  assert.equal(Object.hasOwn(bodies[2], 'full_email'), false);
});
test('attention uses server-wide state and total rather than a related-only client filter', async () => {
  let url = '';
  const api = createAnalysisAPI('', async (u) => {
    url = String(u);
    return Response.json({ results: [], total: 123, limit: 20, offset: 0 });
  });
  const result = await api.results(
    { account_id: '', state: 'all', review: 'attention', limit: 20, offset: 0 },
    async () => 't',
  );
  assert.match(url, /review=attention/);
  assert.equal(result.total, 123);
});
test('an extracted charge is offered as a price proposal for explicit owner confirmation', async () => {
  const module = await import('./subscriptions-controller.ts');
  assert.equal(typeof module.subscriptionFromEvent, 'function');
  const event = {
    service: 'A',
    plan: null,
    event_type: 'payment',
    amount: '1200',
    currency: 'JPY',
    interval_unit: null,
    interval_count: null,
    next_billing_at: null,
  };
  const values = module.subscriptionFromEvent(event as never);
  assert.equal(values.amount, '1200');
  assert.equal(values.currency, 'JPY');
  assert.equal(values.status, 'unknown');
  const refund = module.subscriptionFromEvent({
    ...event,
    event_type: 'refund',
  } as never);
  assert.equal(refund.amount, null);
  assert.equal(refund.currency, null);
});
