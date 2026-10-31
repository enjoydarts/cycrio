import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import {
  importProcessed,
  importProgressText,
  importStateNames,
} from './import-executions-api.ts';
const row = {
  discovered_count: 100,
  completed_count: 60,
  failed_count: 5,
  unknown_count: 2,
};
test('import progress counts terminal outcomes without claiming that failed mail was analyzed successfully', () => {
  assert.equal(importProcessed(row), 67);
  assert.equal(importProgressText(row), '処理済み 67 / 100 通');
  assert.equal(importStateNames.enabled, '自動取り込み有効');
  assert.equal(
    importProcessed({
      ...row,
      discovered_count: 0,
      completed_count: 0,
      failed_count: 0,
      unknown_count: 0,
    }),
    0,
  );
  assert.equal(importProcessed({ ...row, completed_count: 100 }), 100);
});
test('shared progress meter renders native progress and handles an empty population', async () => {
  const Component = (await import('./ProgressMeter.svelte')).default;
  const html = render(Component, {
    props: {
      value: 67,
      max: 100,
      label: '取得済み分の処理進捗',
      description: '処理済み 67 / 100 通',
    },
  }).body;
  assert.match(
    html,
    /<progress[^>]*max="100"[^>]*value="67"|<progress[^>]*value="67"[^>]*max="100"/,
  );
  assert.match(html, /67%/);
  assert.match(html, /処理済み 67 \/ 100 通/);
  const empty = render(Component, {
    props: {
      value: 0,
      max: 0,
      label: '取得済み分の処理進捗',
      description: '処理対象はまだありません',
    },
  }).body;
  assert.doesNotMatch(empty, /<progress|NaN|Infinity/);
  assert.match(empty, /処理対象はまだありません/);
  const fine = render(Component, {
    props: {
      value: 1234,
      max: 10000,
      label: '取得済み分の処理進捗',
      description: '処理済み 1,234 / 10,000 通',
    },
  }).body;
  assert.match(fine, /12\.34%/);
  const almost = render(Component, {
    props: { value: 99999, max: 100000, label: '進捗', description: '処理中' },
  }).body;
  assert.doesNotMatch(almost, />100%/);
});
