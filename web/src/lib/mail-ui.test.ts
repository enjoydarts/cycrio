import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile, parse } from 'svelte/compiler';
import { render } from 'svelte/server';
test('actual refresh and discard handlers have separate semantics', async () => {
  const source = await readFile(
    new URL('./MailAccounts.svelte', import.meta.url),
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
  for (const [id, expected] of [
    ['mail-refresh', 'refresh'],
    ['mail-discard', 'discard'],
  ]) {
    const button = buttons.find(
      (node) =>
        Array.isArray(node.attributes) &&
        (node.attributes as { name: string; value: { data: string }[] }[]).some(
          (a) => a.name === 'id' && a.value?.[0]?.data === id,
        ),
    );
    assert.ok(button);
    const onclick = (
      button.attributes as {
        name: string;
        value: { expression: { start: number; end: number } };
      }[]
    ).find((a) => a.name === 'onclick')!;
    const expression = (
      Array.isArray(onclick.value) ? onclick.value[0] : onclick.value
    ).expression;
    const calls: string[] = [];
    const handler = new Function(
      'controller',
      `return (${source.slice(expression.start, expression.end)})`,
    )({
      refresh: () => calls.push('refresh'),
      discard: () => calls.push('discard'),
    });
    handler();
    assert.deepEqual(calls, [expected]);
  }
});
test('mail component SSR shows explicit Fastmail boundaries and never includes a preset secret', async () => {
  const source = await readFile(
    new URL('./MailAccounts.svelte', import.meta.url),
    'utf8',
  );
  const { js } = compile(source, { generate: 'server' });
  const syncSource = await readFile(
    new URL('./MailSync.svelte', import.meta.url),
    'utf8',
  );
  const syncCode = compile(syncSource, { generate: 'server' }).js.code.replace(
    /(from\s+|import\s*)(['"])([^'"]+)\2/g,
    (_m, prefix, quote, path) =>
      `${prefix}${quote}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${quote}`,
  );
  const syncURL = `data:text/javascript;base64,${Buffer.from(syncCode).toString('base64')}`;
  const code = js.code.replace(
    /(from\s+|import\s*)(['"])([^'"]+)\2/g,
    (_m, prefix, quote, path) =>
      `${prefix}${quote}${path === './MailSync.svelte' ? syncURL : path.startsWith('.') ? new URL(path.endsWith('.ts') ? path : `${path}.ts`, import.meta.url).href : import.meta.resolve(path)}${quote}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const html = render(Component, {
    props: { api: {}, token: async () => '', onDenied: () => {} },
  }).body;
  for (const text of [
    'imap.fastmail.com:993',
    'アプリパスワード',
    '本文',
    'AI',
    '同期',
    'Google',
  ])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /localStorage|sessionStorage/);
});
