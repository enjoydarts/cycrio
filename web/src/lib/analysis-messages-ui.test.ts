import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
test('message list SSR distinguishes each stage, uses import time, and exposes keyboard buttons without mail contents', async () => {
  const source = await readFile(
    new URL('./AnalysisMessagesPanel.svelte', import.meta.url),
    'utf8',
  );
  const compiled = compile(source, { generate: 'server' });
  assert.deepEqual(
    compiled.warnings.map((w) => w.code),
    [],
  );
  const code = compiled.js.code.replace(
    /(from\s+|import\s+)(['"])([^'"]+)\2/g,
    (_m, p, q, path) =>
      `${p}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
  );
  const Panel = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const id = '11111111-1111-4111-8111-111111111111';
  const row = {
    id,
    account_id: id,
    account_label: '連携メール',
    discovered_at: '2026-10-29T00:00:00Z',
    primary: 'completed',
    secondary: 'waiting',
    registration: 'not_started',
    result_id: null,
    service: null,
    amount: null,
    currency: null,
    event_type: null,
    error_code: '',
  };
  const html = render(Panel, {
    props: { messages: [row], selectedId: id, onSelect: () => {} },
  }).body;
  for (const label of [
    '一次判定',
    '二次抽出',
    '登録',
    '取込日時',
    '一次判定済み',
    '二次抽出待ち',
    '未開始',
    'aria-pressed="true"',
    '<button',
  ])
    assert.ok(html.includes(label), label);
  assert.doesNotMatch(html, /受信日時|件名|差出人|本文|<aside/);
  const empty = render(Panel, {
    props: { messages: [], selectedId: null, onSelect: () => {} },
  }).body;
  assert.match(empty, /該当するメールはありません/);
});
