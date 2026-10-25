import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile, parse } from 'svelte/compiler';
import { render } from 'svelte/server';
import { profile } from './airuntime-test-fixtures.ts';
test('the runtime refresh button preserves edits by calling refresh rather than load', async () => {
  const source = await readFile(
    new URL('./AIRuntime.svelte', import.meta.url),
    'utf8',
  );
  function firstButton(value: unknown): Record<string, unknown> | undefined {
    if (!value || typeof value !== 'object') return;
    if (Array.isArray(value)) {
      for (const child of value) {
        const result = firstButton(child);
        if (result) return result;
      }
      return;
    }
    const node = value as Record<string, unknown>;
    if (node.name === 'button') return node;
    for (const child of Object.values(node)) {
      const result = firstButton(child);
      if (result) return result;
    }
  }
  const button = firstButton(parse(source, { modern: true }));
  assert.ok(button && Array.isArray(button.attributes));
  const attribute = button.attributes.find(
    (a: { name?: string }) => a.name === 'onclick',
  );
  assert.ok(attribute && attribute.value);
  const value = Array.isArray(attribute.value)
    ? attribute.value[0]
    : attribute.value;
  const expression = value.expression;
  assert.ok(
    expression &&
      typeof expression.start === 'number' &&
      typeof expression.end === 'number',
  );
  const actions: string[] = [];
  const handler = new Function(
    'controller',
    'confirmed',
    `return (${source.slice(expression.start, expression.end)});`,
  )(
    {
      load: () => actions.push('load'),
      refresh: () => actions.push('refresh'),
    },
    false,
  );
  handler();
  assert.deepEqual(actions, ['refresh']);
});
test('the actual approved provider input rejects nested or empty variant segments', async () => {
  const source = await readFile(
    new URL('./AIRuntime.svelte', import.meta.url),
    'utf8',
  );
  function find(value: unknown): Record<string, unknown> | undefined {
    if (!value || typeof value !== 'object') return;
    if (Array.isArray(value)) {
      for (const child of value) {
        const found = find(child);
        if (found) return found;
      }
      return;
    }
    const node = value as Record<string, unknown>;
    if (
      node.name === 'input' &&
      Array.isArray(node.attributes) &&
      node.attributes.some(
        (a) =>
          a.name === 'id' &&
          Array.isArray(a.value) &&
          a.value.some(
            (part: { data?: string }) => part.data === 'runtime-provider',
          ),
      )
    )
      return node;
    for (const child of Object.values(node)) {
      const found = find(child);
      if (found) return found;
    }
  }
  const input = find(parse(source, { modern: true }));
  assert.ok(input && Array.isArray(input.attributes));
  const attribute = input.attributes.find(
    (a: { name?: string }) => a.name === 'pattern',
  );
  assert.ok(attribute);
  const { js } = compile(
    `<input ${source.slice(attribute.start, attribute.end)} />`,
    { generate: 'server' },
  );
  const code = js.code.replace(
    /(from\s+|import\s*)(['"])(svelte[^'"]+)\2/g,
    (_match, prefix, quote, path) =>
      `${prefix}${quote}${import.meta.resolve(path)}${quote}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const html = render(Component).body;
  const pattern = /pattern="([^"]*)"/.exec(html)?.[1];
  assert.ok(pattern);
  const expression = new RegExp(`^(?:${pattern})$`, 'v');
  for (const value of [
    'vendor/a/b',
    'vendor//a',
    'vendor/',
    '/vendor',
    'Vendor/a',
  ])
    assert.equal(expression.test(value), false, value);
  for (const value of ['vendor', 'vendor/a', 'vendor-1/a_1.b'])
    assert.equal(expression.test(value), true, value);
});
async function component(name: string) {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js } = compile(source, { generate: 'server' });
  const code = js.code.replace(
    /(from\s+|import\s*)(['"])([^'"]+)\2/g,
    (_match, prefix, quote, path) =>
      `${prefix}${quote}${path.startsWith('.') ? new URL(path.endsWith('.ts') ? path : `${path}.ts`, import.meta.url).href : import.meta.resolve(path)}${quote}`,
  );
  return (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
}
test('runtime profile renders human model and key versions and labels unknown pricing', async () => {
  const Component = await component('RuntimeProfile');
  const html = render(Component, {
    props: { profile: profile(), title: '保存済みの実行下書き' },
  }).body.replace(/\s+/g, ' ');
  assert.match(html, /D1/);
  assert.match(html, /Test/);
  assert.match(html, /API キー版/);
  assert.match(html, /不明/);
  assert.match(html, /8192/);
  assert.match(html, /技術情報/);
});
test('totals distinguish unknown currency calls from known zero without summing currencies', async () => {
  const Component = await component('CostTotals');
  const html = render(Component, {
    props: {
      totals: [
        {
          currency: null,
          amount: '0',
          estimated_amount: '0',
          reported_amount: '0',
          calls: 1,
          unknown_calls: 1,
          failures: 1,
        },
        {
          currency: 'USD',
          amount: '0',
          estimated_amount: '0',
          reported_amount: '0',
          calls: 1,
          unknown_calls: 0,
          failures: 0,
        },
      ],
    },
  }).body.replace(/\s+/g, ' ');
  assert.match(html, /通貨不明/);
  assert.match(html, /金額不明/);
  assert.match(html, /0 USD/);
  assert.match(html, /不明 1 件/);
  assert.match(html, /失敗 1 件/);
});
