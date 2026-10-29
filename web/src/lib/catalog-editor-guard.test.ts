import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { parse } from 'svelte/compiler';
test('model selection and closing respect dirty edits and a saving or unknown mutation', async () => {
  const source = await readFile(
    new URL('./ModelCatalog.svelte', import.meta.url),
    'utf8',
  );
  const ast = parse(source, { modern: true }) as any;
  const fn = ast.instance.content.body.find(
    (node: any) =>
      node.type === 'FunctionDeclaration' && node.id.name === 'openModel',
  );
  const body = source
    .slice(fn.start, fn.end)
    .replace(/model: Model(?: \| null)?/, 'model');
  for (const state of [
    { dirty: true, busy: false, uncertain: false },
    { dirty: false, busy: true, uncertain: false },
    { dirty: false, busy: false, uncertain: true },
  ]) {
    const selected: unknown[] = [];
    const change = new Function(
      'controller',
      'tick',
      'document',
      'window',
      'busy',
      'uncertainWrite',
      'editorDirty',
      `return (${body});`,
    )(
      {
        select: async (value: unknown) => selected.push(value),
        state: { selected: null },
      },
      async () => {},
      { getElementById: () => null },
      { confirm: () => false, matchMedia: () => ({ matches: true }) },
      state.busy,
      state.uncertain,
      state.dirty,
    );
    await change({ id: 'different-model' });
    assert.deepEqual(selected, [], JSON.stringify(state));
  }
});
test('explicit discard permits model close and preserves the selected target identity', async () => {
  const source = await readFile(
    new URL('./ModelCatalog.svelte', import.meta.url),
    'utf8',
  );
  const ast = parse(source, { modern: true }) as any;
  const fn = ast.instance.content.body.find(
    (node: any) =>
      node.type === 'FunctionDeclaration' && node.id.name === 'openModel',
  );
  const body = source
    .slice(fn.start, fn.end)
    .replace(/model: Model(?: \| null)?/, 'model');
  const selected: unknown[] = [];
  const change = new Function(
    'controller',
    'tick',
    'document',
    'window',
    'busy',
    'uncertainWrite',
    'editorDirty',
    `return (${body});`,
  )(
    {
      select: async (value: unknown) => selected.push(value),
      state: { selected: null },
    },
    async () => {},
    { getElementById: () => null },
    { confirm: () => true, matchMedia: () => ({ matches: true }) },
    false,
    false,
    true,
  );
  const target = { id: 'different-model' };
  await change(target);
  assert.deepEqual(selected, [target]);
});
