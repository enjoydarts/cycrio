import assert from 'node:assert/strict';
import test from 'node:test';
import { createThemeController } from './theme.ts';

function fixture(stored: string | null = null, dark = false) {
  let listener: (() => void) | undefined;
  const values = new Map<string, string>(
    stored === null ? [] : [['cycrio-theme', stored]],
  );
  const root = { dataset: {} as Record<string, string> };
  const media = {
    matches: dark,
    addEventListener: (_: string, next: () => void) => {
      listener = next;
    },
    removeEventListener: () => {
      listener = undefined;
    },
  };
  const controller = createThemeController({
    root,
    storage: {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => {
        values.set(key, value);
      },
    },
    media,
  });
  return {
    controller,
    root,
    values,
    change(dark: boolean) {
      media.matches = dark;
      listener?.();
    },
  };
}
test('theme defaults to system and follows OS changes only for system preference', () => {
  const f = fixture();
  assert.equal(f.controller.preference(), 'system');
  assert.equal(f.root.dataset.theme, 'light');
  f.change(true);
  assert.equal(f.root.dataset.theme, 'dark');
  f.controller.set('light');
  f.change(true);
  assert.equal(f.root.dataset.theme, 'light');
  assert.deepEqual([...f.values], [['cycrio-theme', 'light']]);
});
test('theme restores validated preference, supports switching back to system and disposes OS listener', () => {
  const f = fixture('dark');
  assert.equal(f.root.dataset.theme, 'dark');
  f.controller.set('system');
  assert.equal(f.root.dataset.theme, 'light');
  f.controller.dispose();
  f.change(true);
  assert.equal(f.root.dataset.theme, 'light');
  assert.equal(fixture('invalid', true).root.dataset.theme, 'dark');
});
test('unavailable local storage does not prevent appearance changes', () => {
  const root = { dataset: {} as Record<string, string> };
  const controller = createThemeController({
    root,
    storage: {
      getItem() {
        throw Error();
      },
      setItem() {
        throw Error();
      },
    },
    media: { matches: false, addEventListener() {}, removeEventListener() {} },
  });
  controller.set('dark');
  assert.equal(root.dataset.theme, 'dark');
});
