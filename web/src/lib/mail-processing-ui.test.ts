import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import {
  processingPreview,
  processingResult,
} from './mail-processing-fixtures.ts';
async function component(name: string) {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const nested: Record<string, string> = {};
  for (const match of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    nested[`./${match[1]}.svelte`] = await componentURL(match[1]);
  const code = js.code.replace(
    /(from\s+|import\s*)(['"])([^'"]+)\2/g,
    (_m, prefix, quote, path) =>
      `${prefix}${quote}${nested[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${quote}`,
  );
  return (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
}
async function componentURL(name: string) {
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
  return `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
}
test('actual results render unconfirmed AI extraction, preserve missing amounts and distinguish unknown, failed and unrelated', async () => {
  const Component = await component('MailProcessingResults');
  const html = render(Component, {
    props: {
      results: [
        processingResult(),
        processingResult({
          id: '77777777-7777-4777-8777-777777777777',
          message_id: '88888888-8888-4888-8888-888888888888',
          state: 'succeeded',
          classification: 'subscription_related',
          error_code: '',
          events: [
            {
              service: 'サービスA',
              plan: null,
              event_type: 'payment',
              amount: null,
              currency: null,
              billed_at: '2026-10-25',
              period_start: null,
              period_end: null,
              next_billing_at: null,
              interval_unit: null,
              interval_count: null,
            },
          ],
        }),
        processingResult({
          id: '99999999-9999-4999-8999-999999999999',
          state: 'failed',
          error_code: 'source_missing',
        }),
        processingResult({
          id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
          state: 'not_related',
          classification: 'not_related',
          error_code: '',
        }),
      ],
      canRetry: false,
      confirmationScope: '',
      onRetry: () => {},
    },
  }).body;
  for (const text of [
    'AI抽出・未確認',
    '結果不明',
    '処理失敗',
    '非関連',
    'サービスA',
    '2026-10-25',
    '再び料金が発生',
    '重複して処理',
  ])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /契約確定|周期推定済|0 円|0 USD|55555555|66666666/);
});
test('actual public model display identifies external destinations, routing, retention and unknown reference pricing', async () => {
  const Component = await component('MailProcessingModels');
  const html = render(Component, { props: { ...processingPreview() } }).body;
  for (const text of [
    'D1',
    'OpenRouter',
    'approved/provider',
    'ZDR',
    '参考料金',
    '不明',
    '確定費用',
    '課金上限',
    '4096',
    '1024',
  ])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /credential|workspace|22222222/);
});
test('actual owner and admin initial forms explain explicit consent, inactive prerequisites and official verification sources without side effects', async () => {
  const api = new Proxy(
    {},
    {
      get() {
        throw Error('SSR must never request a private endpoint');
      },
    },
  );
  const owner = render(await component('MailProcessing'), {
    props: {
      account: { id: '11111111-1111-4111-8111-111111111111', version: 1 },
      api,
      token: async () => '',
      onDenied: () => {},
    },
  }).body;
  for (const text of [
    'メール本文の手動解析',
    '今回解析する上限',
    '外部送信と料金',
    '最新20件',
    '保存や読み込みでは',
  ])
    assert.ok(owner.includes(text), text);
  assert.match(owner, /required/);
  assert.doesNotMatch(owner, /value="(?:10|100)"/);
  const admin = render(await component('MailProcessingPolicy'), {
    props: { api, token: async () => '', onDenied: () => {} },
  }).body.replace(/\s+/g, ' ');
  for (const text of [
    'メール解析の送信条件',
    '契約条件',
    'Liquid AI',
    '保持条件',
    '秘密値や個人データ',
    'AI 設定を有効化',
  ])
    assert.ok(admin.includes(text), text);
  assert.match(admin, /https:\/\/www.liquid.ai\/terms-conditions/);
  assert.match(admin, /https:\/\/www.liquid.ai\/privacy-policy/);
});
