import './svelte-test-hooks.ts';
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
    if (node.name === 'Button') return node;
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
    /(from\s+|import\s+)(['"])(svelte[^'"]+)\2/g,
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
    /(from\s+|import\s+)(['"])([^'"]+)\2/g,
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
for (const name of ['CostTotals', 'UsageAmounts'])
  test(`${name} shows known zero without displaying unknown currency costs`, async () => {
    const Component = await component(name);
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
    assert.doesNotMatch(html, /通貨不明|金額不明|不明 1 件|失敗 1 件/);
    assert.match(html, /0 USD/);
  });
for (const name of ['CostTotals', 'UsageAmounts'])
  test(`${name} does not turn uncalculated costs into zero spend or zero calls`, async () => {
    const Component = await component(name);
    const output = render(Component, {
      props: {
        totals: [
          {
            currency: null,
            amount: '0',
            estimated_amount: '0',
            reported_amount: '0',
            calls: 2,
            unknown_calls: 2,
            failures: 1,
          },
        ],
      },
    }).body;
    assert.doesNotMatch(output, /通貨不明|金額不明|呼出し 0 件|0 USD|不明原価/);
    assert.match(output, /集計できる金額はありません/);
  });
for (const name of ['CostTotals', 'UsageAmounts'])
  test(`${name} also omits known currencies when no call has a calculated amount`, async () => {
    const Component = await component(name);
    const totals = [
      {
        currency: 'USD',
        amount: '0',
        estimated_amount: '0',
        reported_amount: '0',
        calls: 2,
        unknown_calls: 2,
        failures: 1,
      },
    ];
    const output = render(Component, { props: { totals } }).body;
    assert.doesNotMatch(output, /0 USD|通貨不明|不明原価/);
    assert.match(output, /集計できる金額はありません/);
    assert.equal(totals[0].unknown_calls, 2);
  });
