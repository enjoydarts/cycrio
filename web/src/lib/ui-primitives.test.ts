import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile, readdir } from 'node:fs/promises';
import { compile, parse } from 'svelte/compiler';
import { render } from 'svelte/server';
import { createRawSnippet } from 'svelte';

async function component(name: string) {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+|import\s+)(['"])([^'"]+)\2/g,
    (_m, p, q, path) =>
      `${p}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
  );
  return (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
}
const children = createRawSnippet(() => ({
  render: () => '<span>失敗した取り込みを再試行する</span>',
}));

test('Button preserves native submit default and explicit button, disabled and form attributes', async () => {
  const Button = await component('Button');
  assert.match(render(Button, { props: { children } }).body, /type="submit"/);
  const html = render(Button, {
    props: {
      children,
      type: 'button',
      disabled: true,
      form: 'editor',
      name: 'action',
      value: 'retry',
      'aria-label': '再試行',
    },
  }).body;
  for (const pattern of [
    /type="button"/,
    /disabled/,
    /form="editor"/,
    /name="action"/,
    /value="retry"/,
    /aria-label="再試行"/,
  ])
    assert.match(html, pattern);
});

test('ActionLink keeps native navigation and removes navigation and tab stop when disabled', async () => {
  const Link = await component('ActionLink');
  assert.match(
    render(Link, {
      props: { children, href: '/subscriptions', target: '_blank' },
    }).body,
    /href="\/subscriptions"/,
  );
  const html = render(Link, {
    props: { children, href: '/subscriptions', disabled: true },
  }).body;
  assert.doesNotMatch(html, /href=/);
  assert.match(html, /aria-disabled="true"/);
  assert.match(html, /tabindex="-1"/);
});

test('disabled ActionLink prevents its actual click handler from navigating or dispatching actions', async () => {
  const source = await readFile(
    new URL('./ActionLink.svelte', import.meta.url),
    'utf8',
  );
  const ast = parse(source, { modern: true }) as unknown as Record<
    string,
    unknown
  >;
  let expression: { start: number; end: number } | undefined;
  function visit(value: unknown) {
    if (!value || typeof value !== 'object') return;
    const node = value as Record<string, unknown>;
    if (node.name === 'onclick' && node.value)
      expression = (
        Array.isArray(node.value)
          ? node.value[0]
          : (node.value as { expression: typeof expression })
      ).expression;
    for (const entry of Object.values(node))
      if (Array.isArray(entry)) entry.forEach(visit);
      else if (entry && typeof entry === 'object') visit(entry);
  }
  visit(ast);
  assert.ok(expression);
  const callbackSource = source.slice(expression.start, expression.end);
  let called = 0;
  let prevented = 0;
  const handler = (disabled: boolean) =>
    new Function('disabled', 'onclick', `return (${callbackSource})`)(
      disabled,
      () => called++,
    );
  handler(true)({ preventDefault: () => prevented++ });
  assert.equal(called, 0);
  assert.equal(prevented, 1);
  handler(false)({ preventDefault: () => prevented++ });
  assert.equal(called, 1);
  assert.equal(prevented, 1);
});

test('business screens compose shared native action and table primitives', async () => {
  const exceptions = new Set([
    'Button.svelte',
    'ActionLink.svelte',
    'DataTable.svelte',
  ]);
  for (const name of await readdir(new URL('./', import.meta.url))) {
    if (!name.endsWith('.svelte') || exceptions.has(name)) continue;
    const source = await readFile(new URL(name, import.meta.url), 'utf8');
    assert.doesNotMatch(
      source,
      /<(?:button|table)\b/,
      `${name} must use shared actions/tables`,
    );
  }
});

test('ActionGroup and DataTable expose wrapping layout and accessible native table semantics', async () => {
  const Group = await component('ActionGroup');
  assert.match(
    render(Group, { props: { children, 'aria-label': '操作' } }).body,
    /aria-label="操作"/,
  );
  const Table = await component('DataTable');
  const html = render(Table, {
    props: { children, 'aria-label': '履歴', class: 'execution-table' },
  }).body;
  assert.match(html, /<table[^>]*aria-label="履歴"/);
  assert.match(html, /execution-table/);
  const compact = render(Table, {
    props: { children, responsive: 'compact' },
  }).body;
  assert.match(compact, /class="[^"]*\bcompact\b/);
});

test('Clerk Last used fallback still targets the external widget native button', async () => {
  const source = await readFile(
    new URL('./AppAccess.svelte', import.meta.url),
    'utf8',
  );
  const { css, warnings } = compile(source, { generate: 'client' });
  assert.deepEqual(warnings, []);
  assert.match(css!.code, /button\.cl-socialButtonsBlockButton/);
});
