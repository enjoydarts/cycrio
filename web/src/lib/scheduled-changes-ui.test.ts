import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import { compile } from 'svelte/compiler';
import { readFile } from 'node:fs/promises';
import { changePreview } from './subscription-changes-fixtures.ts';
import { id } from './subscriptions-fixtures.ts';
import type { ScheduledChange } from './subscription-scheduled-changes-api.ts';

async function renderConnected(name: string, props: Record<string, unknown>) {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const moduleURL = (code: string) =>
    `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
  const imports: Record<string, string> = {
    '$app/env/public': moduleURL(
      "export const PUBLIC_API_BASE_URL='https://synthetic.invalid';",
    ),
    './app-context.ts': moduleURL(
      'export const getAppSession=()=>({token:async()=>null,onDenied:()=>{}});',
    ),
  };
  const code = js.code.replace(
    /(^import[\s\S]*?from\s+|^import\s+)(['"])([^'"]+)\2/gm,
    (_m, p, q, path) =>
      `${p}${q}${imports[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${q}`,
  );
  return render((await import(moduleURL(code))).default, { props }).body;
}

test('connected reservation and review render their own Japanese-time behavior and explicit consent actions', async () => {
  const list = await renderConnected('ScheduledChanges', { id });
  assert.match(list, /契約変更の予約/);
  assert.match(list, /現在の契約内容を保持/);
  assert.match(list, /要再確認で停止/);
  assert.match(list, /反映前の確認時/);
  const review = await renderConnected('ScheduledChangeReview', {
    subscription: changePreview().subscription,
    source: { source_result_id: id, source_event_index: 0 },
    onSaved() {},
    onBack() {},
    onBackToResults() {},
  });
  assert.match(review, /効力日（日本時間）以降/);
  assert.match(review, /予約後に契約を編集/);
  assert.match(review, /反映前の確認時/);
  assert.match(review, /選択した変更を予約/);
  assert.match(review, /disabled/);
  assert.doesNotMatch(review, / checked/);
});

test('scheduled summary shows frozen before/proposed values, JST date, evidence and safe needs-review instructions', async () => {
  const Summary = (await import('./ScheduledChangeSummary.svelte')).default;
  const schedule: ScheduledChange = {
    id,
    request_id: id,
    subscription_id: id,
    version: 2,
    base_version: 1,
    effective_on: '2027-01-01',
    state: 'needs_review',
    selected_fields: ['amount'],
    created_at: '2026-11-03T00:00:00Z',
    applied_at: null,
    reason: 'subscription_changed',
    preview: { ...changePreview(), scheduled_for: '2027-01-01' },
  };
  const html = render(Summary, { props: { schedule, onCancel() {} } }).body;
  assert.match(html, /要再確認/);
  assert.match(html, /2027-01-01（日本時間）/);
  assert.match(html, /契約が更新/);
  assert.match(html, /取り消してから候補/);
  assert.match(html, /data-label="予約時の値"[^>]*>0/);
  assert.match(html, /data-label="予約した値"[^>]*>9/);
  assert.match(html, /元の抽出情報/);
  assert.match(html, /予約を取り消す/);
  const applied = render(Summary, {
    props: { schedule: { ...schedule, state: 'applied' }, onCancel() {} },
  }).body;
  assert.doesNotMatch(applied, /予約を取り消す/);
  const locked = render(Summary, {
    props: { schedule, locked: true, onCancel() {} },
  }).body;
  assert.match(locked, /disabled/);
});
