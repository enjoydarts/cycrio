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
import { emptyPriceResearchView } from './price-research-controller.ts';

const moduleURL = (code: string) =>
  `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
async function component(
  name: string,
  replacements: Record<string, string> = {},
): Promise<string> {
  replacements = {
    '$app/navigation': moduleURL('export const beforeNavigate=()=>{};'),
    '$app/env/public': moduleURL(
      "export const PUBLIC_API_BASE_URL='https://synthetic.invalid';",
    ),
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
test('account settings explain Clerk deletion and retries and keep destructive confirmation in the common modal', async () => {
  const Component = (await import(await component('AccountSettings'))).default;
  const html = render(Component, {
    props: { api: {}, token: async () => null, signOut: () => {} },
  }).body;
  assert.match(html, /class="library-header/);
  assert.match(html, /class="library-panel/);
  assert.match(html, /Clerkのユーザーも削除/);
  assert.match(html, /失敗した場合は自動で再試行/);
  assert.match(html, /Google・Microsoftのアカウント自体は削除しません/);
  assert.match(html, /サブスクリプションの解約/);
  assert.match(html, /OAuth許可は取り消されません/);
  assert.match(html, /共有のサービス用APIキー/);
  assert.match(html, /<dialog/);
  assert.match(html, /type="checkbox"/);
  assert.match(html, /aria-label="退会の確認文言"/);
  assert.match(
    html,
    /<button(?=[^>]*type="button")(?=[^>]*disabled)[^>]*>(?:<!--[\s\S]*?-->|<!---->)*退会してデータを削除/,
  );
});
test('monthly usage explains anonymous archive totals and separate individual histories', async () => {
  const Component = (await import(await component('UsageOverview'))).default;
  const html = render(Component, { props: { report: report() } }).body;
  assert.match(html, /匿名化した退会済み利用者の原価/);
  assert.match(html, /利用者別・メール接続別・呼出し履歴には含めません/);
});
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
  assert.match(html, /<section[^>]*aria-label="料金調査"[^>]*hidden/);
  assert.doesNotMatch(html, /Web検索テストを実行/);
  assert.match(html, /<section[^>]*aria-label="変更履歴"[^>]*hidden/);
  assert.doesNotMatch(html, /公式のリリース日とは異なります/);
});
test('catalog changes shows detected timestamps, readable differences, safe sources and numbered pages', async () => {
  const id = '11111111-1111-4111-8111-111111111111';
  const before = {
    display_name: 'Future',
    enabled: true,
    available: true,
    text_input: null,
    structured_output: null,
    context_length: 200000,
    max_input_tokens: null,
    max_output_tokens: null,
    price: null,
  };
  const state = {
    status: 'ready',
    changes: [
      {
        id,
        model_id: id,
        provider: 'openai',
        model_name: 'Future',
        upstream_model_id: 'future',
        kind: 'updated',
        occurred_at: '2026-11-01T00:00:00Z',
        source_url: 'https://openai.com/api/pricing/',
        before,
        after: { ...before, context_length: 400000 },
      },
    ],
    total: 45,
    limit: 20,
    offset: 0,
    filter: {},
    error: null,
  };
  const Component = (
    await import(
      await component('CatalogChanges', {
        './catalog-changes-controller.ts': override(
          './catalog-changes-controller.ts',
          'emptyCatalogChangesView',
          state,
        ),
      })
    )
  ).default;
  const html = render(Component, {
    props: { token: async () => null, onDenied: () => {} },
  }).body;
  assert.match(html, /公式のリリース日とは異なります/);
  assert.match(html, /検知日時（日本時間）/);
  assert.match(html, /Future/);
  assert.match(html, /200,000/);
  assert.match(html, /400,000/);
  assert.match(html, /<details/);
  assert.match(html, /href="https:\/\/openai.com\/api\/pricing\/"/);
  assert.match(html, /aria-label="2ページへ"/);
});
test('price research displays unknown prices, pending results, official evidence and ready guards', async () => {
  const id = '11111111-1111-4111-8111-111111111111';
  const saved = {
    version: 1,
    provider: 'openai',
    model_id: id,
    automatic_enabled: true,
    interval_hours: 24,
    next_run_at: null,
    ready: false,
    latest_run: {
      id,
      kind: 'update',
      target_provider: 'liquid',
      state: 'unknown',
      error_code: 'lease_expired',
      created_at: '2026-11-01T00:00:00Z',
      started_at: null,
      finished_at: null,
      updated_count: 2,
      unresolved_count: 3,
      target_count: 10,
      completed_count: 7,
      non_text_count: 2,
      unclassified_count: 1,
      call_id: null,
      sources: ['https://docs.liquid.ai/pricing'],
    },
  };
  const Component = (
    await import(
      await component('PriceResearch', {
        './price-research-controller.ts': override(
          './price-research-controller.ts',
          'emptyPriceResearchView',
          {
            ...emptyPriceResearchView(),
            status: 'ready',
            saved,
            draft: {
              expected_version: 1,
              model_id: id,
              automatic_enabled: true,
              interval_hours: 24,
            },
            models: [{ ...model(), provider: 'openai' }],
            total: 1,
          },
        ),
      })
    )
  ).default;
  const html = render(Component, {
    props: {
      catalog: {},
      token: async () => null,
      onDenied: () => {},
      onSync: () => {},
    },
  }).body;
  assert.match(html, /料金未取得/);
  assert.match(html, /定期更新は\s+Web検索テストの成功まで保留/);
  assert.match(html, /結果不明です。自動で再送しません/);
  assert.match(html, /2 件 \/ 3 件/);
  assert.match(html, /7 件 \/ 10 件/);
  assert.match(html, /2 件 \/ 1 件/);
  assert.doesNotMatch(html, /料金調査モデルのページ|最大 20/);
  assert.match(html, /href="https:\/\/docs.liquid.ai\/pricing"/);
  assert.match(
    html,
    /<button[^>]*disabled[^>]*>[^]*?選んだ提供元の公式価格を更新/,
  );
  assert.doesNotMatch(html, /<input[^>]*(?:name|id)="[^"]*model/);
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
  assert.match(html, /<select[^>]*aria-label="集計年"/);
  assert.match(html, /<select[^>]*aria-label="集計月"/);
  assert.doesNotMatch(html, /type="month"/);
  assert.match(html, /class="library-header/);
  assert.match(html, /class="library-panel/);
  assert.match(html, /aria-label="原価の表示"/);
  assert.match(html, /<section[^>]*aria-label="ユーザー・メール別"[^>]*hidden/);
  assert.match(html, /<section[^>]*aria-label="呼出し履歴"[^>]*hidden/);
  assert.match(html, /<details[^>]*>[\s\S]*?<summary>詳細条件/);
});

test('usage expands user totals into labeled mail costs with stage details and a history action', async () => {
  const totals = [
    {
      currency: 'USD',
      amount: '0.123',
      estimated_amount: '0.123',
      reported_amount: '0',
      calls: 3,
      unknown_calls: 1,
      failures: 1,
    },
  ];
  const account = {
    account_id: 'account-id',
    label: '仕事用メール',
    provider: 'gmail',
    calls: 3,
    unknown_calls: 1,
    failures: 1,
    totals,
    stages: [
      { stage: 'classification', totals },
      { stage: 'extraction', totals },
    ],
  };
  const state = {
    ...emptyUsageView(),
    status: 'ready',
    filter: {
      month: '2026-10',
      user_id: 'consumer-id',
      account_id: 'account-id',
    },
    report: {
      ...report(),
      users_total: 1,
      users: [
        {
          user_id: 'consumer-id',
          calls: 6,
          unknown_calls: 2,
          failures: 2,
          totals,
          mail_accounts: [
            account,
            { ...account, account_id: null, label: '', provider: '' },
          ],
        },
      ],
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
  assert.match(html, /ユーザー・メール別/);
  assert.match(html, /メール接続別の内訳/);
  assert.match(html, /仕事用メール/);
  assert.match(html, /接続先不明/);
  assert.match(html, /一次判定/);
  assert.match(html, /二次処理/);
  assert.match(html, /このメールの呼出し履歴/);
  assert.match(html, /メールの絞り込みを解除/);
  assert.match(html, /原価不明 1 件/);
  assert.match(html, /失敗・結果不明 1 件/);
  assert.doesNotMatch(html, /<h[1-6][^>]*>account-id/);
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
