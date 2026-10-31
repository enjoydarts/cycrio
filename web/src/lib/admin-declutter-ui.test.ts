import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile, parse } from 'svelte/compiler';
import { render } from 'svelte/server';
import { credentialProviders } from './api.ts';
import { emptyIntegrationState } from './integrations-controller.ts';
import { setupFixture } from './ai-setup-test-fixtures.ts';
import { createAISetupController } from './ai-setup-controller.ts';
import { run } from './airuntime-test-fixtures.ts';

test('all administration screens share the subscription header and contain purpose navigation inside its panel', async () => {
  for (const name of ['AdminCredentials', 'IntegrationSettings', 'AISetup']) {
    const source = await readFile(
      new URL(`./${name}.svelte`, import.meta.url),
      'utf8',
    );
    const tree = parse(source, { modern: true }) as any;
    let header = false,
      panel = false,
      navigation = false;
    function visit(node: any, ancestors: string[] = []) {
      if (!node || typeof node !== 'object') return;
      if (Array.isArray(node)) {
        node.forEach((child) => visit(child, ancestors));
        return;
      }
      if (node.name === 'LibraryHeader') header = true;
      if (node.name === 'LibraryPanel') panel = true;
      if (node.name === 'nav' && ancestors.includes('LibraryPanel'))
        navigation = true;
      for (const child of Object.values(node))
        visit(child, [...ancestors, node.name ?? '']);
    }
    visit(tree);
    assert.ok(header, `${name} header`);
    assert.ok(panel, `${name} panel`);
    assert.ok(navigation, `${name} panel navigation`);
  }
});

const navigation =
  'data:text/javascript,' +
  encodeURIComponent(
    'export function beforeNavigate(){}; export const page={url:new URL("https://example.test/settings/ai")};',
  );
