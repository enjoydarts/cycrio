import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import { subscription } from './subscriptions-fixtures.ts';
const source = (name: string) =>
  readFile(new URL(`./${name}.svelte`, import.meta.url), 'utf8').catch(
    () => '',
  );
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
const rule = {
  interval_unit: 'month',
  interval_count: 1,
  anchor_date: '2026-01-31',
  month_end: true,
};
const baseline = {
  subscription: {
    ...subscription(),
    interval_unit: 'month',
    interval_count: 1,
    next_billing_date: '2026-10-01',
  },
  settings: null,
  as_of: '2026-10-26',
  timezone: 'Asia/Tokyo',
  next_date_source: 'manual',
  overdue: true,
};
const settings = {
  rule,
  stale: false,
  confirmed_as_of: '2026-10-26',
  confirmed_candidate: '2026-10-31',
  algorithm_version: 'calendar-v1',
  confirmed_version: 1,
  confirmed_at: '2026-10-26T00:00:00Z',
  updated_at: '2026-10-26T00:00:00Z',
};
test('schedule components compile without accessibility warnings and expose compact detail entry', async () => {
  for (const name of [
    'ScheduleCalculator',
    'ScheduleConditions',
    'ScheduleReview',
  ]) {
    const text = await source(name);
    assert.ok(text.length, name);
    assert.deepEqual(
      compile(text, {
        filename: `${name}.svelte`,
        generate: 'server',
      }).warnings.map((w) => w.code),
      [],
      name,
    );
  }
  assert.match(await source('SubscriptionDetail'), /ScheduleCalculator/);
});
test('saved conditions render human confirmation separately from calculation and manual overrides; absent settings add no card', async () => {
  const Conditions = await component('ScheduleConditions');
  assert.doesNotMatch(
    render(Conditions, { props: { view: baseline } }).body,
    /本人確認済み|基準日/,
  );
  const html = render(Conditions, {
    props: {
      view: {
        ...baseline,
        settings,
        next_date_source: 'confirmed_calculation',
      },
    },
  }).body;
  for (const text of ['本人確認済み', '2026-01-31', '月末固定', '1か月ごと'])
    assert.ok(html.includes(text), text);
  const stale = render(Conditions, {
    props: { view: { ...baseline, settings: { ...settings, stale: true } } },
  }).body;
  assert.match(stale, /再確認/);
  assert.match(stale, /手動/);
  assert.ok(
    stale.indexOf('</details>') < stale.indexOf('再確認'),
    'reconfirmation warning stays visible when saved conditions are collapsed',
  );
  assert.ok(
    stale.indexOf('</details>') < stale.indexOf('手動'),
    'manual override warning stays visible when saved conditions are collapsed',
  );
});
test('candidate review renders before/after, JST today and overdue without calling it a payment; nullable reasons are distinct', async () => {
  const Review = await component('ScheduleReview');
  const preview = {
    subscription: baseline.subscription,
    rule,
    as_of: '2026-10-26',
    timezone: 'Asia/Tokyo',
    candidate_date: '2026-10-26',
    reason: '',
    algorithm_version: 'calendar-v1',
    overdue: true,
  };
  const html = render(Review, { props: { baseline, preview } }).body;
  for (const text of [
    '変更前',
    '変更後',
    '2026-10-01',
    '2026-10-26',
    '本日',
    '日本時間',
    '指定条件から算出',
    '支払済み',
  ])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /NaN|Infinity/);
  assert.match(
    render(Review, {
      props: {
        baseline,
        preview: { ...preview, reason: 'ended', candidate_date: null },
      },
    }).body,
    /終了済み/,
  );
  assert.match(
    render(Review, {
      props: {
        baseline,
        preview: { ...preview, reason: 'out_of_range', candidate_date: null },
      },
    }).body,
    /日付の範囲/,
  );
});
