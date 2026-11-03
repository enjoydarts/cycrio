import assert from 'node:assert/strict';
import test from 'node:test';
import {
  parseValues,
  parseSubscription,
  createSubscriptionsAPI,
} from './subscriptions-api.ts';
import { parseProcessingEvent } from './mail-processing-api.ts';
import {
  subscriptionFromEvent,
  createSubscriptionEditor,
} from './subscriptions-controller.ts';
import { subscription, id } from './subscriptions-fixtures.ts';
import {
  changeEvidence,
  changePreview,
} from './subscription-changes-fixtures.ts';
import { parseChangePreview } from './subscription-changes-api.ts';
import { lifecycleLabels, tokyoDate } from './subscription-lifecycle.ts';

test('lifecycle facts preserve unknown, explicit false and legacy omissions without cancellation inference', () => {
  const event = changeEvidence().original_event;
  for (const auto_renew of [null, false, true]) {
    const v = parseValues({
      ...subscription(),
      trial_end_date: '2028-02-29',
      auto_renew,
    });
    assert.equal(v.auto_renew, auto_renew);
    assert.equal(v.trial_end_date, '2028-02-29');
    const e = parseProcessingEvent({
      ...event,
      trial_end_date: '2028-02-29',
      auto_renew,
    });
    assert.equal(subscriptionFromEvent(e).auto_renew, auto_renew);
  }
  assert.equal(parseValues(subscription()).auto_renew, undefined);
  const cancelled = subscriptionFromEvent(
    parseProcessingEvent({ ...event, event_type: 'cancellation' }),
  );
  assert.equal(cancelled.auto_renew, null);
  assert.equal(cancelled.access_end_date, null);
  assert.equal(cancelled.status, 'unknown');
  for (const patch of [
    { auto_renew: 'false' },
    { trial_end_date: '2027-02-29' },
  ]) {
    assert.throws(() => parseValues({ ...subscription(), ...patch }));
    assert.throws(() => parseProcessingEvent({ ...event, ...patch }));
  }
});

test('M2 comparison accepts an explicit boolean false while legacy receipts remain readable', () => {
  const preview = parseChangePreview({
    ...changePreview(),
    fields: [
      {
        key: 'auto_renew',
        current: true,
        proposed: false,
        selectable: true,
        reason: '',
      },
    ],
  });
  assert.equal(preview.fields[0].proposed, false);
});

test('editor preserves suppressed raw manual date and sends explicit clear intent separately from effective date', async () => {
  const bodies: any[] = [];
  const initial = parseSubscription({
    ...subscription(),
    status: 'ended',
    next_billing_mode: 'manual',
    next_billing_date: null,
    manual_next_billing_date: '2027-11-01',
  });
  const api = createSubscriptionsAPI('', async (_url, options) => {
    const body = JSON.parse(String(options?.body));
    bodies.push(body);
    return Response.json({
      ...initial,
      ...body,
      next_billing_date: null,
      manual_next_billing_date: body.next_billing_date,
      version: bodies.length + 1,
    });
  });
  const editor = createSubscriptionEditor(
    api,
    async () => 't',
    () => {},
    () => {},
    () => id,
    initial,
  );
  assert.equal(editor.state().values.next_billing_date, '2027-11-01');
  editor.edit({ plan: '仕事用' });
  editor.confirm(true);
  await editor.save();
  assert.equal(bodies[0].next_billing_date, '2027-11-01');
  assert.equal(bodies[0].next_billing_date_explicit, true);
  editor.edit({ next_billing_date: null });
  editor.confirm(true);
  await editor.save();
  assert.equal(bodies[1].next_billing_date, null);
  assert.equal(bodies[1].next_billing_date_explicit, true);
  assert.equal(Object.hasOwn(bodies[0], 'auto_renew'), false);
});

test('lifecycle badges use inclusive access end and JST boundary without changing stored status or inventing billing', () => {
  const value = {
    ...subscription(),
    status: 'active' as const,
    access_end_date: '2026-11-03',
    trial_end_date: '2026-11-05',
    auto_renew: false,
  };
  assert.equal(tokyoDate(new Date('2026-11-03T15:00:00Z')), '2026-11-04');
  assert.ok(
    lifecycleLabels(value, '2026-11-03').includes('利用終了予定 2026-11-03'),
  );
  assert.ok(
    lifecycleLabels(value, '2026-11-04').includes('利用終了 2026-11-03'),
  );
  assert.ok(lifecycleLabels(value, '2026-11-03').includes('自動更新停止'));
  assert.ok(
    lifecycleLabels(value, '2026-11-03').includes('無料期間終了 2026-11-05'),
  );
  assert.equal(value.status, 'active');
  assert.equal(value.amount, '0');
});
