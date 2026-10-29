import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import { createRawSnippet } from 'svelte';
async function component(name: string) {
  const { js, warnings } = compile(
    await readFile(new URL(`./${name}.svelte`, import.meta.url), 'utf8'),
    { generate: 'server' },
  );
  assert.deepEqual(warnings, []);
  const code = js.code
    .replace(/^import\s+['"][^'"]+\.css['"];?\n/gm, '')
    .replace(
      /(from\s+|import\s*)(['"])([^'"]+)\2/g,
      (_m, p, q, path) =>
        `${p}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
    );
  return (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
}
const snippet = (value: string) =>
  createRawSnippet(() => ({ render: () => value }));
test('subscriptions and personal settings use the same page header and panel components', async () => {
  for (const name of [
    'Subscriptions',
    'SettingsOverview',
    'MailAccounts',
    'Appearance',
  ]) {
    const source = await readFile(
      new URL(`./${name}.svelte`, import.meta.url),
      'utf8',
    );
    assert.match(source, /<LibraryHeader\b/, name);
    assert.match(source, /<LibraryPanel\b/, name);
  }
});
test('shared library header owns one title, optional explanation and the primary action', async () => {
  const C = await component('LibraryHeader');
  const html = render(C, {
    props: {
      title: '確認が必要なメール',
      id: 'mail-heading',
      action: snippet('<a href="/import">メールを取り込む</a>'),
    },
  }).body;
  assert.match(html, /class="library-header/);
  assert.match(html, /<h1[^>]*id="mail-heading"/);
  assert.equal((html.match(/<h1\b/g) ?? []).length, 1);
  assert.match(html, /href="\/import"/);
  assert.doesNotMatch(html, /<p/);
});
test('shared library panel keeps tabs, toolbar and results within one surface', async () => {
  const C = await component('LibraryPanel');
  const html = render(C, {
    props: {
      children: snippet(
        '<nav class="library-tabs">表示</nav><form class="library-toolbar">検索</form><table><tbody><tr><td>結果</td></tr></tbody></table>',
      ),
    },
  }).body;
  assert.match(html, /class="library-panel/);
  assert.equal((html.match(/library-panel/g) ?? []).length, 1);
  assert.match(html, /library-tabs[\s\S]*library-toolbar[\s\S]*<table/);
});
