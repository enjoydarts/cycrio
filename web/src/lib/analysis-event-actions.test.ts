import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import type { AnalysisResult } from './analysis-api.ts';
import {
  processingResult,
  processingIDs as ids,
} from './mail-processing-fixtures.ts';
const event = {
  service: 'A',
  plan: null,
  event_type: 'renewal' as const,
  amount: null,
  currency: null,
  billed_at: null,
  period_start: null,
  period_end: null,
  next_billing_at: null,
  interval_unit: null,
  interval_count: null,
};
const result = (patch: Partial<AnalysisResult> = {}): AnalysisResult => ({
  ...processingResult({ state: 'succeeded', error_code: '', events: [event] }),
  account_id: ids.account,
  account_label: '受信',
  registrations: [],
  automation_state: 'pending',
  automation: [],
  billing_automation: [],
  ...patch,
});
async function componentURL(name: string): Promise<string> {
  if (['SubscriptionForm', 'AnalysisAttach', 'AnalysisStart'].includes(name))
    return 'data:text/javascript,export default function UnusedEditor() {}';
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const nested: Record<string, string> = {};
  for (const m of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    nested[`./${m[1]}.svelte`] = await componentURL(m[1]);
  const code = js.code.replace(
    /(from\s+|import\s+)(['"])([^'"]+)\2/g,
    (_m, prefix, q, path) =>
      `${prefix}${q}${nested[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${q}`,
  );
  return `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
}
async function html(value: AnalysisResult) {
  const Component = (await import(await componentURL('AnalysisDetail')))
    .default;
  return render(Component, {
    props: {
      result: value,
      active: true,
      accounts: [],
      requests: new Map(),
      drafts: new Map(),
      onRegistered: () => {},
      onAccepted: () => {},
      onBackToResults: () => {},
    },
  }).body;
}
test('every successful unregistered candidate offers explicit owner approval', async () => {
  const output = await html(result());
  assert.doesNotMatch(output, /自動登録待ち/);
  assert.match(output, /新規サブスクに登録/);
  assert.match(output, /既存のサブスクに紐付ける/);
});
test('both normal and ambiguous unregistered events offer owner approval', async () => {
  const output = await html(
    result({
      events: [event, { ...event, service: 'B' }],
      automation_state: 'needs_review',
      automation: [
        {
          event_index: 1,
          state: 'needs_review',
          reason: 'ambiguous_identity',
          subscription_id: null,
        },
      ],
    }),
  );
  assert.equal((output.match(/>新規サブスクに登録</g) ?? []).length, 2);
  assert.equal((output.match(/>既存のサブスクに紐付ける</g) ?? []).length, 2);
  assert.doesNotMatch(output, /自動登録待ち/);
});
for (const outcome of [
  undefined,
  {
    event_index: 0,
    state: 'not_applicable',
    reason: 'non_financial_event',
    entry_id: null,
  },
  {
    event_index: 0,
    state: 'not_applicable',
    reason: 'owner_detached',
    entry_id: null,
  },
] as const)
  test(`normal registered ${outcome?.reason ?? 'billing pending'} links to history without opening a new source editor`, async () => {
    const output = await html(
      result({
        registrations: [{ event_index: 0, subscription_id: ids.profile }],
        automation_state: 'registered',
        automation: [
          {
            event_index: 0,
            state: 'registered',
            reason: 'created',
            subscription_id: ids.profile,
          },
        ],
        billing_automation: outcome ? [outcome] : [],
      }),
    );
    assert.match(
      output,
      new RegExp(`/subscriptions/${ids.profile}\\?tab=history`),
    );
    assert.doesNotMatch(
      output,
      /source_result_id=|新規サブスクに登録|既存のサブスクに紐付ける/,
    );
  });
for (const state of ['recorded', 'needs_review', 'failed'] as const)
  test(`financial ${state} retains the explicit current-source history route`, async () => {
    const output = await html(
      result({
        registrations: [{ event_index: 0, subscription_id: ids.profile }],
        automation_state: 'registered',
        billing_automation: [
          {
            event_index: 0,
            state,
            reason: state === 'recorded' ? 'created' : 'missing_facts',
            entry_id: state === 'recorded' ? ids.run : null,
          },
        ],
      }),
    );
    assert.match(
      output,
      new RegExp(`source_result_id=${ids.result}&amp;source_event_index=0`),
    );
    assert.doesNotMatch(output, /新規サブスクに登録|既存のサブスクに紐付ける/);
  });
test('zero-event detail offers neither manual contract actions nor a financial source link', async () => {
  const output = await html(
    result({ events: [], automation_state: 'no_events' }),
  );
  assert.match(output, /登録対象の情報はありません/);
  assert.doesNotMatch(
    output,
    /新規サブスクに登録|既存のサブスクに紐付ける|source_result_id=/,
  );
});
