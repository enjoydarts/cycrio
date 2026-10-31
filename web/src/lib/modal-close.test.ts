import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { parse } from 'svelte/compiler';

async function eventHandler(
  tag: string,
  event: string,
  busy: boolean,
  onclose: () => void,
  dialog: { open: boolean; close: () => void },
  open = true,
) {
  const source = await readFile(
    new URL('./Modal.svelte', import.meta.url),
    'utf8',
  );
  const ast = parse(source, { modern: true }) as any;
  let expression: any;
  function visit(node: any) {
    if (!node || typeof node !== 'object') return;
    if (Array.isArray(node)) {
      node.forEach(visit);
      return;
    }
    if (node.name === tag) {
      const a = node.attributes?.find((a: any) => a.name === event);
      if (a) {
        expression = (Array.isArray(a.value) ? a.value[0] : a.value).expression;
        return;
      }
    }
    for (const v of Object.values(node)) visit(v);
  }
  visit(ast.fragment);
  assert.ok(expression, event);
  return new Function(
    'busy',
    'canClose',
    'onclose',
    'dialog',
    'open',
    `return (${source.slice(expression.start, expression.end)})`,
  )(busy, () => true, onclose, dialog, open);
}
test('close button notifies controlled state synchronously before native dialog cleanup', async () => {
  let closed = 0,
    native = 0;
  const click = await eventHandler('Button', 'onclick', false, () => closed++, {
    open: true,
    close: () => native++,
  });
  click();
  assert.equal(closed, 1);
  assert.equal(native, 0);
});
test('Escape uses controlled close and remains blocked while busy', async () => {
  for (const busy of [false, true]) {
    let closed = 0,
      prevented = 0;
    const cancel = await eventHandler(
      'dialog',
      'oncancel',
      busy,
      () => closed++,
      { open: true, close: () => {} },
    );
    cancel({ preventDefault: () => prevented++ });
    assert.equal(prevented, 1);
    assert.equal(closed, busy ? 0 : 1);
  }
});
test('a stale native close notification cannot close a reopened dialog', async () => {
  for (const open of [false, true]) {
    let closed = 0;
    const close = await eventHandler(
      'dialog',
      'onclose',
      false,
      () => closed++,
      { open, close: () => {} },
    );
    close();
    assert.equal(closed, open ? 0 : 1);
  }
});
test('native cleanup does not notify the parent twice after a controlled close', async () => {
  let closed = 0;
  const close = await eventHandler(
    'dialog',
    'onclose',
    false,
    () => closed++,
    { open: false, close: () => {} },
    false,
  );
  close();
  assert.equal(closed, 0);
});
