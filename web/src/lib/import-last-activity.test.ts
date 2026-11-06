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
    '最終メール確認',
    '最終解析',
    '実行開始日時',
    '日本時間',
    '2026/11/03 23:59:58',
    '2026/11/04 00:00:01',
  ])
    assert.ok(body.includes(label), label);
  assert.match(body, /datetime="2026-11-03T15:00:01Z"/);
});

test('zero additions explain the check without claiming there is no analysis backlog', async () => {
  const Activity = (await import('./ImportLastActivity.svelte')).default;
  const body = render(Activity, {
    props: {
      syncStartedAt: '2026-11-03T14:59:58Z',
      syncActivity: { state: 'completed', added_count: 0 },
      analysisActivity: { state: 'failed' },
      automationActivity: {
        state: 'enabled',
        next_check_at: '2026-11-03T15:00:01Z',
        retry_at: null,
        busy: false,
      },
    },
  }).body;
  for (const label of [
    '追加 0 通',
    '新しく追加されたメールはありません',
    '未解析',
    '再解析',
    '失敗',
    '次回確認',
    '2026/11/04 00:00:01',
  ])
    assert.ok(body.includes(label), label);
  assert.doesNotMatch(body, /解析対象なし/);
});

test('unfinished checks never describe zero additions as a successful empty result', async () => {
  const Activity = (await import('./ImportLastActivity.svelte')).default;
  for (const state of ['running', 'failed', 'cancelled', 'unknown']) {
    const body = render(Activity, {
      props: {
        syncActivity: { state, added_count: 0 },
        automationActivity: {
          state: 'stopped',
          next_check_at: '2026-11-03T15:00:01Z',
          retry_at: null,
          busy: false,
        },
      },
    }).body;
    assert.doesNotMatch(
      body,
      /新しく追加されたメールはありません|2026\/11\/04 00:00:01/,
    );
    assert.match(body, /自動確認は停止中/);
  }
});

test('busy and retry schedules are conditional and clearly labeled', async () => {
  const Activity = (await import('./ImportLastActivity.svelte')).default;
  const busy = render(Activity, {
    props: {
      automationActivity: {
        state: 'enabled',
        next_check_at: '2026-11-03T15:00:01Z',
        retry_at: null,
        busy: true,
      },
    },
  }).body;
  assert.match(busy, /処理の完了後/);
  assert.doesNotMatch(busy, /2026\/11\/04 00:00:01/);
  const retry = render(Activity, {
    props: {
      automationActivity: {
        state: 'enabled',
        next_check_at: '2026-11-03T15:00:01Z',
        retry_at: '2026-11-03T16:00:01Z',
        busy: false,
      },
    },
  }).body;
  assert.match(retry, /再試行予定/);
  assert.match(retry, /2026\/11\/04 01:00:01/);
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
