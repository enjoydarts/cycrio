import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { parseOverview } from './overview-api.ts';
import { overviewFixture } from './overview-fixtures.ts';
import { parseReport } from './reports-api.ts';
import { reportContractFixture, reportFixture } from './reports-fixtures.ts';
import { subscription } from './subscriptions-fixtures.ts';
const SubscriptionNameModal = (await import('./SubscriptionNameModal.svelte'))
  .default;
const OverviewPanel = (await import('./OverviewPanel.svelte')).default;
const ReportsPanel = (await import('./ReportsPanel.svelte')).default;

test('name modal prefills one editable display name, canonical context and explicit save and cancel', () => {
  const html = render(SubscriptionNameModal, {
    props: {
      subscription: {
        ...subscription(),
        service: 'Netflix',
        display_name: '家族用',
      },
      base: '',
      token: async () => 't',
      onclose() {},
      onSaved() {},
      onDenied() {},
      onFrozen() {},
      onReload() {},
    },
  }).body;
  assert.equal([...html.matchAll(/<input/g)].length, 1);
  assert.match(html, /value="家族用"/);
  assert.match(html, /元のサービス名: Netflix/);
  assert.match(html, /空欄で保存すると元のサービス名に戻ります/);
  assert.match(
    html.replace(/<!--[\s\S]*?-->/g, ''),
    /type="submit"[^>]*>[^<]*保存/,
  );
  assert.match(html, /キャンセル/);
});

test('home parser and upcoming presentation preserve display name and original service', () => {
  const raw = overviewFixture();
  const page = parseOverview({
    ...raw,
    upcoming: raw.upcoming.map((row) => ({
      ...row,
      service: 'Netflix',
      display_name: '家族用',
    })),
  });
  assert.equal(page.upcoming[0].display_name, '家族用');
  const html = render(OverviewPanel, {
    props: { view: { page, status: 'ready', error: null } },
  }).body;
  assert.match(html, /家族用/);
  assert.match(html, /Netflix/);
  assert.match(html, /service-logos\/netflix.svg/);
});

test('report individual rows use aliases while service aggregation and logos keep canonical names', () => {
  const contract = {
    ...reportContractFixture(),
    service: 'Netflix',
    display_name: '家族用',
  };
  const raw = reportFixture();
  const page = parseReport({
    ...raw,
    contracts: [contract],
    upcoming: [
      {
        subscription_id: contract.id,
        service: contract.service,
        display_name: contract.display_name,
        plan: null,
        date: raw.as_of,
        amount: '10',
        currency: 'USD',
        annual_renewal: false,
        status: 'active',
      },
    ],
    monthly_services: [
      {
        subscription_id: contract.id,
        service: contract.service,
        display_name: contract.display_name,
        plan: null,
        currency: 'USD',
        payment_amount: '10',
        refund_amount: null,
        net_amount: '10',
        invoice_amount: null,
        payment_count: 1,
        refund_count: 0,
        invoice_count: 0,
      },
    ],
    coverage: {
      ...raw.coverage,
      total_contracts: 1,
      open_contracts: 1,
      active_contracts: 1,
      normalized_contracts: 1,
    },
  });
  for (const initialTab of ['upcoming', 'savings', 'monthly', 'services']) {
    const html = render(ReportsPanel, {
      props: {
        view: { page, status: 'ready', error: null },
        month: raw.month,
        currentMonth: raw.month,
        initialTab,
      },
    }).body;
    assert.match(html, /Netflix/);
    assert.match(html, /service-logos\/netflix.svg/);
    if (initialTab === 'services') assert.doesNotMatch(html, /家族用/);
    else assert.match(html, /家族用/);
  }
});
