import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
test('daily routes compose subscription and analysis screens through shared authenticated context', async () => {
  for (const [path, name] of [
    ['subscriptions', 'Subscriptions'],
    ['subscriptions/[id=uuid]', 'SubscriptionDetail'],
    ['analysis', 'AnalysisInbox'],
  ]) {
    const source = await readFile(
      new URL(`../routes/${path}/+page.svelte`, import.meta.url),
      'utf8',
    ).catch(() => '');
    assert.match(source, new RegExp(name));
    assert.doesNotMatch(source, /createAuthController/);
  }
});
test('daily mutation dialogs propagate saving state and original AI fields are separately labeled', async () => {
  for (const name of ['Subscriptions', 'SubscriptionDetail', 'AnalysisInbox']) {
    const source = await readFile(
      new URL(`./${name}.svelte`, import.meta.url),
      'utf8',
    ).catch(() => '');
    assert.match(source, /busy=|\{busy\}/);
    assert.match(source, /onBusyChange|onFrozen/);
  }
  const fields = await readFile(
    new URL('./EventFields.svelte', import.meta.url),
    'utf8',
  ).catch(() => '');
  assert.match(fields, /次回請求日/);
  assert.match(fields, /請求日/);
  assert.match(fields, /不明/);
});
