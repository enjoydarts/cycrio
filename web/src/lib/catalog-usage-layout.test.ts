import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import { modelFixture as model, syncFixture } from './catalog-test-fixtures.ts';
import { emptyCatalogState } from './model-catalog.ts';
import { emptyUsageView } from './ai-runtime-controller.ts';
import { report } from './airuntime-test-fixtures.ts';

const moduleURL = (code: string) =>
  `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
async function component(
  name: string,
  replacements: Record<string, string> = {},
): Promise<string> {
  replacements = {
    '$app/navigation': moduleURL('export const beforeNavigate=()=>{};'),
    ...replacements,
  };
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const nested: Record<string, string> = {};
  for (const m of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    nested[`./${m[1]}.svelte`] = await component(m[1], replacements);
  const code = js.code
    .replace(/^import\s+['"][^'"]+\.css['"];?\n/gm, '')
    .replace(
      /(from\s+|import\s*)(['"])([^'"]+)\2/g,
      (_m, p, q, path) =>
        `${p}${q}${replacements[path] ?? nested[path] ?? (path.startsWith('.') ? new URL(path.endsWith('.ts') ? path : `${path}.ts`, import.meta.url).href : import.meta.resolve(path))}${q}`,
    );
  return moduleURL(code);
}
function override(path: string, name: string, value: unknown) {
  return moduleURL(
    `export * from ${JSON.stringify(new URL(path, import.meta.url).href)}; export const ${name}=()=>(${JSON.stringify(value)});`,
  );
}
test('catalog starts with model browsing and separates provider sync and manual registration', async () => {
  const state = {
    ...emptyCatalogState(),
    status: 'ready',
    models: [model()],
    total: 1,
  };
  const Component = (
    await import(
      await component('ModelCatalog', {
        './model-catalog': override(
          './model-catalog.ts',
          'emptyCatalogState',
          state,
        ),
      })
    )
  ).default;
  const html = render(Component, {
    props: { api: {}, token: async () => null, onDenied: () => {} },
  }).body;
  assert.match(html, /<h1[^>]*>モデルカタログ<\/h1>/);
  assert.match(html, /class="library-header/);
  assert.match(html, /class="library-panel/);
  assert.match(html, /aria-label="カタログの表示"/);
  assert.match(html, /<section[^>]*aria-label="同期・更新"[^>]*hidden/);
  assert.match(html, /<section[^>]*aria-label="モデルを追加"[^>]*hidden/);
});
test('catalog price technical identity is disclosed separately from the current reference rates', async () => {
  const Component = (await import(await component('CatalogPrices'))).default;
  const html = render(Component, {
    props: {
      model: model(),
      prices: [],
      busy: false,
      save: async () => true,
      reset: async () => true,
      reload: async () => true,
      invalid: () => {},
    },
  }).body;
  assert.match(html, /aria-label="価格の表示"/);
  assert.match(html, /<section[^>]*aria-label="価格を管理"[^>]*hidden/);
  assert.match(html, /<section[^>]*aria-label="価格履歴"[^>]*hidden/);
});
test('sync begins with provider states and asks which provider to configure', async () => {
  const Component = (await import(await component('CatalogSync'))).default;
  const html = render(Component, {
    props: {
      states: [syncFixture('openrouter'), syncFixture('anthropic')],
      busy: false,
      configure: async () => true,
      request: () => {},
      reload: async () => true,
    },
  }).body;
  assert.match(html, /aria-label="提供元の同期状態"/);
  assert.match(html, /OpenRouter の同期設定/);
  assert.doesNotMatch(html, /<form/);
});
test('usage opens the overview and retains detailed breakdown and calls behind explicit views', async () => {
  const Component = (
    await import(
      await component('AIUsage', {
        './ai-runtime-controller': override(
          './ai-runtime-controller.ts',
          'emptyUsageView',
          emptyUsageView(),
        ),
      })
    )
  ).default;
  const html = render(Component, {
    props: { api: {}, token: async () => null, onDenied: () => {} },
  }).body;
  assert.match(html, /<h1[^>]*>AI 利用原価<\/h1>/);
  assert.match(html, /class="library-header/);
  assert.match(html, /class="library-panel/);
  assert.match(html, /aria-label="原価の表示"/);
  assert.match(html, /<section[^>]*aria-label="詳細内訳"[^>]*hidden/);
  assert.match(html, /<section[^>]*aria-label="呼出し履歴"[^>]*hidden/);
  assert.match(html, /<details[^>]*>[\s\S]*?<summary>詳細条件/);
});
test('usage overview prioritizes compact currency amounts without treating unknown as zero', async () => {
  const totals = [
    {
      currency: 'USD',
      amount: '0',
      estimated_amount: '0',
      reported_amount: '0',
      calls: 2,
      unknown_calls: 1,
      failures: 1,
    },
    {
      currency: 'EUR',
      amount: '1.23456789',
      estimated_amount: '1.23456789',
      reported_amount: '0',
      calls: 1,
      unknown_calls: 0,
      failures: 0,
    },
    {
      currency: null,
      amount: '0',
      estimated_amount: '0',
      reported_amount: '0',
      calls: 1,
      unknown_calls: 1,
      failures: 0,
    },
  ];
  const state = {
    ...emptyUsageView(),
    status: 'ready',
    report: { ...report(), totals },
  };
  const Component = (
    await import(
      await component('AIUsage', {
        './ai-runtime-controller': override(
          './ai-runtime-controller.ts',
          'emptyUsageView',
          state,
        ),
      })
    )
  ).default;
  const html = render(Component, {
    props: { api: {}, token: async () => null, onDenied: () => {} },
  }).body;
  assert.match(html, /class="usage-amounts/);
  assert.match(html, /0 USD/);
  assert.match(html, /1.23456789 EUR/);
  assert.match(html, /金額不明/);
});
