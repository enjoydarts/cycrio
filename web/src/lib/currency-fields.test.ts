import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { parseValues } from './subscriptions-api.ts';
import {
  blankSubscription,
  subscriptionFromEvent,
} from './subscriptions-controller.ts';
import { subscription } from './subscriptions-fixtures.ts';
import { createSubscriptionEditor } from './subscriptions-controller.ts';
const CurrencySelect = (await import('./CurrencySelect.svelte')).default;
const BillingDateFields = (await import('./BillingDateFields.svelte')).default;
const SubscriptionListPanel = (await import('./SubscriptionListPanel.svelte'))
  .default;
const OverviewPanel = (await import('./OverviewPanel.svelte')).default;
import { parseOverview } from './overview-api.ts';
import { overviewFixture } from './overview-fixtures.ts';
test('currency selector offers unknown and JPY/USD/EUR while retaining an existing other currency', () => {
  const html = render(CurrencySelect, { props: { value: 'GBP' } }).body;
  for (const currency of ['JPY', 'USD', 'EUR', 'GBP'])
    assert.match(html, new RegExp(`value="${currency}"`));
  assert.match(html, /不明/);
  assert.match(html, /<select/);
  assert.doesNotMatch(html, /<input/);
});
test('mode input remains backward compatible and rejects invalid mode', () => {
  const legacy = { ...blankSubscription(), service: 'Test' };
  delete legacy.next_billing_mode;
  assert.equal(parseValues(legacy).next_billing_mode, undefined);
  assert.equal(
    parseValues({ ...legacy, next_billing_mode: 'automatic' })
      .next_billing_mode,
    'automatic',
  );
  assert.throws(() => parseValues({ ...legacy, next_billing_mode: 'bogus' }));
});
test('manual dates stay editable while automatic dates are marked estimated with reception/cycle hint', () => {
  const automatic = render(BillingDateFields, {
    props: {
      values: {
        ...blankSubscription(),
        next_billing_mode: 'automatic',
        next_billing_date: '2026-12-01',
      },
      receivedAt: '2026-11-01T00:00:00Z',
    },
  }).body;
  assert.match(automatic, /推定/);
  assert.match(automatic, /受信日/);
  assert.match(automatic, /周期/);
  assert.match(automatic, /2026-12-01/);
  assert.doesNotMatch(automatic, /type="date"/);
  const manual = render(BillingDateFields, {
    props: { values: subscription() },
  }).body;
  assert.match(manual, /type="date"/);
});
test('source-backed drafts default to automatic and existing manual dates survive editing', () => {
  const api = {} as Parameters<typeof createSubscriptionEditor>[0];
  const source = {
    result_id: '11111111-1111-4111-8111-111111111111',
    event_index: 0,
  };
  const c = createSubscriptionEditor(
    api,
    async () => 't',
    () => {},
    () => {},
    () => '11111111-1111-4111-8111-111111111111',
    null,
    source,
  );
  assert.equal(c.state().values.next_billing_mode, 'automatic');
  const existing = subscription();
  existing.next_billing_mode = 'manual';
  existing.next_billing_date = '2020-01-01';
  const edit = createSubscriptionEditor(
    api,
    async () => 't',
    () => {},
    () => {},
    () => '11111111-1111-4111-8111-111111111111',
    existing,
  );
  edit.edit({ plan: 'updated' });
  assert.equal(edit.state().values.next_billing_date, '2020-01-01');
  assert.equal(edit.state().values.next_billing_mode, 'manual');
});
test('automatic event draft does not present an extracted date as the received-date calculation', () => {
  const event = {
    ...subscription().sources[0]?.original_event,
    service: 'Service',
    plan: null,
    contract_kind: 'recurring',
    recurring_amount: '1',
    recurring_currency: 'USD',
    interval_unit: 'month',
    interval_count: 1,
    next_billing_at: '2026-12-01',
  };
  const draft = subscriptionFromEvent(event as never);
  assert.equal(draft.next_billing_mode, 'automatic');
  assert.equal(draft.next_billing_date, null);
});
test('list and home dates distinguish automatic estimates from manual dates', () => {
  const item = {
    ...subscription(),
    next_billing_mode: 'automatic' as const,
    next_billing_date: '2026-11-03',
  };
  const html = render(SubscriptionListPanel, {
    props: { items: [item], onSelect: () => {} },
  }).body;
  assert.match(html, /推定/);
  const page = parseOverview(overviewFixture());
  page.upcoming[0].next_billing_mode = 'automatic';
  assert.match(
    render(OverviewPanel, {
      props: { view: { page, status: 'ready', error: null } },
    }).body,
    /推定/,
  );
});
