import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { subscription } from './subscriptions-fixtures.ts';
import { parseReport } from './reports-api.ts';
import { reportFixture, reportContractFixture } from './reports-fixtures.ts';
import {
  changeEvidence,
  changePreview,
} from './subscription-changes-fixtures.ts';
const Fields = (await import('./SubscriptionLifecycleFields.svelte')).default;
const Events = (await import('./EventFields.svelte')).default;
const List = (await import('./SubscriptionListPanel.svelte')).default;
const Dates = (await import('./BillingDateFields.svelte')).default;
const Reports = (await import('./ReportsPanel.svelte')).default;
const Comparison = (await import('./SubscriptionChangeComparison.svelte'))
  .default;

test('compiled lifecycle fields keep unknown/on/off distinct and expose an explicit trial date', () => {
  for (const auto_renew of [null, true, false]) {
    const html = render(Fields, {
      props: {
        values: { ...subscription(), trial_end_date: '2028-02-29', auto_renew },
        onchange: () => {},
      },
    }).body;
    assert.match(html, /無料期間終了日/);
    assert.match(html, /value="2028-02-29"/);
    assert.match(
      html,
      new RegExp(
        `<option value="${auto_renew === null ? '' : auto_renew ? 'on' : 'off'}" selected`,
      ),
    );
    assert.match(html, /最終の請求がある場合/);
  }
});
test('compiled extraction, list and changes display explicit renewal stop without replacing active status', () => {
  const item = {
    ...subscription(),
    trial_end_date: '2040-02-29',
    auto_renew: false,
    access_end_date: '2040-03-01',
  };
  for (const layout of ['list', 'cards'] as const) {
    const html = render(List, {
      props: { items: [item], layout, onSelect: () => {} },
    }).body;
    assert.match(html, /自動更新停止/);
    assert.match(html, /無料期間終了 2040-02-29/);
    assert.match(html, /利用終了予定 2040-03-01/);
    assert.match(html, /利用中/);
  }
  const event = render(Events, {
    props: {
      event: {
        ...changeEvidence().original_event,
        trial_end_date: '2040-02-29',
        auto_renew: false,
      },
    },
  }).body;
  assert.match(event, /無料期間終了日/);
  assert.match(event, /2040-02-29/);
  assert.match(event, /停止/);
  const p = changePreview();
  p.fields = [
    {
      key: 'auto_renew',
      current: true,
      proposed: false,
      selectable: true,
      reason: '',
    },
  ];
  const diff = render(Comparison, {
    props: { preview: p, onselect: () => {}, onconfirm: () => {} },
  }).body;
  assert.match(diff, /自動更新/);
  assert.match(diff, /有効/);
  assert.match(diff, /停止/);
  assert.doesNotMatch(diff, / checked/);
});
test('compiled automatic billing explanation formats anchor receipt time in Japan time', () => {
  const html = render(Dates, {
    props: {
      values: { ...subscription(), next_billing_mode: 'automatic' },
      receivedAt: '2026-11-03T12:00:00Z',
    },
  }).body;
  assert.match(html, /2026\/11\/03 21:00/);
  assert.doesNotMatch(html, /2026-11-03T12:00:00Z/);
});
test('combined access-end and interval approval explains deferred next-date calculation instead of claiming insufficient facts', () => {
  const preview = changePreview();
  preview.subscription.next_billing_mode = 'automatic';
  preview.fields = [
    {
      key: 'interval',
      current: null,
      proposed: { unit: 'month', count: 1 },
      selectable: true,
      reason: '',
    },
    {
      key: 'access_end_date',
      current: null,
      proposed: '2040-01-01',
      selectable: true,
      reason: '',
    },
  ];
  preview.warnings = [
    {
      code: 'derived_billing_date_requires_lifecycle_review',
      field: 'interval',
    },
  ];
  const html = render(Comparison, {
    props: {
      preview,
      selected: ['interval', 'access_end_date'],
      onselect: () => {},
      onconfirm: () => {},
    },
  }).body;
  assert.match(html, /承認後に更新/);
  assert.doesNotMatch(html, /条件不足のため不明/);
});
test('reports do not ask for a missing date when a known date was deliberately suppressed beyond access end', () => {
  const raw = {
    ...reportFixture(),
    contracts: [
      {
        ...reportContractFixture(),
        access_end_date: '2026-12-01',
        billing_date_suppressed: true,
      },
    ],
  };
  raw.coverage = {
    ...raw.coverage,
    total_contracts: 1,
    open_contracts: 1,
    active_contracts: 1,
    normalized_contracts: 1,
  };
  const report = parseReport(raw);
  assert.equal(report.contracts[0].billing_date_suppressed, true);
  assert.equal(
    parseReport({ ...raw, contracts: [{ ...reportContractFixture() }] })
      .contracts[0].billing_date_suppressed,
    false,
  );
  const html = render(Reports, {
    props: {
      view: { page: report, status: 'ready', error: null },
      initialTab: 'upcoming',
      month: '2026-11',
      currentMonth: '2026-11',
    },
  }).body;
  assert.doesNotMatch(html, /日付を確認する/);
});
