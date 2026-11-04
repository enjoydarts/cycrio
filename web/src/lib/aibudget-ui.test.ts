import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { render } from 'svelte/server';
import {
  processingBlocked,
  processingReasons,
} from './mail-processing-text.ts';
import {
  automationAISetupPath,
  automationAIWaiting,
} from './mail-automation-progress.ts';
import { processingAction } from './setup-readiness.ts';
import { gettingStartedGuide } from './getting-started.ts';
import { gettingStartedFixture } from './getting-started-fixtures.ts';
test('budget panel explains current JST month, scopes, zero and overrun', async () => {
  const { default: AIBudgetSettings } =
    await import('./AIBudgetSettings.svelte');
  const html = render(AIBudgetSettings, {
    props: {
      owner: '11111111-1111-4111-8111-111111111111',
      token: async () => 'token',
      onDenied: () => {},
      api: {
        read: async () => {
          throw Error();
        },
        save: async () => {
          throw Error();
        },
      },
    },
  }).body;
  for (const text of [
    'Asia/Tokyo',
    '0 USD',
    '管理者',
    '実行中',
    '現在月',
    'USD 以外',
  ])
    assert.ok(html.replace(/\s+/g, ' ').includes(text), text);
});
test('safe budget reasons guide only administrators to usage', () => {
  for (const reason of [
    'budget_exceeded',
    'budget_unknown',
    'budget_pending',
  ] as const) {
    assert.ok(processingBlocked[reason]);
    assert.ok(processingReasons[reason]);
    assert.equal(processingAction(reason, false)?.href, null);
  }
  assert.equal(
    processingAction('budget_exceeded', true)?.href,
    '/settings/usage',
  );
  assert.equal(automationAISetupPath(['budget_unknown']), '/settings/usage');
  assert.equal(
    automationAIWaiting({
      state: 'enabled',
      ready: false,
      blocked_reasons: ['budget_exceeded'],
    }),
    true,
  );
});
test('getting started identifies budget waiting and routes administrators to costs', () => {
  const page = gettingStartedFixture();
  page.automation!.state = 'enabled';
  page.automation!.ready = false;
  page.automation!.blocked_reasons = ['budget_exceeded'];
  assert.match(gettingStartedGuide(page, false).title, /月額上限/);
  assert.equal(gettingStartedGuide(page, false).href, null);
  assert.equal(gettingStartedGuide(page, true).href, '/settings/usage');
});
