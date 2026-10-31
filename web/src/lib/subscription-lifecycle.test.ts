import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { parseProcessingEvent } from './mail-processing-api.ts';
import { parseValues } from './subscriptions-api.ts';
import {
  subscriptionFromEvent,
  createSubscriptionEditor,
} from './subscriptions-controller.ts';
import { values, subscription } from './subscriptions-fixtures.ts';
import type { SubscriptionsAPI } from './subscriptions-api.ts';
import {
  parseSchedulePreview,
  createScheduleAPI,
  ScheduleError,
} from './schedule-api.ts';
import { parseScheduleHistoryPreview } from './schedule-history-api.ts';
import { historyPreview, historyRule } from './schedule-history-fixtures.ts';
import { createApprovalAPI } from './approval-api.ts';
import {
  createSubscriptionsAPI,
  parseSubscription,
} from './subscriptions-api.ts';
const legacy = {
  service: '会員',
  plan: null,
  event_type: 'payment',
  amount: '1000',
  currency: 'JPY',
  billed_at: '2026-10-31',
  period_start: null,
  period_end: null,
  next_billing_at: '2026-11-30',
  interval_unit: 'month',
  interval_count: 1,
};
test('old events default lifecycle facts without turning transaction price into contract price', () => {
  const event = parseProcessingEvent(legacy);
  assert.equal(event.recurring_amount, null);
  assert.equal(event.contract_kind, null);
  const draft = subscriptionFromEvent(event);
  assert.equal(draft.amount, null);
  assert.equal(draft.currency, null);
  assert.equal(draft.contract_kind, 'unknown');
});
test('explicit regular price survives prorated transaction while incomplete pair stays unknown', () => {
  const event = parseProcessingEvent({
    ...legacy,
    recurring_amount: '1500',
    recurring_currency: 'JPY',
    contract_kind: 'recurring',
  });
  assert.equal(subscriptionFromEvent(event).amount, '1500');
  assert.equal(
    subscriptionFromEvent({ ...event, recurring_currency: null }).amount,
    null,
  );
});
test('nameless cancellation retains independent dates and unknown state', () => {
  const event = parseProcessingEvent({
    ...legacy,
    service: null,
    event_type: 'cancellation',
    effective_date: '2026-10-31',
    access_end_date: '2026-11-30',
  });
  const draft = subscriptionFromEvent(event);
  assert.equal(draft.service, '');
  assert.equal(draft.status, 'unknown');
  assert.equal(draft.effective_date, '2026-10-31');
  assert.equal(draft.access_end_date, '2026-11-30');
  assert.throws(() => parseValues(draft));
});
test('fixed term draft clears recurring fields and editor changes clear prior schedule', () => {
  const draft = subscriptionFromEvent(
    parseProcessingEvent({
      ...legacy,
      contract_kind: 'fixed_term',
      recurring_amount: '1500',
      recurring_currency: 'JPY',
    }),
  );
  for (const field of [
    'amount',
    'currency',
    'interval_unit',
    'interval_count',
    'next_billing_date',
  ] as const)
    assert.equal(draft[field], null);
  const c = createSubscriptionEditor(
    {} as SubscriptionsAPI,
    async () => 't',
    () => {},
    () => {},
    undefined,
    subscription(),
  );
  c.edit({ contract_kind: 'fixed_term' });
  assert.equal(c.state().values.next_billing_date, null);
  assert.equal(c.state().values.amount, null);
  assert.throws(() => parseValues({ ...values, contract_kind: 'fixed_term' }));
  assert.equal(
    parseValues({ ...values, contract_kind: undefined }).contract_kind,
    'unknown',
  );
});
test('new events validate exact facts and reject malformed values', () => {
  for (const event_type of ['payment_failed', 'payment_reminder', 'suspension'])
    assert.equal(
      parseProcessingEvent({ ...legacy, event_type }).event_type,
      event_type,
    );
  for (const patch of [
    { recurring_amount: '-1' },
    { recurring_currency: 'XYZ' },
    { contract_kind: 'annual' },
    { effective_date: '2026-02-30' },
    { access_end_date: 'tomorrow' },
  ])
    assert.throws(() => parseProcessingEvent({ ...legacy, ...patch }));
});
test('event UI separates transaction and regular price and shows unknown service and lifecycle dates', async () => {
  const fields = await readFile(
    new URL('./EventFields.svelte', import.meta.url),
    'utf8',
  );
  for (const label of [
    '取引金額',
    '通常継続料金',
    'サービス名未確認',
    '効力日',
    '利用終了日',
  ])
    assert.ok(fields.includes(label), label);
  const form = await readFile(
    new URL('./SubscriptionForm.svelte', import.meta.url),
    'utf8',
  );
  assert.match(form, /contract_kind/);
  assert.match(form, /access_end_date/);
});
test('fixed term schedule responses retain the explicit blocking reason in preview and history', () => {
  const s = {
    ...subscription(),
    contract_kind: 'fixed_term',
    amount: null,
    currency: null,
    interval_unit: null,
    interval_count: null,
    next_billing_date: null,
  };
  assert.equal(
    parseSchedulePreview({
      subscription: s,
      rule: historyRule,
      as_of: '2026-10-26',
      timezone: 'Asia/Tokyo',
      candidate_date: null,
      reason: 'fixed_term',
      algorithm_version: 'calendar-v1',
      overdue: false,
    }).reason,
    'fixed_term',
  );
  const history = historyPreview();
  assert.throws(() =>
    parseScheduleHistoryPreview({
      ...history,
      subscription: s,
      reason: 'fixed_term',
      candidates: history.candidates.map((c) => ({
        ...c,
        candidate_date: null,
        reason: 'fixed_term',
      })),
    }),
  );
});
test('fixed term schedule conflict is shown as a precise safe reason', async () => {
  const api = createScheduleAPI(
    '',
    async () =>
      new Response(JSON.stringify({ reason: 'fixed_term' }), { status: 409 }),
  );
  await assert.rejects(
    api.save(
      subscription().id,
      {
        ...historyRule,
        request_id: subscription().id,
        expected_version: 1,
        expected_as_of: '2026-10-26',
        expected_candidate: '2026-10-31',
        confirmed: true,
      },
      async () => 't',
    ),
    (error: unknown) =>
      error instanceof ScheduleError && error.reason === 'fixed_term',
  );
});
test('fixed term history accepts the backend empty blocked snapshot without claiming evidence', () => {
  const history = historyPreview();
  const s = {
    ...subscription(),
    contract_kind: 'fixed_term',
    amount: null,
    currency: null,
    next_billing_date: null,
  };
  const parsed = parseScheduleHistoryPreview({
    ...history,
    subscription: s,
    reason: 'fixed_term',
    status: 'insufficient',
    token: '',
    counts: null,
    samples: [],
    candidates: [],
    entries: [],
  });
  assert.equal(parsed.reason, 'fixed_term');
  assert.equal(parsed.counts, null);
});
test('nameless cancellation remains selectable as a candidate with no mail contents retained', async () => {
  const api = createApprovalAPI('', async () =>
    Response.json({
      candidates: [
        {
          result_id: subscription().id,
          event_index: 0,
          account_id: subscription().id,
          account_label: '個人',
          mail_count: 1,
          event: {
            ...legacy,
            service: null,
            event_type: 'cancellation',
            body: 'private',
          },
          body: 'private',
        },
      ],
      total: 1,
      limit: 20,
      offset: 0,
    }),
  );
  const page = await api.candidates(
    { account_id: '', limit: 20, offset: 0 },
    async () => 't',
  );
  assert.equal(page.candidates.length, 1);
  assert.equal(page.candidates[0].event.service, null);
  assert.equal('body' in page.candidates[0].event, false);
});
test('subscription APIs default legacy lifecycle and send confirmed fields on create and update', async () => {
  const old = { ...subscription() };
  delete old.contract_kind;
  delete old.effective_date;
  delete old.access_end_date;
  assert.equal(parseSubscription(old).contract_kind, 'unknown');
  assert.equal(parseSubscription(old).access_end_date, null);
  const bodies: Record<string, unknown>[] = [];
  const api = createSubscriptionsAPI('', async (_url, init) => {
    const body = JSON.parse(String(init?.body));
    bodies.push(body);
    return Response.json({ ...subscription(), ...body });
  });
  const lifecycle = {
    ...values,
    contract_kind: 'recurring' as const,
    effective_date: '2026-10-31',
    access_end_date: '2026-11-30',
  };
  await api.create(
    {
      ...lifecycle,
      request_id: subscription().id,
      source_result_id: null,
      source_event_index: null,
      confirmed: true,
    },
    async () => 't',
  );
  await api.update(
    subscription().id,
    { ...lifecycle, expected_version: 1, confirmed: true },
    async () => 't',
  );
  for (const body of bodies) {
    assert.equal(body.contract_kind, 'recurring');
    assert.equal(body.effective_date, '2026-10-31');
    assert.equal(body.access_end_date, '2026-11-30');
  }
});
