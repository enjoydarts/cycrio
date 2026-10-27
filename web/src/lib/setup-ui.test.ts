import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { settingsAccess } from './settings.ts';
import { compile, parse } from 'svelte/compiler';
import { render } from 'svelte/server';
import { parseModel } from './catalog-api.ts';
import { modelFixture } from './catalog-test-fixtures.ts';
import { processingPolicy } from './mail-processing-fixtures.ts';
test('compact price form emits valid currency and decimal patterns in actual HTML', async () => {
  const source = await readFile(
    new URL('./SetupPrice.svelte', import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+|import\s*)(['"])([^'"]+)\2/g,
    (_m, prefix, quote, path) =>
      `${prefix}${quote}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${quote}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const html = render(Component, {
    props: {
      model: parseModel(modelFixture()),
      busy: false,
      save: async () => true,
      onclose: () => {},
    },
  }).body;
  const patterns = [...html.matchAll(/pattern="([^"]+)"/g)].map((m) => m[1]);
  assert.ok(patterns.includes('[A-Z]{3}'));
  const currency = new RegExp(
    `^(?:${patterns.find((p) => p.startsWith('[A-Z]'))})$`,
  );
  for (const value of ['USD', 'JPY']) assert.equal(currency.test(value), true);
  for (const value of ['US', 'USDD', 'usd'])
    assert.equal(currency.test(value), false);
  const decimal = new RegExp(
    `^(?:${patterns.find((p) => p.startsWith('(?:0|'))})$`,
  );
  for (const value of ['0', '0.02', '123.456'])
    assert.equal(decimal.test(value), true);
  for (const value of ['1e3', '-1', '1,2', '1x2'])
    assert.equal(decimal.test(value), false);
});
test('settings overview stays on settings and unified AI route is administrator only', async () => {
  const source = await readFile(
    new URL('../routes/settings/+page.svelte', import.meta.url),
    'utf8',
  );
  assert.ok(
    !source.includes("goto('/settings/mail'"),
    'settings must show real readiness instead of redirecting',
  );
  assert.equal(settingsAccess('ai', 'platform_admin'), 'allowed');
  assert.equal(settingsAccess('ai', 'user'), 'denied');
});
test('both analysis entry points explain disabled consent with the relevant action', async () => {
  for (const name of ['AnalysisStart', 'MailProcessing']) {
    const source = await readFile(
      new URL(`./${name}.svelte`, import.meta.url),
      'utf8',
    );
    assert.match(source, /<ProcessingReadiness/);
    assert.match(source, /returnTo/);
  }
});
async function setupComponentURL(name: string): Promise<string> {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const nested: Record<string, string> = {};
  for (const match of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    nested[`./${match[1]}.svelte`] = await setupComponentURL(match[1]);
  const code = js.code.replace(
    /(from\s+|import\s*)(['"])([^'"]+)\2/g,
    (_m, prefix, quote, path) =>
      `${prefix}${quote}${nested[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${quote}`,
  );
  return `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
}
test('owner mail setup returns to the validated analysis context without invoking any API during rendering', async () => {
  const Component = (await import(await setupComponentURL('MailAccounts')))
    .default;
  const target =
    '/analysis?account_id=11111111-1111-4111-8111-111111111111&state=failed&offset=20&tab=all&setup_account=11111111-1111-4111-8111-111111111111&setup_max=7&start=true';
  const api = new Proxy(
    {},
    {
      get() {
        throw Error('rendering must not perform mail or AI calls');
      },
    },
  );
  for (const [returnTo, href] of [
    [target, target],
    ['https://evil.test/', '/analysis'],
    ['//evil.test/', '/analysis'],
  ]) {
    const html = render(Component, {
      props: { api, token: async () => null, onDenied: () => {}, returnTo },
    }).body;
    assert.ok(html.includes('解析へ戻って条件を確認'));
    assert.ok(html.includes(`href="${href.replaceAll('&', '&amp;')}"`));
    assert.doesNotMatch(html, /href="(?:https:)?\/\/evil/);
  }
  const page = await readFile(
    new URL('../routes/settings/[section]/+page.svelte', import.meta.url),
    'utf8',
  );
  assert.match(page, /returnTo=\{page.url.searchParams.get\('return_to'\)\}/);
});
test('permission editor stays mounted while switching between setup stages', async () => {
  const source = await readFile(
    new URL('./AISetup.svelte', import.meta.url),
    'utf8',
  );
  let found = false;
  function walk(value: unknown, ancestors: Record<string, unknown>[] = []) {
    if (!value || typeof value !== 'object') return;
    if (Array.isArray(value)) {
      for (const child of value) walk(child, ancestors);
      return;
    }
    const node = value as Record<string, unknown>;
    if (
      node.name === 'StandardMailPolicy' &&
      ['Component', 'InlineComponent'].includes(String(node.type))
    ) {
      found = true;
      for (const ancestor of ancestors) {
        if (ancestor.type !== 'IfBlock') continue;
        const expression = (ancestor.test ?? ancestor.expression) as
          { start: number; end: number } | undefined;
        assert.ok(expression);
        assert.doesNotMatch(
          source.slice(expression.start, expression.end),
          /\bpane\b/,
          'stage changes must preserve the editor and its original consent baseline',
        );
      }
    }
    for (const child of Object.values(node)) walk(child, [...ancestors, node]);
  }
  walk(parse(source, { modern: true }));
  assert.equal(found, true);
});
test('acknowledged stop callbacks never adopt purged props or revive a disposed permission editor', async () => {
  const source = await readFile(
    new URL('./StandardMailPolicy.svelte', import.meta.url),
    'utf8',
  );
  const functions: Record<string, string> = {};
  function walk(value: unknown) {
    if (!value || typeof value !== 'object') return;
    if (Array.isArray(value)) {
      value.forEach(walk);
      return;
    }
    const node = value as {
      type?: string;
      id?: { name: string };
      start?: number;
      end?: number;
    };
    if (
      node.type === 'FunctionDeclaration' &&
      node.id &&
      ['adopt', 'stop'].includes(node.id.name)
    )
      functions[node.id.name] = source.slice(node.start, node.end);
    Object.values(value).forEach(walk);
  }
  walk(parse(source, { modern: true }));
  assert.ok(functions.adopt && functions.stop);
  for (const mode of ['props', 'disposed']) {
    let adoptions = 0;
    const stop = new Function(
      'initial',
      'createStandardReview',
      'onDirty',
      `let policyState=initial,busy=false,disposed=false,review=null,saved=false,legacyCustom=false;const saveCustom=async()=>{${mode === 'props' ? 'policyState=null;' : 'disposed=true;'}return true;};${functions.adopt};${functions.stop};return stop;`,
    )(
      processingPolicy(),
      () => {
        adoptions++;
        throw Error('purged props must not be adopted');
      },
      () => {},
    );
    await assert.doesNotReject(stop());
    assert.equal(adoptions, 0);
  }
});
