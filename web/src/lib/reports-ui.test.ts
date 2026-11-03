import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { parseReport } from './reports-api.ts';
import { reportFixture, reportContractFixture } from './reports-fixtures.ts';
import { readFile } from 'node:fs/promises';
import { ratesFixture } from './exchange-rates-fixtures.ts';
const ReportsPanel = (await import('./ReportsPanel.svelte')).default;
const ready = () => ({
  page: parseReport(reportFixture()),
  status: 'ready' as const,
  error: null,
});
test('JPY projection applies to all report tabs and reports rate provenance and exclusions', () => {
  const view = ready();
  view.page.contracts = [reportContractFixture()];
  view.page.currencies = [
    {
      currency: 'USD',
      monthly_amount: '10',
      annual_amount: '120',
      contract_count: 1,
      next_30_days_amount: null,
      next_30_days_count: 0,
    },
  ];
  for (const initialTab of [
    'summary',
    'services',
    'upcoming',
    'savings',
    'monthly',
  ]) {
    const html = render(ReportsPanel, {
      props: {
        view,
        month: '2026-11',
        currentMonth: '2026-11',
        initialTab,
        initialCurrencyMode: 'jpy',
        ratesView: { page: ratesFixture(), status: 'ready', error: null },
      },
    }).body;
    assert.match(html, /最新レートによる参考換算/);
    assert.match(html, /ECB/);
    assert.match(html, /2026-11-02/);
    assert.match(html, /2026-11-03T00:00:00Z/);
    assert.match(html, /対象外/);
    if (initialTab === 'summary') assert.match(html, /1,501.23 JPY/);
  }
});
test('FX error leaves native reports available and hides converted amounts until successful retry', () => {
  const view = ready();
  view.page.currencies = [
    {
      currency: 'USD',
      monthly_amount: '10',
      annual_amount: '120',
      contract_count: 1,
      next_30_days_amount: null,
      next_30_days_count: 0,
    },
  ];
  const props = {
    view,
    month: '2026-11',
    currentMonth: '2026-11',
    ratesView: {
      page: ratesFixture(),
      status: 'error' as const,
      error: 'dependency_unavailable' as const,
    },
  };
  assert.match(render(ReportsPanel, { props }).body, /10 USD/);
  const converted = render(ReportsPanel, {
    props: { ...props, initialCurrencyMode: 'jpy' },
  }).body;
  assert.doesNotMatch(converted, /1,501/);
  assert.match(converted, /為替レートを再取得/);
});
test('zero upcoming count is displayed as no schedule rather than unknown money', () => {
  const view = ready();
  view.page.currencies.push({
    currency: 'JPY',
    monthly_amount: '4083.33333333',
    annual_amount: '49000',
    contract_count: 1,
    next_30_days_amount: null,
    next_30_days_count: 0,
  });
  const html = render(ReportsPanel, {
    props: { view, month: '2026-11', currentMonth: '2026-11' },
  }).body;
  assert.match(html, /予定なし/);
  assert.match(html, /≈ 4,083.33 JPY/);
  assert.doesNotMatch(html, /4,083.33333333/);
});
test('simulation labels explicitly preserve horizontal layout over the daily form label rule', async () => {
  const source = await readFile(
    new URL('./ReportsPanel.svelte', import.meta.url),
    'utf8',
  );
  assert.match(
    source,
    /\.reports \.simulation-choice\s*\{[^}]*flex-direction:\s*row/s,
  );
});
test('report panel renders all five sections without compiler warnings', () => {
  for (const initialTab of [
    'summary',
    'services',
    'upcoming',
    'savings',
    'monthly',
  ]) {
    const html = render(ReportsPanel, {
      props: {
        view: ready(),
        month: '2026-11',
        currentMonth: '2026-11',
        initialTab,
      },
    }).body;
    assert.match(html, /分析・レポート/);
    assert.match(html, /支出サマリー/);
    assert.match(html, /月次レポート/);
    if (initialTab === 'monthly') {
      assert.match(html, /CSVをダウンロード/);
      assert.match(html, /集計月（日本時間）/);
      assert.match(html, /記録はありません/);
    }
    if (initialTab === 'savings')
      assert.match(html, /契約の保存・解約は行いません/);
  }
});
test('historical-only currencies do not appear as unknown current spending', () => {
  const view = ready();
  view.page.currencies.push({
    currency: 'USD',
    monthly_amount: null,
    annual_amount: null,
    contract_count: 0,
    next_30_days_amount: null,
    next_30_days_count: 0,
  });
  const html = render(ReportsPanel, {
    props: { view, month: '2026-11', currentMonth: '2026-11' },
  }).body;
  assert.match(html, /集計できる登録料金がありません/);
  assert.doesNotMatch(html, /USD · 0 契約/);
});
test('missing price and saved billing dates provide links to the relevant contract', () => {
  const view = ready(),
    id = '11111111-1111-4111-8111-111111111111';
  view.page.contracts.push({
    id,
    service: '確認対象',
    plan: null,
    status: 'unknown',
    contract_kind: 'recurring',
    amount: '100',
    currency: null,
    interval_unit: 'month',
    interval_count: 1,
    next_billing_date: null,
    monthly_amount: null,
    annual_amount: null,
    exclusion_reason: 'missing_currency',
  });
  for (const initialTab of ['summary', 'upcoming']) {
    const html = render(ReportsPanel, {
      props: { view, month: '2026-11', currentMonth: '2026-11', initialTab },
    }).body;
    assert.match(html, new RegExp(`href="/subscriptions/${id}"`));
    assert.match(html, initialTab === 'summary' ? /通貨不明/ : /日付未設定/);
    assert.match(html, /<details/);
  }
});
test('loading, errors, denial and disposal hide stale report amounts', () => {
  for (const status of ['loading', 'error', 'denied', 'disposed'] as const) {
    const view = ready();
    view.page.currencies.push({
      currency: 'JPY',
      monthly_amount: '123456789',
      annual_amount: '1481481468',
      contract_count: 1,
      next_30_days_amount: null,
      next_30_days_count: 0,
    });
    const html = render(ReportsPanel, {
      props: {
        view: {
          ...view,
          status,
          error: status === 'error' ? 'dependency_unavailable' : null,
        },
        month: '2026-11',
        currentMonth: '2026-11',
      },
    }).body;
    assert.doesNotMatch(html, /123,456,789/);
    if (status === 'error') {
      assert.match(html, /role="alert"/);
      assert.match(html, /もう一度読み込む/);
    }
  }
});
