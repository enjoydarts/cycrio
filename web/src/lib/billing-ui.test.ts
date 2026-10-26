import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
const source = (name: string) =>
  readFile(new URL(`./${name}`, import.meta.url), 'utf8').catch(() => '');
test('billing history is a detail tab and monthly summary remains under subscriptions navigation', async () => {
  const detail = await source('SubscriptionDetail.svelte');
  assert.match(detail, /BillingHistory/);
  assert.match(detail, /tab=history/);
  assert.match(detail, /概要/);
  assert.match(detail, /請求・支払い履歴/);
  assert.match(
    await source('Subscriptions.svelte'),
    /href="\/subscriptions\/summary"/,
  );
  assert.match(
    await readFile(
      new URL('../routes/subscriptions/summary/+page.svelte', import.meta.url),
      'utf8',
    ).catch(() => ''),
    /BillingSummary/,
  );
});
test('registered AI event offers explicit history navigation preserving event zero without writing', async () => {
  const detail = await source('AnalysisDetail.svelte');
  assert.match(detail, /履歴に記録/);
  assert.match(detail, /source_result_id/);
  assert.match(detail, /source_event_index/);
});
test('billing components compile accessibly and mutations stay in controllers', async () => {
  for (const name of [
    'BillingHistory',
    'BillingEditor',
    'BillingSources',
    'BillingTable',
    'BillingSummary',
    'BillingSummaryTable',
  ]) {
    const code = await source(`${name}.svelte`);
    assert.ok(code.length, name);
    const result = compile(code, {
      filename: `${name}.svelte`,
      generate: 'server',
    });
    assert.deepEqual(
      result.warnings.map((w) => w.code),
      [],
      name,
    );
    assert.doesNotMatch(
      code,
      /createAuthController|\.create\(|\.update\(/,
      name,
    );
  }
  const editor = await source('BillingEditor.svelte');
  assert.match(editor, /onBusyChange/);
  assert.match(editor, /内容を確認しました/);
  assert.match(editor, /別の取引/);
  assert.match(editor, /同じ要求で再確認/);
});
test('summary server rendering distinguishes no records, zero and negative exact totals with currency separation', async () => {
  const code = compile(await source('BillingSummaryTable.svelte'), {
    generate: 'server',
  }).js.code.replace(
    /(from\s+|import\s*)(['"])(svelte[^'"]+)\2/g,
    (_m, p, q, path) => `${p}${q}${import.meta.resolve(path)}${q}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const summary = {
    month: '2026-10',
    start_date: '2026-10-01',
    end_date: '2026-11-01',
    timezone: 'Asia/Tokyo',
    subscription_id: null,
    currencies: [],
    invoice_count: 1,
    voided_count: 2,
  };
  const empty = render(Component, { props: { summary } }).body;
  assert.match(empty, /集計対象の支払い・返金はありません/);
  const big = '1000000000000000000000000.000000000001';
  const html = render(Component, {
    props: {
      summary: {
        ...summary,
        currencies: [
          {
            currency: 'JPY',
            payment_amount: big,
            refund_amount: '0',
            net_amount: big,
            payment_count: 2,
            refund_count: 0,
          },
          {
            currency: 'USD',
            payment_amount: '0',
            refund_amount: '1.5',
            net_amount: '-1.5',
            payment_count: 0,
            refund_count: 1,
          },
        ],
      },
    },
  }).body;
  for (const text of [
    big,
    'JPY',
    'USD',
    '-1.5',
    '請求',
    '無効',
    '支払い',
    '返金',
    '差引',
  ])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /NaN|Infinity/);
});
