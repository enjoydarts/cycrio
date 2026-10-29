import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import { subscription } from './subscriptions-fixtures.ts';
import { overviewFixture } from './overview-fixtures.ts';
async function componentURL(name: string): Promise<string> {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const nested: Record<string, string> = {};
  for (const m of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    nested[`./${m[1]}.svelte`] = await componentURL(m[1]);
  return `data:text/javascript;base64,${Buffer.from(js.code.replace(/^import\s+['"][^'"]+\.css['"];?\n/gm, '').replace(/(from\s+|import\s*)(['"])([^'"]+)\2/g, (_m, prefix, q, path) => `${prefix}${q}${nested[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${q}`)).toString('base64')}`;
}
test('library list and cards expose real amounts, unknown sources and accessible detail actions', async () => {
  const Component = (await import(await componentURL('SubscriptionListPanel')))
    .default;
  for (const layout of ['list', 'cards']) {
    const html = render(Component, {
      props: {
        items: [
          subscription(),
          {
            ...subscription(),
            id: '22222222-2222-4222-8222-222222222222',
            service: 'Unknown',
            amount: null,
            currency: null,
          },
        ],
        layout,
        onSelect: () => {},
      },
    }).body;
    assert.match(html, /0 JPY/);
    assert.match(html, /金額不明/);
    assert.match(html, /取得元情報なし/);
    assert.doesNotMatch(html, /手動登録/);
    assert.match(html, /サービスの詳細/);
  }
});

async function libraryHTML(query = '', available = true) {
  const source = await readFile(
    new URL('./Subscriptions.svelte', import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const module = (code: string) =>
    `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
  const stubs: Record<string, string> = {
    '$app/state': module(
      `export const page = {url:new URL('https://test/subscriptions${query}')}`,
    ),
    '$app/navigation': module(
      'export const goto = () => {}; export const beforeNavigate = () => {}',
    ),
    '$app/env/public': module("export const PUBLIC_API_BASE_URL = ''"),
    './app-context.ts': module(
      'export const getAppSession = () => ({token:async()=>null,onDenied:()=>{}})',
    ),
    './workspace-context.ts': module(
      `export const getWorkspace=()=>({view:{accounts:{value:[]},overview:{status:'${available ? 'ready' : 'loading'}',value:${JSON.stringify(overviewFixture())}}},refresh:async()=>{}})`,
    ),
    './SubscriptionListPanel.svelte': await componentURL(
      'SubscriptionListPanel',
    ),
    './LibraryHeader.svelte': await componentURL('LibraryHeader'),
    './LibraryPanel.svelte': await componentURL('LibraryPanel'),
  };
  for (const name of [
    'Modal',
    'SubscriptionForm',
    'SubscriptionDetail',
    'SubscriptionDrawer',
  ])
    stubs[`./${name}.svelte`] = module(
      'export default function InactiveDialog() {}',
    );
  const code = js.code
    .replace(/^import\s+['"][^'"]+\.css['"];?\n/gm, '')
    .replace(
      /(^import[^\n]*?from\s+|^import\s*)(['"])([^'"]+)\2/gm,
      (_m, p, q, path) =>
        `${p}${q}${stubs[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${q}`,
    );
  return render((await import(module(code))).default)
    .body.replace(/<!--[\s\S]*?-->/g, '')
    .replace(/ class="[^"]*"/g, '');
}
test('library summary uses formal counts and actual decimal payments while source scope hides global totals', async () => {
  const html = await libraryHTML();
  assert.match(html, /href="\/analysis\?tab=attention"/);
  assert.match(html, /123456789012345678901234.123456789012 USD/);
  assert.match(html, /すべて<span>4<\/span>/);
  assert.match(html, /利用中<span>2<\/span>/);
  const scoped = await libraryHTML(
    '?account_id=11111111-1111-4111-8111-111111111111',
  );
  assert.doesNotMatch(scoped, /今月の支払実績|すべて<span>/);
  assert.match(
    scoped,
    /href="\/import\?account_id=11111111-1111-4111-8111-111111111111"/,
  );
  assert.match(scoped, /status=active[^"<>]*account_id=11111111/);
  assert.doesNotMatch(
    await libraryHTML('', false),
    /今月の支払実績|すべて<span>/,
  );
});
