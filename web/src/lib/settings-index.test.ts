import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
const moduleURL = (code: string) =>
  `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
async function componentURL(name: string, role: string): Promise<string> {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const context = moduleURL(
    `export const getAppSession=()=>({user:{role:'${role}'}})`,
  );
  const nested: Record<string, string> = {};
  for (const match of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    nested[`./${match[1]}.svelte`] = await componentURL(match[1], role);
  const code = js.code
    .replace(/^import\s+['"][^'"]+\.css['"];?\n/gm, '')
    .replace(
      /(^import[^\n]*?from\s+|^import\s+)(['"])([^'"]+)\2/gm,
      (_m, p, q, path) =>
        `${p}${q}${nested[path] ?? (path === './app-context.ts' ? context : path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${q}`,
    );
  return moduleURL(code);
}
async function settingsHTML(role: string) {
  return render(
    (await import(await componentURL('SettingsOverview', role))).default,
  ).body;
}
test('settings is a single purpose index without duplicated readiness panels or background service reads', async () => {
  const html = await settingsHTML('platform_admin');
  for (const id of [
    'mail',
    'appearance',
    'ai',
    'credentials',
    'integrations',
    'catalog',
    'usage',
  ])
    assert.equal(
      (html.match(new RegExp(`href="/settings/${id}"`, 'g')) ?? []).length,
      1,
      id,
    );
  assert.doesNotMatch(
    html,
    /次のステップ|準備状況を更新|接続・対象フォルダーの準備済み/,
  );
  assert.equal((html.match(/<h1\b/g) ?? []).length, 1);
  const source = await readFile(
    new URL('./SettingsOverview.svelte', import.meta.url),
    'utf8',
  );
  assert.doesNotMatch(source, /onMount|\.credentials\(|\.preview\(|\.state\(/);
});
test('personal settings index never offers administrator destinations', async () => {
  const html = await settingsHTML('user');
  assert.match(html, /href="\/settings\/mail"/);
  assert.match(html, /href="\/settings\/appearance"/);
  assert.doesNotMatch(
    html,
    /href="\/settings\/(ai|credentials|integrations|catalog|usage)"/,
  );
});
