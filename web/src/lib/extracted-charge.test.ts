import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { parseProcessingEvent } from './mail-processing-api.ts';
import { extractedCharge } from './extracted-charge.ts';
import {
  createSubscriptionEditor,
  subscriptionFromEvent,
} from './subscriptions-controller.ts';
import { parseValues, type SubscriptionsAPI } from './subscriptions-api.ts';
const Hint = (await import('./ExtractedChargeHint.svelte')).default;
const event = (patch = {}) =>
  parseProcessingEvent({
    service: 'Test Service',
    plan: null,
    event_type: 'payment',
    amount: '1000',
    currency: 'JPY',
    billed_at: null,
    period_start: null,
    period_end: null,
    next_billing_at: null,
    interval_unit: null,
    interval_count: null,
    ...patch,
  });
test('transaction amount requires explicit use and edits reset confirmation', () => {
  const e = event();
  assert.equal(subscriptionFromEvent(e).amount, null);
  const controller = createSubscriptionEditor(
    {} as SubscriptionsAPI,
    async () => 't',
    () => {},
    () => {},
  );
  controller.edit(subscriptionFromEvent(e));
  controller.confirm(true);
  controller.edit(extractedCharge(e, 'unknown')!);
  assert.equal(controller.state().values.amount, '1000');
  assert.equal(controller.state().values.currency, 'JPY');
  assert.equal(controller.state().confirmed, false);
});
test('unknown transaction currency remains unknown and cannot be saved', () => {
  const e = event({ currency: null });
  const patch = extractedCharge(e, 'unknown');
  assert.deepEqual(patch, { amount: '1000', currency: null });
  assert.throws(() => parseValues({ ...subscriptionFromEvent(e), ...patch }));
});
test('fixed terms, refunds, cancellations and known ongoing fees cannot use transaction charge', () => {
  for (const e of [
    event({ contract_kind: 'fixed_term' }),
    event({ event_type: 'refund' }),
    event({ event_type: 'cancellation' }),
    event({ recurring_amount: '1500', recurring_currency: 'JPY' }),
    event({ amount: null }),
  ])
    assert.equal(extractedCharge(e, 'unknown'), null);
  assert.equal(extractedCharge(event(), 'fixed_term'), null);
});
test('hint shows extracted reference, warning and safe action, while unknown currency asks for selection', () => {
  const html = render(Hint, {
    props: { event: event(), values: subscriptionFromEvent(event()) },
  }).body;
  assert.match(html, /取引金額.*1000 JPY/s);
  assert.match(html, /今回の請求額であり、通常料金と異なる場合/);
  assert.match(html, /type="button"/);
  assert.match(html, /抽出された請求額を使う/);
  const partial = event({ recurring_amount: '1500', recurring_currency: null });
  const unknown = render(Hint, {
    props: {
      event: partial,
      values: {
        ...subscriptionFromEvent(partial),
        amount: '1500',
        currency: null,
      },
    },
  }).body;
  assert.match(unknown, /通貨を選択してください/);
  assert.doesNotMatch(unknown, /抽出された請求額を使う/);
  for (const e of [
    event({ event_type: 'refund' }),
    event({ contract_kind: 'fixed_term' }),
  ])
    assert.doesNotMatch(
      render(Hint, { props: { event: e, values: subscriptionFromEvent(e) } })
        .body,
      /抽出された請求額を使う/,
    );
});
