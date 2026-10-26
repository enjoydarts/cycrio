import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import {
  historyPreview,
  historyEvidence,
  historyRule,
} from './schedule-history-fixtures.ts';
import { initialHistoryView } from './schedule-history-controller.ts';
const source = (name: string) =>
  readFile(new URL(`./${name}.svelte`, import.meta.url), 'utf8');
async function component(name: string) {
  const code = compile(await source(name), {
    generate: 'server',
  }).js.code.replace(
    /(from\s+|import\s*)(['"])([^'"]+)\2/g,
    (_m, p, q, path) =>
      `${p}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
  );
  return (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
}
const values = {
  interval_unit: 'week',
  interval_count: '2',
  anchor_date: '2026-01-01',
  month_end: false,
};
test('closed history entry renders no additional date fields, and expanded filters require explicit action', async () => {
  const History = await component('ScheduleHistory');
  const props = {
    view: initialHistoryView('JPY'),
    values,
    dirty: false,
    disabled: false,
    conflict: false,
    onToggle: () => {},
    onEdit: () => {},
    onFetch: () => {},
    onAdopt: () => {},
  };
  const closed = render(History, { props }).body;
  assert.match(closed, /支払履歴から候補を確認/);
  assert.doesNotMatch(closed, /type="date"/);
  assert.match(closed, /aria-expanded="false"/);
  const expanded = render(History, {
    props: { ...props, view: { ...props.view, expanded: true } },
  }).body;
  for (const t of [
    '対象開始日',
    '対象通貨',
    '候補を確認する',
    'プラン',
    '3〜5件',
    '完全一致',
    '0円',
    '返金',
    '未記録',
  ])
    assert.ok(expanded.includes(t), t);
  assert.match(expanded, /type="date"[^>]*required/);
  assert.match(expanded, /pattern="\[A-Z\]\{3\}"/);
  assert.doesNotMatch(expanded, /この候補を入力する/);
});
test('all ambiguous choices render equally with replacement disclosure and disabled unavailable candidates', async () => {
  const History = await component('ScheduleHistory');
  const result = {
    ...historyPreview(),
    status: 'ambiguous',
    candidates: [
      ...historyPreview().candidates,
      {
        rule: { ...historyRule, month_end: false },
        candidate_date: null,
        reason: 'out_of_range',
      },
    ],
  };
  const html = render(History, {
    props: {
      view: { ...initialHistoryView(), expanded: true, result },
      values,
      dirty: true,
      disabled: false,
      conflict: false,
      onToggle: () => {},
      onEdit: () => {},
      onFetch: () => {},
      onAdopt: () => {},
    },
  }).body;
  for (const t of [
    '複数候補',
    '2026-07-31',
    '2026-01-01',
    '月末固定',
    '日付指定',
    '置き換え',
    '0 JPY',
    '対象明細',
    '3件',
    '範囲',
  ])
    assert.ok(html.replace(/\s+/g, ' ').includes(t), t);
  assert.equal((html.match(/この候補を入力する/g) ?? []).length, 2);
  assert.match(html, /<button[^>]*disabled[^>]*>この候補を入力する/);
  assert.doesNotMatch(html, /checked|おすすめ|精度/);
  assert.ok(
    html.indexOf('<details>') < html.indexOf('schedule-history-counts'),
    'full exclusion counts stay in the expandable evidence detail',
  );
  assert.match(
    html.replace(/\s+/g, ' '),
    /対象 3件 · 有効な支払 3件 · 比較に使用 3件/,
  );
});
test('historical evidence renders separately from present history and accepts stale historical rules', async () => {
  const Conditions = await component('ScheduleConditions');
  const html = render(Conditions, {
    props: {
      view: {
        subscription: historyPreview().subscription,
        settings: {
          rule: historyRule,
          stale: true,
          confirmed_as_of: '2026-10-26',
          confirmed_candidate: '2026-10-31',
          history_evidence: historyEvidence(),
        },
        next_date_source: 'manual',
      },
    },
  }).body;
  for (const t of [
    '前回確認した履歴の根拠',
    '2026-10-26',
    '2026-01-01',
    '2026-09-30',
    '再推定',
  ])
    assert.ok(html.includes(t), t);
  assert.doesNotMatch(html, /history-exact-v1|1111-4111|版 1/);
});
test('history component compiles without accessibility warnings', async () => {
  assert.deepEqual(
    compile(await source('ScheduleHistory'), {
      filename: 'ScheduleHistory.svelte',
      generate: 'server',
    }).warnings.map((w) => w.code),
    [],
  );
});
