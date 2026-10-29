import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
test('the application preserves the approved Cycrio green, teal and mint identity', async () => {
  const source = await readFile(
    new URL('./brand.css', import.meta.url),
    'utf8',
  );
  const light = source.match(/:root\s*\{([^}]+)\}/)![1];
  for (const [token, color] of Object.entries({
    'brand-primary': '#22c55e',
    'brand-secondary': '#0f766e',
    'brand-accent': '#a7f3d0',
    'brand-background': '#ecfdf5',
  })) {
    assert.match(light, new RegExp(`--${token}:\\s*${color};`, 'i'), token);
  }
});
test('both themes keep normal text, actions and errors above AA contrast against application surfaces', async () => {
  const source = await readFile(
    new URL('./brand.css', import.meta.url),
    'utf8',
  );
  function luminance(hex: string) {
    const components = hex
      .match(/[a-f0-9]{2}/gi)!
      .map((value) => parseInt(value, 16) / 255)
      .map((value) =>
        value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4,
      );
    return (
      components[0] * 0.2126 + components[1] * 0.7152 + components[2] * 0.0722
    );
  }
  for (const block of source.matchAll(/:root[^{}]*\{([^}]+)\}/g)) {
    const colors = Object.fromEntries(
      [...block[1].matchAll(/--([a-z-]+):\s*(#[a-f0-9]{6})/gi)].map((match) => [
        match[1],
        match[2],
      ]),
    );
    for (const [foreground, background] of [
      ['color-text', 'color-surface'],
      ['color-muted', 'color-surface'],
      ['color-muted', 'brand-background'],
      ['color-action', 'color-surface'],
      ['color-action', 'color-surface-soft'],
      ['color-on-action', 'color-action'],
      ['color-danger', 'color-surface'],
    ]) {
      assert.ok(
        colors[foreground] && colors[background],
        `${foreground}/${background} tokens`,
      );
      const values = [
        luminance(colors[foreground]),
        luminance(colors[background]),
      ].sort((a, b) => a - b);
      assert.ok(
        (values[1] + 0.05) / (values[0] + 0.05) >= 4.5,
        `${foreground}/${background}`,
      );
    }
  }
});
test('mail provider initials use the contrast-checked action foreground and background together', async () => {
  const source = await readFile(
    new URL('./AppShell.svelte', import.meta.url),
    'utf8',
  );
  const icon = source.match(/\.mail-logo\.fastmail\s*\{([^}]+)\}/)![1];
  assert.match(icon, /color:\s*var\(--color-on-action\)/);
  assert.match(icon, /background:\s*var\(--color-action\)/);
});
test('legacy settings typography and action colors use readable shared tokens', async () => {
  for (const filename of [
    'catalog.css',
    'ai-runtime.css',
    'AdminCredentials.svelte',
    'MailAccounts.svelte',
    'MailSync.svelte',
    'MailProcessingPolicy.svelte',
  ]) {
    const source = await readFile(
      new URL(`./${filename}`, import.meta.url),
      'utf8',
    );
    assert.doesNotMatch(
      source,
      /font-size:\s*0\.(?:[0-6]\d*|7[0-4]?)rem/,
      filename,
    );
    assert.doesNotMatch(source, /color:\s*var\(--color-surface\)/, filename);
  }
});
