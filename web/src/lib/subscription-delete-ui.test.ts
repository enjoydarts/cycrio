import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
test('detail offers a central delete confirmation and protects drafts and navigation', async () => {
  const source = await readFile(
    new URL('./SubscriptionDetail.svelte', import.meta.url),
    'utf8',
  );
  assert.match(source, /title="Cycrioから削除"/);
  assert.match(source, /サービス自体の解約は行いません/);
  assert.match(source, /過去の支払い・返金履歴は残ります/);
  assert.match(source, /busy=\{deleteView.locked\}/);
  assert.match(source, /同じ要求で削除を確認/);
  assert.match(source, /onDeleted/);
  assert.match(source, /deleteView.locked \|\|/);
  assert.match(source, /deleted = true;\s*await tick\(\);\s*deleting = false;/);
  assert.deepEqual(compile(source, { generate: 'server' }).warnings, []);
});