async function component(name: string, state: Record<string, unknown> = {}) {
  let source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  if (name === 'AISetup')
    source = source.replace(
      'const session = getAppSession();',
      'const session = { user: { role: "platform_admin" } };',
    );
  const ast = parse(source, { modern: true }) as any;
  const edits: { start: number; end: number; value: string }[] = [];
  for (const statement of ast.instance?.content.body ?? [])
    for (const declaration of statement.declarations ?? []) {
      if (
        declaration.id?.type === 'Identifier' &&
        declaration.id.name in state
      ) {
        const argument = declaration.init.arguments?.[0];
        if (argument)
          edits.push({
            start: argument.start,
            end: argument.end,
            value: JSON.stringify(state[declaration.id.name]),
          });
      }
    }
  for (const edit of edits.sort((a, b) => b.start - a.start))
    source = source.slice(0, edit.start) + edit.value + source.slice(edit.end);
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const nested: Record<string, string> = {};
  for (const match of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    nested[`./${match[1]}.svelte`] = await component(match[1]);
  const code = js.code
    .replace(/^import\s+['"][^'"]+\.css['"];?$/gm, '')
    .replace(
      /(from\s+|^import\s+)(['"])([^'"]+)\2/gm,
      (_m, p, q, path) =>
        `${p}${q}${path.startsWith('$app/') ? navigation : (nested[path] ?? (path.startsWith('.') ? new URL(path.endsWith('.ts') ? path : `${path}.ts`, import.meta.url).href : import.meta.resolve(path)))}${q}`,
    );
  return `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
}
const credentials = {
  status: 'ready',
  credentials: credentialProviders.map((provider) => ({
    provider,
    state: 'configured',
    version: 2,
    updated_at: null,
  })),
  events: [],
  error: null,
  success: null,
};
test('credential landing shows service statuses without mounting nine secret editors', async () => {
  const Component = (
    await import(await component('AdminCredentials', { view: credentials }))
  ).default;
  const html = render(Component, {
    props: { api: {}, token: async () => '', onDenied() {} },
  }).body;
  assert.match(html, /<h1[^>]*>API キー/);
  assert.equal((html.match(/<form\b/g) ?? []).length, 0);
  assert.equal((html.match(/type="password"/g) ?? []).length, 0);
  assert.match(html, /Liquid AI/);
  assert.match(html, /Outlook/);
  assert.match(html, /操作履歴/);
});
test('one selected credential editor is available without exposing other provider forms', async () => {
  const Component = (
    await import(
      await component('AdminCredentials', {
        view: credentials,
        selectedProvider: 'openai',
      })
    )
  ).default;
  const html = render(Component, {
    props: { api: {}, token: async () => '', onDenied() {} },
  }).body;
  assert.equal((html.match(/<form\b/g) ?? []).length, 1);
  assert.match(html, /id="secret-openai"/);
  assert.doesNotMatch(
    html,
    /id="secret-liquid"|id="client-secret-gmail_oauth"/,
  );
  assert.match(html, /サービス一覧に戻る/);
});
test('credential selection preserves dirty secrets until explicit discard and blocks mutation-time switching', async () => {
  const source = await readFile(
    new URL('./AdminCredentials.svelte', import.meta.url),
    'utf8',
  );
  const ast = parse(source, { modern: true }) as any;
  const fn = (name: string) =>
    ast.instance.content.body.find(
      (node: any) =>
        node.type === 'FunctionDeclaration' && node.id.name === name,
    );
  const select = fn('selectProvider'),
    discard = fn('discardEditor');
  const factory = new Function(
    'busy',
    `let selectedProvider='openai',dirty=true,nextProvider=null,discardConfirmation=false,confirmation=null; const select=${source.slice(select.start, select.end).replace('value: CredentialProvider | null', 'value')}; const discard=${source.slice(discard.start, discard.end)}; return {select,discard,state:()=>({selectedProvider,dirty,discardConfirmation})};`,
  );
  const state = factory(false);
  state.select(null);
  assert.deepEqual(state.state(), {
    selectedProvider: 'openai',
    dirty: true,
    discardConfirmation: true,
  });
  state.discard();
  assert.deepEqual(state.state(), {
    selectedProvider: null,
    dirty: false,
    discardConfirmation: false,
  });
  const busy = factory(true);
  busy.select(null);
  busy.discard();
  assert.deepEqual(busy.state(), {
    selectedProvider: 'openai',
    dirty: true,
    discardConfirmation: false,
  });
});
test('integration landing prioritizes saved setup and separates webhook, send and history purposes', async () => {
  const Component = (
    await import(
      await component('IntegrationSettings', {
        view: {
          ...emptyIntegrationState(),
          view: {
            config: {
              version: 1,
              sender: 'sender@example.test',
              clerk_instance_id: 'ins_test',
            },
            resend_ready: true,
            clerk_webhook_ready: true,
            resend_webhook_ready: true,
            profile_ready: true,
            profile_synced_at: null,
            attempts: [],
          },
          sender: 'sender@example.test',
          instance: 'ins_test',
          version: 1,
        },
      })
    )
  ).default;
  const html = render(Component, {
    props: { api: {}, token: async () => '', onDenied() {} },
  }).body;
  assert.match(html, /<h1[^>]*>メール送信・外部イベント/);
  assert.match(html, /aria-label="連携の設定項目"/);
  assert.doesNotMatch(html, /テストメールを1通送信|\/v1\/webhooks\/clerk/);
  assert.equal((html.match(/<form\b/g) ?? []).length, 0);
  assert.match(html, /送信設定を編集/);
});
test('successful AI test presents activation as the next action without another paid test form', async () => {
  const fixture = setupFixture();
  const controller = createAISetupController(
    fixture.api,
    async () => 'synthetic',
    () => {},
    () => {},
  );
  await controller.load();
  const view = structuredClone(controller.state());
  view.runtime!.latest_test = { ...run(), state: 'succeeded' } as any;
  const Component = (
    await import(
      await component('AISetup', { view, pane: 'test', initialized: true })
    )
  ).default;
  const html = render(Component).body;
  assert.match(html, /<h1[^>]*>AI の準備/);
  assert.match(html, /成功したテストの版を有効化/);
  assert.doesNotMatch(
    html,
    /type="checkbox"[^>]*[^]*外部 AI へのテスト送信と料金/,
  );
  assert.match(html, /再テスト/);
  assert.doesNotMatch(html, /AI の実行を停止する/);
});
test('model and price controls remain rendered after a previous HTTP 400 test', async () => {
  const fixture = setupFixture();
  fixture.state.runtime.latest_test = {
    ...run(),
    state: 'failed',
    primary_outcome: 'success',
    secondary_outcome: 'provider_http',
    secondary_failure: {
      http_status: 400,
      reason: 'request_rejected',
      hint: '送信形式を確認してください。',
    },
  } as any;
  const controller = createAISetupController(
    fixture.api,
    async () => 'synthetic',
    () => {},
    () => {},
  );
  await controller.load();
  const Component = (
    await import(
      await component('AISetup', {
        view: controller.state(),
        pane: 'models',
        initialized: true,
      })
    )
  ).default;
  const html = render(Component).body;
  assert.match(html, /一次判定<select/);
  assert.match(html, /選択モデルの参考料金/);
  assert.match(html, /不明（無料を意味しません）/);
});
test('route selection identifies Google endpoint regions without showing unknown precision', async () => {
  const fixture = setupFixture();
  fixture.state.routes.routes[0] = {
    ...fixture.state.routes.routes[0],
    provider_name: 'Google',
    tag: 'google-vertex/europe',
    quantization: 'Unknown',
  };
  fixture.state.runtime.draft!.openrouter_provider = 'google-vertex/europe';
  const controller = createAISetupController(
    fixture.api,
    async () => 'synthetic',
    () => {},
    () => {},
  );
  await controller.load();
  const Component = (
    await import(
      await component('AISetup', {
        view: controller.state(),
        pane: 'models',
        initialized: true,
      })
    )
  ).default;
  const html = render(Component).body;
  assert.match(html, /Google Vertex AI · 欧州/);
  assert.doesNotMatch(html, /Unknown|unknown/);
});
test('fresh model setup presents one route summary without duplicate metadata panels', async () => {
  const fixture = setupFixture();
  const controller = createAISetupController(
    fixture.api,
    async () => 'synthetic',
    () => {},
    () => {},
  );
  await controller.load();
  const Component = (
    await import(
      await component('AISetup', {
        view: controller.state(),
        pane: 'models',
        initialized: true,
      })
    )
  ).default;
  const html = render(Component).body;
  assert.doesNotMatch(html, /最新の適合配信先を表示しています/);
  assert.match(html, /配信先を確認・変更/);
  assert.match(html, /不明（無料を意味しません）/);
});
test('an integration pending request remains visible and reachable when viewing history', async () => {
  const view = {
    ...emptyIntegrationState(),
    pending: {
      request_id: '00000000-0000-4000-8000-000000000001',
      expected_version: 1,
      confirm: true,
    },
    error: 'dependency_unavailable',
  };
  const Component = (
    await import(
      await component('IntegrationSettings', { view, pane: 'history' })
    )
  ).default;
  const html = render(Component, {
    props: { api: {}, token: async () => '', onDenied() {} },
  }).body;
  assert.match(html, /元の要求を保持しています/);
  assert.match(html, /送信要求の確認へ/);
  assert.match(html, /role="alert"/);
  assert.doesNotMatch(html, /テストメールを1通送信/);
});
