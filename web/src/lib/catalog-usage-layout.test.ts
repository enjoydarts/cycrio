import './svelte-test-hooks.ts';
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
      /(from\s+|import\s+)(['"])([^'"]+)\2/g,
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
test('usage opens the overview and keeps user UUIDs and calls in separate views', async () => {
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
  assert.match(html, /<section[^>]*aria-label="使用ユーザー別"[^>]*hidden/);
  assert.match(html, /<section[^>]*aria-label="呼出し履歴"[^>]*hidden/);
  assert.match(html, /<details[^>]*>[\s\S]*?<summary>詳細条件/);
});
test('usage overview shows costs and all call counts before provider and model breakdown', async () => {
  const total = {
    currency: 'USD',
    amount: '0.1',
    estimated_amount: '0.1',
    reported_amount: '0',
    calls: 2,
    unknown_calls: 0,
    failures: 1,
  };
  const state = {
    ...emptyUsageView(),
    status: 'ready',
    report: {
      ...report(),
      totals: [
        total,
        { ...total, currency: null, calls: 3, unknown_calls: 3, failures: 2 },
      ],
      breakdown: [
        {
          provider: 'openai',
          model_id: 'model-1',
          model_name: 'First model',
          stage: 'classification',
          totals: [total],
        },
        {
          provider: 'openai',
          model_id: 'model-2',
          model_name: 'Second model',
          stage: 'extraction',
          totals: [{ ...total, amount: '0.2', calls: 1, failures: 0 }],
        },
      ],
      users: [
        {
          user_id: 'consumer-uuid',
          calls: 2,
          unknown_calls: 0,
          failures: 1,
          totals: [total],
        },
      ],
      users_total: 1,
    },
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
  const overview =
    /<section[^>]*aria-label="原価の概要"[^>]*>([\s\S]*?)<\/section>/.exec(
      html,
    )?.[1];
  assert.ok(overview);
  assert.match(overview, /呼出し[\s\S]*?5 件/);
  assert.match(overview, /失敗・結果不明[\s\S]*?3 件/);
  assert.match(overview, /提供元別[\s\S]*?OpenAI[\s\S]*?0.3 USD/);
  assert.match(overview, /First model/);
  assert.match(overview, /Second model/);
  assert.match(
    overview,
    /<details[^>]*>[\s\S]*?<summary>管理者テスト・システム処理/,
  );
  assert.doesNotMatch(overview, /consumer-uuid/);
});
test('unknown-only provider spend has no amount card and does not imply that calls are absent', async () => {
  const Component = (await import(await component('UsageOverview'))).default;
  const totals = [
    {
      currency: 'USD',
      amount: '0',
      estimated_amount: '0',
      reported_amount: '0',
      calls: 3,
      unknown_calls: 3,
      failures: 1,
    },
  ];
  const html = render(Component, {
    props: {
      report: {
        ...report(),
        totals,
        breakdown: [
          {
            provider: 'openai',
            model_id: 'model-1',
            model_name: 'Model',
            stage: 'classification',
            totals,
          },
        ],
      },
    },
  }).body;
  const providers = html.slice(
    html.indexOf('提供元別'),
    html.indexOf('モデル・処理段階別'),
  );
  assert.doesNotMatch(
    providers,
    /provider-card|0 USD|対象の呼出しはありません/,
  );
  assert.match(providers, /集計済みの提供元コストはありません/);
  assert.match(html, /3 件/);
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
  assert.match(html, /の集計済みコスト/);
  assert.doesNotMatch(html, /通貨不明|金額不明/);
});
