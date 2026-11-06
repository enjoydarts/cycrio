import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { existsSync } from 'node:fs';
import { render } from 'svelte/server';

test('account activity shows actual execution starts in Japan time across midnight', async () => {
  assert.ok(
    existsSync(new URL('./ImportLastActivity.svelte', import.meta.url)),
    'account execution activity is missing',
  );
  const Activity = (await import('./ImportLastActivity.svelte')).default;
  const body = render(Activity, {
    props: {
      syncStartedAt: '2026-11-03T14:59:58Z',
      analysisStartedAt: '2026-11-03T15:00:01Z',
    },
  }).body;
  for (const label of [
    '最終取り込み',
    '最終解析',
    '実行開始日時',
    '日本時間',
    '2026/11/03 23:59:58',
    '2026/11/04 00:00:01',
  ])
    assert.ok(body.includes(label), label);
  assert.match(body, /datetime="2026-11-03T15:00:01Z"/);
});

test('activity distinguishes never executed from missing legacy metadata', async () => {
  assert.ok(
    existsSync(new URL('./ImportLastActivity.svelte', import.meta.url)),
    'account execution activity is missing',
  );
  const Activity = (await import('./ImportLastActivity.svelte')).default;
  const empty = render(Activity, {
    props: { syncStartedAt: null, analysisStartedAt: null },
  }).body;
  assert.equal((empty.match(/未実行/g) ?? []).length, 2);
  const unknown = render(Activity, { props: {} }).body;
  assert.equal((unknown.match(/記録なし/g) ?? []).length, 2);
  assert.doesNotMatch(unknown, /未実行/);
});
