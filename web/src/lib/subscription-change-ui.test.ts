import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { changePreview } from './subscription-changes-fixtures.ts';
import type { ScheduledPreview } from './subscription-scheduled-changes-api.ts';
const SubscriptionChangeComparison = (
  await import('./SubscriptionChangeComparison.svelte')
).default;

test('compiled change comparison renders independent current/proposed values and source with all approval boxes initially unchecked', () => {
  const html = render(SubscriptionChangeComparison, {
    props: {
      preview: changePreview(),
      onselect: () => {},
      onconfirm: () => {},
    },
  }).body;
  assert.match(html, /現在/);
  assert.match(html, /変更候補/);
  assert.match(html, /data-label="現在"[^>]*>0/);
  assert.match(html, /data-label="変更候補"[^>]*>9/);
  assert.match(html, /元の抽出情報を確認/);
  assert.match(html, /通常継続料金/);
  assert.match(html, /USD/);
  assert.equal((html.match(/type="checkbox"/g) ?? []).length, 3);
  assert.doesNotMatch(html, / checked/);
});

test('scheduled comparison can require separate date consent before final confirmation without blocking field selection', () => {
  const html = render(SubscriptionChangeComparison, {
    props: {
      preview: changePreview(),
      selected: ['amount'],
      confirmationDisabled: true,
      onselect() {},
      onconfirm() {},
    },
  }).body;
  const boxes = [...html.matchAll(/<input[^>]*type="checkbox"[^>]*>/g)].map(
    (m) => m[0],
  );
  assert.doesNotMatch(boxes[0], /disabled/);
  assert.match(boxes[2], /disabled/);
});

test('scheduled interval review defers the billing projection until application while immediate review retains it', () => {
  const p: ScheduledPreview = {
    ...changePreview(),
    scheduled_for: '2027-01-01',
  };
  p.subscription.next_billing_mode = 'automatic';
  p.fields = [
    {
      key: 'interval',
      current: { unit: 'month', count: 1 },
      proposed: { unit: 'year', count: 1 },
      selectable: true,
      reason: '',
    },
  ];
  p.derived_next_billing_date = '2026-12-01';
  const props = {
    preview: p,
    selected: ['interval'] as const,
    onselect() {},
    onconfirm() {},
  };
  const scheduled = render(SubscriptionChangeComparison, {
    props: { ...props, selected: [...props.selected], deferred: true },
  }).body;
  assert.match(scheduled, /次回請求日の推定は、予約の反映時に再計算します。/);
  assert.doesNotMatch(scheduled, /2026-12-01|変更後の次回請求日（推定）/);
  const immediate = render(SubscriptionChangeComparison, {
    props: { ...props, selected: [...props.selected] },
  }).body;
  assert.match(immediate, /変更後の次回請求日（推定）：2026-12-01/);
  assert.doesNotMatch(immediate, /予約の反映時に再計算/);
});

test('compiled comparison blocks ambiguous/future fields and explains derived and manual dates', () => {
  const p = changePreview();
  p.subscription.next_billing_mode = 'automatic';
  p.fields = [
    {
      key: 'interval',
      current: null,
      proposed: { unit: 'year', count: 1 },
      selectable: true,
      reason: '',
    },
    {
      key: 'effective_date',
      current: null,
      proposed: '2027-01-01',
      selectable: false,
      reason: 'future_effective',
    },
    {
      key: 'next_billing_date',
      current: null,
      proposed: '2027-02-01',
      selectable: true,
      reason: '',
    },
  ];
  p.derived_next_billing_date = '2027-02-01';
  p.warnings = [{ code: 'cancellation_requires_review' }];
  const html = render(SubscriptionChangeComparison, {
    props: {
      preview: p,
      selected: ['interval', 'next_billing_date'],
      onselect: () => {},
      onconfirm: () => {},
    },
  }).body;
  assert.match(html, /将来の効力日/);
  assert.match(html, /「効力日に予約する」/);
  assert.match(html, /契約状態や利用終了日は自動変更しません/);
  assert.match(html, /日付の設定は手動指定/);
  assert.doesNotMatch(html, /変更後の次回請求日（推定）/);
  assert.match(html, /2027-02-01/);
  const boxes = [...html.matchAll(/<input[^>]*type="checkbox"[^>]*>/g)].map(
    (m) => m[0],
  );
  assert.match(boxes[1], /disabled/);
  const automatic = render(SubscriptionChangeComparison, {
    props: {
      preview: p,
      selected: ['interval'],
      onselect: () => {},
      onconfirm: () => {},
    },
  }).body;
  assert.match(automatic, /変更後の次回請求日（推定）/);
});
