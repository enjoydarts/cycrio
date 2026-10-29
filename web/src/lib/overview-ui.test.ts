import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import { overviewFixture } from './overview-fixtures.ts';
import { parseOverview } from './overview-api.ts';

async function componentURL(name: string): Promise<string> {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const nested: Record<string, string> = {};
  for (const match of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    nested[`./${match[1]}.svelte`] = await componentURL(match[1]);
  const code = js.code
    .replace(/^import\s+['"][^'"]+\.css['"];?\n/gm, '')
    .replace(
      /(^import[^\n]*?from\s+|^import\s*)(['"])([^'"]+)\2/gm,
      (_m, p, q, path) =>
        `${p}${q}${nested[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${q}`,
    );
  return `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
}
const ready = () => ({
  page: parseOverview(overviewFixture()),
  status: 'ready' as const,
  error: null,
});
test('home HTML separates saved dates, unknown charges, review count and actual monthly payments', async () => {
  const Component = (await import(await componentURL('OverviewPanel'))).default;
  const html = render(Component, { props: { view: ready() } }).body;
  for (const text of [
    'ホーム',
    '保存済みの次回請求日',
    '金額不明',
    '0 JPY',
    '状態不明',
    '支払・返金実績',
    'JPY',
    'USD',
    '123456789012345678901234.123456789012',
  ])
    assert.ok(html.includes(text), text);
  assert.match(html, /href="\/analysis\?tab=attention"/);
  assert.match(html, /メール単位/);
  assert.match(
    html,
    /href="\/subscriptions\/11111111-1111-4111-8111-111111111111"/,
  );
  assert.doesNotMatch(
    html,
    /href="\/settings\/(?:ai|usage|credentials)|月額合計|請求確定額/,
  );
});
test('home loading or failed reads do not leave stale figures visible as fresh data', async () => {
  const Component = (await import(await componentURL('OverviewPanel'))).default;
  for (const status of ['loading', 'error', 'denied', 'disposed'] as const) {
    const html = render(Component, {
      props: {
        view: {
          ...ready(),
          status,
          error: status === 'error' ? 'dependency_unavailable' : null,
        },
      },
    }).body;
    assert.doesNotMatch(html, /日常のサービス|個人メール|123456789/);
    if (status === 'error') {
      assert.match(html, /role="alert"/);
      assert.match(html, /もう一度読み込む/);
    }
  }
});
test('brand keeps the real home accessible without selecting a library page on root', async () => {
  const Component = (await import(await componentURL('AppShell'))).default;
  const { createRawSnippet } = await import('svelte');
  const html = render(Component, {
    props: {
      user: { id: '11111111-1111-4111-8111-111111111111', role: 'user' },
      pathname: '/',
      signOut: () => {},
      children: createRawSnippet(() => ({ render: () => '<p>home</p>' })),
    },
  }).body;
  assert.match(html, /href="\/"[^>]*aria-label="Cycrio ホーム"/);
  assert.equal((html.match(/aria-current="page"/g) ?? []).length, 0);
});
