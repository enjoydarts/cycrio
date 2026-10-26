import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile, access } from 'node:fs/promises';
import { compile, parse } from 'svelte/compiler';
import { render } from 'svelte/server';

async function component(name: string) {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+|import\s*)(['"])([^'"]+)\2/g,
    (_m, prefix, quote, path) =>
      `${prefix}${quote}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${quote}`,
  );
  return (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
}
test('root layout owns one authentication controller and a stable authorization scope for child routes', async () => {
  const source = await readFile(
    new URL('../routes/+layout.svelte', import.meta.url),
    'utf8',
  );
  assert.match(source, /createAuthController\(/);
  assert.match(source, /createSessionBoundary\(/);
  assert.match(source, /#key sessionState.scope/);
  assert.match(source, /sso-callback/);
  for (const route of [
    '+page.svelte',
    'admin/+page.svelte',
    'mail/+page.svelte',
    'sso-callback/+page.svelte',
  ]) {
    const page = await readFile(
      new URL(`../routes/${route}`, import.meta.url),
      'utf8',
    );
    assert.doesNotMatch(page, /AccessPanel|createAuthController/);
  }
});
test('settings navigation renders real routes and withholds administrator names and links from owners', async () => {
  await assert.doesNotReject(
    access(new URL('./SettingsNavigation.svelte', import.meta.url)),
  );
  const Component = await component('SettingsNavigation');
  const owner = render(Component, {
    props: { role: 'user', section: 'mail' },
  }).body;
  assert.match(owner, /href="\/settings\/mail"[^>]*aria-current="page"/);
  assert.match(owner, /href="\/settings\/appearance"/);
  assert.doesNotMatch(owner, /credentials|catalog|AI モデル|管理者設定/);
  const admin = render(Component, {
    props: { role: 'platform_admin', section: 'usage' },
  }).body;
  assert.match(admin, /href="\/settings\/usage"[^>]*aria-current="page"/);
  assert.match(admin, /href="\/settings\/mail-policy"/);
});
test('static settings composition includes permission branches and each settings component', async () => {
  const source = await readFile(
    new URL('../routes/settings/[section]/+page.svelte', import.meta.url),
    'utf8',
  ).catch(() => '');
  assert.match(source, /settingsAccess/);
  assert.match(source, /access === 'denied'/);
  assert.match(source, /access === 'not_found'/);
  for (const name of [
    'MailAccounts',
    'Appearance',
    'AdminCredentials',
    'ModelCatalog',
    'AIModelSettings',
    'AIRuntime',
    'MailProcessingPolicy',
    'AIUsage',
  ])
    assert.match(source, new RegExp(`<${name}\\b`));
});
test('internal model and price UUIDs are available only inside technical disclosure', async () => {
  for (const [filename, expressions] of [
    ['AIModelSettings', ['model.current_price?.id', 'savedPrice']],
    ['ModelCatalog', ['model.id']],
  ] as const) {
    const source = await readFile(
      new URL(`./${filename}.svelte`, import.meta.url),
      'utf8',
    );
    const found = new Set<string>();
    function visit(value: unknown, disclosed = false) {
      if (!value || typeof value !== 'object') return;
      if (Array.isArray(value)) {
        value.forEach((child) => visit(child, disclosed));
        return;
      }
      const node = value as Record<string, unknown>;
      const inside = disclosed || node.name === 'details';
      if (
        node.type === 'ExpressionTag' &&
        node.expression &&
        typeof node.expression === 'object'
      ) {
        const expression = node.expression as { start: number; end: number };
        const text = source.slice(expression.start, expression.end);
        for (const expected of expressions)
          if (text.includes(expected)) {
            found.add(expected);
            assert.equal(inside, true, `${filename}: ${expected}`);
          }
      }
      Object.values(node).forEach((child) => visit(child, inside));
    }
    visit(parse(source, { modern: true }));
    assert.equal(found.size, expressions.length);
  }
});
