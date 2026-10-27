import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
test('daily routes compose subscription and analysis screens through shared authenticated context', async () => {
  for (const [path, name] of [
    ['subscriptions', 'Subscriptions'],
    ['subscriptions/[id]', 'SubscriptionDetail'],
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
    assert.match(source, /busy=/);
    assert.match(source, /onBusyChange/);
  }
  const fields = await readFile(
    new URL('./EventFields.svelte', import.meta.url),
    'utf8',
  ).catch(() => '');
  assert.match(fields, /次回請求日/);
  assert.match(fields, /請求日/);
  assert.match(fields, /不明/);
});
test('subscription list links to the server counted attention messages across all AI states', async () => {
  const source = await readFile(
    new URL('./Subscriptions.svelte', import.meta.url),
    'utf8',
  );
  assert.match(source, /review:\s*'attention'/);
  assert.match(source, /state:\s*'all'/);
  assert.match(source, /limit:\s*1/);
  assert.match(source, /pending\.page\.total/);
  assert.match(source, /メール単位/);
  assert.match(source, /href="\/analysis"/);
});
