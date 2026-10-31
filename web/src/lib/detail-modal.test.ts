import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { compile } from 'svelte/compiler';

test('subscription details use a centered, viewport-contained modal with scrollable content', async () => {
  const source = await readFile(
    new URL('./SubscriptionDrawer.svelte', import.meta.url),
    'utf8',
  );
  const { css, warnings } = compile(source, { generate: 'client' });
  assert.deepEqual(warnings, []);
  // Inspect the emitted styles: a right-anchored, full-height drawer would
  // violate these layout constraints even though its dialog semantics work.
  const block = css!.code.match(/dialog[^{}:]*\{([^}]+)\}/)![1];
  const styles = Object.fromEntries(
    block
      .split(';')
      .filter((declaration) => declaration.includes(':'))
      .map((declaration) => {
        const separator = declaration.indexOf(':');
        return [
          declaration.slice(0, separator).trim(),
          declaration.slice(separator + 1).trim(),
        ];
      }),
  );
  assert.equal(styles.margin, 'auto', 'center horizontally and vertically');
  assert.equal(styles.width, 'calc(100% - 32px)', '16px viewport side inset');
  assert.equal(styles['max-width'], '900px', 'readable desktop detail width');
  assert.equal(styles['max-height'], 'calc(100svh - 32px)');
  assert.equal(styles.height, undefined, 'short details keep a natural height');
  assert.equal(styles.overflow, 'auto', 'long details scroll inside the modal');
  assert.equal(styles['overscroll-behavior'], 'contain');
});
