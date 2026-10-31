import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
test('reanalysis confirmation explains scope, protection and renewed charges before explicit consent', async () => {
  const source = await readFile(
    new URL('./ReanalysisConfirmation.svelte', import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+)(['"])([^'"]+)\2/g,
    (_m, p, q, path) =>
      `${p}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const body = render(Component, {
    props: {
      view: {
        account: 'a',
        before: '2026-10-31T07:19:58Z',
        preview: null,
        confirmed: false,
        uncertain: false,
        summary: null,
        status: 'loading',
        error: null,
      },
      onConfirm: () => {},
      onStart: () => {},
      onReplay: () => {},
      onRefresh: () => {},
    },
  }).body;
  for (const text of [
    '成功・対象外',
    '確認済み・不要',
    '元メール',
    '再度料金',
    '候補',
    '通常解析',
    '全件',
    '同意',
  ])
    assert.ok(body.includes(text), text);
  assert.match(body, /type="checkbox"/);
  assert.match(body, /disabled/);
});
test('account history lists logical executions with period, mail and contract counts', async () => {
  const source = await readFile(
    new URL('./ImportExecutionsPanel.svelte', import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+)(['"])([^'"]+)\2/g,
    (_m, p, q, path) =>
      `${p}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const history = [
    {
      id: 'automation:11111111-1111-4111-8111-111111111111:1',
      kind: 'automatic',
      created_at: '2026-10-30T00:00:00Z',
      since: '2025-10-30',
      before: '2026-10-31',
      state: 'enabled',
      is_current: true,
      incremental: true,
      discovered_count: 12000,
      pending_count: 100,
      completed_count: 11800,
      candidate_count: 18,
      registered_count: 7,
      failed_count: 90,
      unknown_count: 10,
    },
  ];
  const body = render(Component, {
    props: {
      page: { history, total: 1, limit: 20, offset: 0 },
      onSelect: () => {},
      onRetry: () => {},
    },
  }).body;
  assert.equal(
    (body.match(/<tbody[^>]*>[\s\S]*?<\/tbody>/)?.[0].match(/<tr\b/g) || [])
      .length,
    1,
  );
  for (const label of [
    '実行日時',
    '2025-10-30',
    '2026-10-30',
    '12,000',
    '確認待ち',
    '登録済み',
    '失敗',
    '未処理・処理中 100 通',
    '詳細',
  ])
    assert.ok(body.includes(label), label);
  assert.doesNotMatch(
    body,
    /メールごとの取り込み履歴|件名|差出人|100件ごと|解析待ち/,
  );
  const empty = render(Component, {
    props: {
      page: { history: [], total: 0, limit: 20, offset: 0 },
      onSelect: () => {},
      onRetry: () => {},
    },
  }).body;
  assert.match(empty, /取り込み履歴はありません/);
});
test('pending mail details explain unfinished analysis and stopping while keeping outcomes separate', async () => {
  const source = await readFile(
    new URL('./ImportExecutionDetail.svelte', import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+)(['"])([^'"]+)\2/g,
    (_m, p, q, path) =>
      `${p}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const row = {
    id: 'automation:11111111-1111-4111-8111-111111111111:1',
    kind: 'automatic',
    created_at: '2026-10-30T00:00:00Z',
    since: '2025-10-30',
    before: '2026-10-31',
    state: 'stopped',
    is_current: true,
    incremental: true,
    discovered_count: 12000,
    pending_count: 100,
    completed_count: 11800,
    candidate_count: 18,
    registered_count: 7,
    failed_count: 90,
    unknown_count: 10,
  };
  for (const state of ['enabled', 'stopped']) {
    const body = render(Component, {
      props: {
        row: { ...row, state },
        account: 'account',
        onRetry: () => {},
        onStop: () => {},
      },
    }).body;
    assert.match(
      body,
      /<dt\b[^>]*>未処理・処理中<\/dt>\s*<dd\b[^>]*>100 通<\/dd>/,
    );
    assert.match(body, /<dt\b[^>]*>処理失敗<\/dt>\s*<dd\b[^>]*>90 通<\/dd>/);
    assert.match(body, /<dt\b[^>]*>結果不明<\/dt>\s*<dd\b[^>]*>10 通<\/dd>/);
    assert.match(body, /メールの所在を取得済み/);
    assert.match(body, /一次判定や必要な二次抽出が未完了/);
    assert.match(body, /処理中を含み、失敗・結果不明は含みません/);
    assert.match(body, /自動取り込みの停止中は、自動解析も進みません/);
    assert.doesNotMatch(body, /解析待ち/);
  }
});
test('running manual work and retries remain stoppable from execution details', async () => {
  const source = await readFile(
    new URL('./ImportExecutionDetail.svelte', import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+)(['"])([^'"]+)\2/g,
    (_m, p, q, path) =>
      `${p}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const row = {
    id: 'manual:11111111-1111-4111-8111-111111111111',
    kind: 'manual',
    created_at: '2026-10-30T00:00:00Z',
    since: null,
    before: null,
    state: 'running',
    is_current: false,
    incremental: false,
    discovered_count: 106,
    pending_count: 106,
    completed_count: 0,
    candidate_count: 0,
    registered_count: 0,
    failed_count: 0,
    unknown_count: 0,
    active_run_id: '22222222-2222-4222-8222-222222222222',
  };
  const props = {
    row,
    account: 'account',
    onRetry: () => {},
    onStop: () => {},
  };
  const body = render(Component, { props }).body;
  assert.match(body, /実行中の処理を停止/);
  const reanalysis = render(Component, {
    props: { ...props, row: { ...row, kind: 'reanalysis' } },
  }).body;
  assert.match(reanalysis, /再解析/);
  assert.match(reanalysis, /実行中の処理を停止/);
  const stopped = render(Component, {
    props: {
      ...props,
      row: { ...row, active_run_id: null, state: 'cancelled' },
    },
  }).body;
  assert.doesNotMatch(stopped, /実行中の処理を停止/);
});
