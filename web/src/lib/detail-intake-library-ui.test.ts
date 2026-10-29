import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
const moduleURL = (value: string) =>
  `data:text/javascript;base64,${Buffer.from(value).toString('base64')}`;
async function component(name: string): Promise<string> {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const imports: Record<string, string> = {
    '$app/navigation': moduleURL(
      'export const beforeNavigate=()=>{};export const goto=()=>{};',
    ),
    '$app/state': moduleURL(
      'export const page={url:new URL("https://test/subscriptions/example")};',
    ),
    '$app/env/public': moduleURL(
      'export const PUBLIC_API_BASE_URL="https://synthetic.invalid";',
    ),
    './app-context.ts': moduleURL(
      'export const getAppSession=()=>({token:async()=>null,onDenied:()=>{}});',
    ),
    './workspace-context.ts': moduleURL(
      'export const getWorkspace=()=>undefined;',
    ),
  };
  for (const m of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    imports[`./${m[1]}.svelte`] = ['LibraryHeader', 'LibraryPanel'].includes(
      m[1],
    )
      ? await component(m[1])
      : moduleURL('export default function InactiveChild(){}');
  const code = js.code
    .replace(/^import\s+['"][^'"]+\.css['"];?\n/gm, '')
    .replace(
      /(^import[\s\S]*?from\s+|^import\s*)(['"])([^'"]+)\2/gm,
      (_m, p, q, path) =>
        `${p}${q}${imports[path] ?? (path.startsWith('.') ? new URL(path.endsWith('.ts') ? path : `${path}.ts`, import.meta.url).href : import.meta.resolve(path))}${q}`,
    );
  return moduleURL(code);
}
test('intake uses the shared library header and one panel around its three steps', async () => {
  const Component = (await import(await component('IntakeWizard'))).default;
  const html = render(Component).body;
  assert.match(html, /class="library-header/);
  assert.match(html, /class="library-panel/);
  assert.match(html, /メールを取り込む/);
  assert.match(html, /取り込みの手順/);
});
test('full-page detail uses the shared library frame while the embedded drawer keeps its own layout', async () => {
  const Component = (await import(await component('SubscriptionDetail')))
    .default;
  const html = render(Component, {
    props: { id: '11111111-1111-4111-8111-111111111111' },
  }).body;
  assert.match(html, /class="library-header/);
  assert.match(html, /class="library-panel/);
  const embedded = render(Component, {
    props: { id: '11111111-1111-4111-8111-111111111111', embedded: true },
  }).body;
  assert.doesNotMatch(embedded, /class="library-header|class="library-panel/);
});
