import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile } from 'svelte/compiler';
test('intake offers three explicit steps and keeps the consent controller mounted when returning to folders', async () => {
  const source = await readFile(
    new URL('./IntakeWizard.svelte', import.meta.url),
    'utf8',
  );
  assert.deepEqual(compile(source, { generate: 'server' }).warnings, []);
  for (const text of [
    'メールを選ぶ',
    'フォルダを選ぶ',
    '取り込みを始める',
    'beforeNavigate',
    'hasUnsaved',
    'adoptStoredFolders',
    'connectOnly',
    'automationVisited',
    'active={view.step === 3}',
  ])
    assert.ok(source.includes(text), text);
  assert.match(source, /取り込みを受け付けました/);
  assert.match(source, /if \(locked\) return;/);
});
