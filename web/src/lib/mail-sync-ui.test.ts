import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile, parse } from 'svelte/compiler';
import { render } from 'svelte/server';
import { account, ids } from './mail-test-fixtures.ts';
test('sync status reports mutation busy and releases it for reads, failure, account reset and disposal', async () => {
  const source = await readFile(
    new URL('./MailSync.svelte', import.meta.url),
    'utf8',
  );
  let changed: { start: number; end: number } | undefined;
  function visit(value: unknown) {
    if (!value || typeof value !== 'object') return;
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    const node = value as Record<string, unknown>;
    if (
      node.type === 'CallExpression' &&
      (node.callee as { name?: string })?.name === 'createMailSyncController'
    )
      changed = (node.arguments as { start: number; end: number }[])[2];
    Object.values(node).forEach(visit);
  }
  visit(parse(source, { modern: true }));
  assert.ok(changed);
  const transitions: boolean[] = [];
  const handler = new Function(
    'onBusyChange',
    `let view; return (${source.slice(changed.start, changed.end)});`,
  )((busy: boolean) => transitions.push(busy));
  for (const status of [
    'loading',
    'saving',
    'ready',
    'error',
    'idle',
    'denied',
    'disposed',
  ])
    handler({ status });
  assert.deepEqual(transitions, [
    false,
    true,
    false,
    false,
    false,
    false,
    false,
  ]);
});
test('mail dialog includes child sync mutations in its busy guard', async () => {
  const source = await readFile(
    new URL('./MailAccounts.svelte', import.meta.url),
    'utf8',
  );
  assert.match(source, /onBusyChange=/);
  assert.match(source, /view.status === 'saving' \|\|[\s\S]*?syncSaving/);
});
test('busy modal suppresses Escape before a noncancelable native close request', async () => {
  const source = await readFile(
    new URL('./Modal.svelte', import.meta.url),
    'utf8',
  );
  let handler: { start: number; end: number } | undefined;
  function visit(value: unknown) {
    if (!value || typeof value !== 'object') return;
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    const node = value as Record<string, unknown>;
    if (node.name === 'svelte:window' && Array.isArray(node.attributes)) {
      const attribute = node.attributes.find(
        (item) => item.name === 'onkeydown',
      );
      const part = Array.isArray(attribute?.value)
        ? attribute.value[0]
        : attribute?.value;
      handler = part?.expression;
    }
    Object.values(node).forEach(visit);
  }
  visit(parse(source, { modern: true }));
  assert.ok(handler, 'Escape is guarded before native close request');
  for (const [busy, open, key, allowed, expected] of [
    [true, true, 'Escape', true, 1],
    [false, true, 'Escape', true, 0],
    [false, true, 'Escape', false, 1],
    [true, false, 'Escape', false, 0],
    [true, true, 'Tab', false, 0],
  ] as const) {
    let prevented = 0;
    const listener = new Function(
      'busy',
      'dialog',
      'canClose',
      `return (${source.slice(handler.start, handler.end)});`,
    )(busy, { open }, () => allowed);
    listener({
      key,
      preventDefault() {
        prevented++;
      },
    });
    assert.equal(prevented, expected);
  }
});
test('manual sync SSR starts empty, explains saved targets, receive-date boundaries and no content/AI', async () => {
  const source = await readFile(
    new URL('./MailSync.svelte', import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+|import\s*)(['"])([^'"]+)\2/g,
    (_m, prefix, quote, path) =>
      `${prefix}${quote}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${quote}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const html = render(Component, {
    props: {
      account: account({
        state: 'ready',
        folders: [
          {
            id: ids.folder,
            name: 'INBOX',
            delimiter: '/',
            selected: true,
            available: true,
            selectable: true,
          },
        ],
      }),
      api: {},
      token: async () => '',
      onDenied: () => {},
      refreshAccount: () => {},
    },
  }).body;
  for (const text of [
    'メールIDの手動同期',
    '開始日（含む）',
    '終了日（含まない）',
    '保存済みの対象フォルダー',
    '1 件',
    'IMAP',
    '受信日',
    '本文取得・AI送信は行わずIDだけを列挙',
    '最新20件',
    '入力した条件で同期を開始',
  ])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /value="(?:2026|20)/);
  assert.match(html, /required/);
  assert.match(html, /aria-live="polite"/);
  assert.doesNotMatch(source, /adminStatus|localStorage|sessionStorage/);
});
test('actual start, retry, refresh and discard CTAs call separate controller operations', async () => {
  const source = await readFile(
    new URL('./MailSync.svelte', import.meta.url),
    'utf8',
  );
  const buttons: Record<string, unknown>[] = [];
  function visit(value: unknown) {
    if (!value || typeof value !== 'object') return;
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    const node = value as Record<string, unknown>;
    if (node.name === 'button') buttons.push(node);
    Object.values(node).forEach(visit);
  }
  visit(parse(source, { modern: true }));
  for (const [id, method] of [
    ['sync-start', 'start'],
    ['sync-retry', 'retry'],
    ['sync-refresh', 'refresh'],
    ['sync-discard', 'discard'],
  ]) {
    const button = buttons.find(
      (node) =>
        Array.isArray(node.attributes) &&
        (node.attributes as { name: string; value: { data: string }[] }[]).some(
          (a) => a.name === 'id' && a.value?.[0]?.data === id,
        ),
    );
    assert.ok(button, id);
    const attr = (
      button.attributes as {
        name: string;
        value: { expression: { start: number; end: number } }[];
      }[]
    ).find((a) => a.name === 'onclick')!;
    const value = Array.isArray(attr.value) ? attr.value[0] : attr.value;
    const expr = value.expression;
    const calls: string[] = [];
    new Function(
      'controller',
      `return (${source.slice(expr.start, expr.end)})`,
    )(
      Object.fromEntries(
        ['start', 'retry', 'refresh', 'discard'].map((key) => [
          key,
          () => calls.push(key),
        ]),
      ),
    )();
    assert.deepEqual(calls, [method]);
  }
  for (const label of [
    '列挙完了',
    '上限に到達',
    '今回確認したID',
    '新規登録',
    '完了フォルダー',
    '同期を停止',
  ])
    assert.ok(source.includes(label), label);
});
