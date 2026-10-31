import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
import { render } from 'svelte/server';
import {
  processingResult,
  processingIDs as ids,
  processingPreview,
  processingRun,
} from './mail-processing-fixtures.ts';
import { createMailProcessingController } from './mail-processing-controller.ts';

async function resultHTML(patch: Record<string, unknown>) {
  const source = await readFile(
    new URL('./AnalysisResultStatus.svelte', import.meta.url),
    'utf8',
  ).catch(() => null);
  assert.ok(source, 'result status component exists');
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const code = js.code.replace(
    /(from\s+|import\s+)(['"])([^'"]+)\2/g,
    (_m, prefix, q, path) =>
      `${prefix}${q}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${q}`,
  );
  const component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  return render(component, {
    props: {
      result: {
        ...processingResult({ state: 'succeeded', events: [] }),
        account_id: ids.account,
        account_label: '受信',
        registrations: [],
        automation_state: 'no_events',
        automation: [],
        billing_automation: [],
        ...patch,
      },
    },
  }).body;
}
test('no-event successful extraction has a complete message without registration ratio or action', async () => {
  const html = await resultHTML({});
  assert.match(html, /登録対象の情報はありません/);
  assert.doesNotMatch(html, /0\s*\/\s*0|未確認|確認する|href=/);
});
test('registered automatic facts link to the current contract and history', async () => {
  const html = await resultHTML({
    events: [{ service: 'A' }],
    automation_state: 'registered',
    registrations: [{ event_index: 0, subscription_id: ids.profile }],
    automation: [
      {
        event_index: 0,
        state: 'registered',
        reason: 'created',
        subscription_id: ids.profile,
      },
    ],
    billing_automation: [
      {
        event_index: 0,
        state: 'recorded',
        reason: 'created',
        entry_id: ids.run,
      },
    ],
  });
  assert.match(html, /自動登録/);
  assert.match(html, new RegExp(`/subscriptions/${ids.profile}`));
  assert.match(html, /tab=history/);
});
test('new manual analysis sends whole-email scope; retained legacy pending is unchanged', async () => {
  let body: unknown;
  const api = {
    preview: async () => ({ ...processingPreview(), full_email_allowed: true }),
    runs: async () => [],
    results: async () => [],
    start: async (_id: unknown, v: unknown) => {
      body = v;
      return processingRun();
    },
  };
  const c = createMailProcessingController(
    api as never,
    async () => 't',
    () => {},
    () => {},
    () => ids.request,
  );
  c.setAccount(ids.account);
  await c.refresh();
  c.edit({ max_messages: '1', confirmed: true });
  await c.start();
  assert.equal((body as { full_email: boolean }).full_email, true);
  c.dispose();
});
test('new manual whole-email requests wait for current explicit full permission', async () => {
  let calls = 0;
  const api = {
    preview: async () => ({
      ...processingPreview(),
      full_email_allowed: false,
    }),
    runs: async () => [],
    results: async () => [],
    start: async () => {
      calls++;
      return processingRun();
    },
  };
  const c = createMailProcessingController(
    api as never,
    async () => 't',
    () => {},
    () => {},
  );
  c.setAccount(ids.account);
  await c.refresh();
  c.edit({ max_messages: '1', confirmed: true });
  await c.start();
  assert.equal(calls, 0);
  c.dispose();
});
test('automatic setup return keeps selected account without reopening the legacy manual modal', async () => {
  const module = await import('./setup-readiness.ts');
  assert.equal(typeof module.automationSetupLink, 'function');
  const target = new URL(
    module.automationSetupLink(
      '/settings/ai?stage=permission',
      '/analysis?tab=attention&start=true&setup_max=7',
      ids.account,
    ),
    'https://example.test',
  );
  const back = new URL(
    target.searchParams.get('return_to')!,
    'https://example.test',
  );
  assert.equal(back.pathname, '/analysis');
  assert.equal(back.searchParams.get('setup_account'), ids.account);
  assert.equal(back.searchParams.has('start'), false);
  assert.equal(back.searchParams.has('setup_max'), false);
  assert.equal(target.searchParams.get('stage'), 'permission');
});
