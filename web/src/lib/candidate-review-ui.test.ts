import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
const moduleURL = (code: string) =>
  `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
async function componentURL(name: string): Promise<string> {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const imports: Record<string, string> = {
    '$app/env/public': moduleURL(
      "export const PUBLIC_API_BASE_URL='https://synthetic.invalid';",
    ),
    './app-context.ts': moduleURL(
      'export const getAppSession=()=>({token:async()=>null,onDenied:()=>{}});',
    ),
  };
  for (const match of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    imports[`./${match[1]}.svelte`] = [
      'SubscriptionForm',
      'AnalysisAttach',
    ].includes(match[1])
      ? moduleURL('export default function InactiveChild(){}')
      : await componentURL(match[1]);
  return moduleURL(
    js.code.replace(
      /(^import[\s\S]*?from\s+|^import\s+)(['"])([^'"]+)\2/gm,
      (_m, p, q, path) =>
        `${p}${q}${imports[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${q}`,
    ),
  );
}
const event = {
  service: 'Example',
  event_type: 'payment',
  amount: '1200',
  currency: 'JPY',
  interval_unit: 'month',
  interval_count: 1,
};
test('bulk exclusion confirmation compiles without stale callback captures', async () => {
  await componentURL('CandidateBulkDismiss');
});
async function review(related_count: number, mail_count: number) {
  return render((await import(await componentURL('CandidateReview'))).default, {
    props: {
      candidate: {
        result_id: 'r',
        event_index: 0,
        account_label: '個人メール',
        received_at: '2026-11-01T00:00:00Z',
        event,
        related_count,
        mail_count,
      },
      drafts: {},
      onDone() {},
      onBusyChange() {},
    },
  }).body;
}
test('related comparison is optional and initially collapsed with plain selection copy', async () => {
  const body = await review(51, 51);
  assert.match(body, /関連メールを比較・選択/);
  assert.doesNotMatch(body, /<details[^>]*\bopen\b/);
  assert.doesNotMatch(
    body,
    /この抽出情報を処理する|候補だけを一度に処理|抽出された金額/,
  );
  assert.match(body, /選択中 1 件/);
});
test('single mail hides meaningless related mail counts even when it has several extracted events', async () => {
  assert.doesNotMatch(await review(1, 1), /関連メール|関連候補/);
  assert.doesNotMatch(await review(2, 1), /関連メール\s*1\s*件/);
});
test('selection toolbar offers accessible page selection and clear, and optional group selection', async () => {
  const Toolbar = (await import(await componentURL('SelectionToolbar')))
    .default;
  const props = {
    selectedCount: 2,
    pageCount: 50,
    allPageSelected: false,
    partialPageSelected: true,
    onTogglePage() {},
    onClear() {},
  };
  const body = render(Toolbar, { props }).body;
  assert.match(body, /このページをすべて選択/);
  assert.match(body, /選択解除/);
  assert.match(body, /選択中 2 件/);
  assert.doesNotMatch(body, />すべて選択</);
  assert.match(
    render(Toolbar, { props: { ...props, totalCount: 75, onSelectAll() {} } })
      .body,
    />すべて選択</,
  );
});
