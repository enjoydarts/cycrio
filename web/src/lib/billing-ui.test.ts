import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile, readdir } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
const source = (name: string) =>
  readFile(new URL(`./${name}`, import.meta.url), 'utf8').catch(() => '');
test('subscription detail routes accept UUIDs and leave removed summary URLs unmatched', async () => {
  const routes = await readdir(
    new URL('../routes/subscriptions/', import.meta.url),
  );
  assert.ok(routes.includes('[id=uuid]'));
  assert.ok(!routes.includes('[id]'));
  const kitRoot = new URL('../../', import.meta.resolve('@sveltejs/kit/vite'));
  const { load_vite_config, extract_svelte_config } = await import(
    new URL('core/config/index.js', kitRoot).href
  );
  const { default: createManifest } = await import(
    new URL('core/sync/create_manifest_data/index.js', kitRoot).href
  );
  const { load_and_validate_params } = await import(
    new URL('core/params.js', kitRoot).href
  );
  const vite = await load_vite_config();
  const manifest = createManifest(extract_svelte_config(vite), vite.root);
  assert.equal(manifest.params, 'src/params.ts');
  const params = await load_and_validate_params({
    routes: manifest.routes,
    params_path: manifest.params,
    root: vite.root,
  });
  const match = params.uuid;
  assert.equal(match('summary'), false);
  assert.equal(match('11111111-1111-4111-8111-111111111111'), true);
  assert.equal(match('invalid'), false);
});
test('billing history accepts a parent draft store and reports unsaved editor state across detail lifetimes', async () => {
  const history = await source('BillingHistory.svelte');
  assert.match(history, /drafts\?: BillingDrafts/);
  assert.match(history, /if \(!providedDrafts\) drafts.clear\(\)/);
  assert.match(history, /onDirty/);
  assert.match(await source('BillingEditor.svelte'), /onDirty\(next.dirty\)/);
});
test('billing history stays in detail while standalone monthly summaries are removed', async () => {
  const detail = await source('SubscriptionDetail.svelte');
  assert.match(detail, /BillingHistory/);
  assert.match(detail, /tab=history/);
  assert.match(detail, /概要/);
  assert.match(detail, /請求・支払い履歴/);
  assert.doesNotMatch(
    await source('Subscriptions.svelte'),
    /href="\/subscriptions\/summary"/,
  );
  assert.equal(
    await readFile(
      new URL('../routes/subscriptions/summary/+page.svelte', import.meta.url),
      'utf8',
    ).catch(() => ''),
    '',
  );
  for (const name of ['AppShell.svelte', 'BillingHistory.svelte'])
    assert.doesNotMatch(
      await source(name),
      /subscriptions\/summary|月次集計|支払・返金/,
    );
});
test('registered AI event offers explicit history navigation preserving event zero without writing', async () => {
  const detail = await source('AnalysisDetail.svelte');
  assert.match(detail, /支払・請求履歴を見る/);
  assert.match(detail, /source_result_id/);
  assert.match(detail, /source_event_index/);
});
test('billing components compile accessibly and mutations stay in controllers', async () => {
  for (const name of [
    'BillingHistory',
    'BillingEditor',
    'BillingSources',
    'BillingTable',
  ]) {
    const code = await source(`${name}.svelte`);
    assert.ok(code.length, name);
    const result = compile(code, {
      filename: `${name}.svelte`,
      generate: 'server',
    });
    assert.deepEqual(
      result.warnings.map((w) => w.code),
      [],
      name,
    );
    assert.doesNotMatch(
      code,
      /createAuthController|\.create\(|\.update\(/,
      name,
    );
  }
  const editor = await source('BillingEditor.svelte');
  assert.match(editor, /onBusyChange/);
  assert.match(editor, /内容を確認しました/);
  assert.match(editor, /別の取引/);
  assert.match(editor, /同じ要求で再確認/);
});
