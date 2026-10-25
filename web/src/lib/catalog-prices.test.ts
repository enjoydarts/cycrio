import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { compile, parse } from 'svelte/compiler';
import { render } from 'svelte/server';

function currencyElement(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== 'object') return;
  if (Array.isArray(value)) {
    for (const child of value) {
      const result = currencyElement(child);
      if (result) return result;
    }
    return;
  }
  const node = value as Record<string, unknown>;
  if (
    node.name === 'input' &&
    Array.isArray(node.attributes) &&
    node.attributes.some(
      (attribute) =>
        attribute.name === 'id' &&
        Array.isArray(attribute.value) &&
        attribute.value.some(
          (part: { data?: string }) => part.data === 'price-currency',
        ),
    )
  )
    return node;
  for (const child of Object.values(node)) {
    const result = currencyElement(child);
    if (result) return result;
  }
}

test('the actual Svelte price currency pattern renders a valid three-letter currency expression', async () => {
  const source = await readFile(
    new URL('./CatalogPrices.svelte', import.meta.url),
    'utf8',
  );
  const input = currencyElement(parse(source, { modern: true }));
  assert.ok(input && Array.isArray(input.attributes));
  const attribute = input.attributes.find(
    (value: { name?: string }) => value.name === 'pattern',
  );
  assert.ok(attribute);
  // Compile the actual attribute so Svelte interpolation and HTML output are exercised.
  const { js } = compile(
    `<input ${source.slice(attribute.start, attribute.end)} value="USD" />`,
    { generate: 'server' },
  );
  const code = js.code.replace(
    /(from\s+|import\s*)(['"])(svelte[^'"]+)\2/g,
    (_match, prefix, quote, path) =>
      `${prefix}${quote}${import.meta.resolve(path)}${quote}`,
  );
  const { default: Component } = await import(
    `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
  );
  const html = render(Component).body;
  const pattern = /pattern="([^"]*)"/.exec(html)?.[1];
  assert.equal(pattern, '[A-Z]{3}');
  assert.equal(new RegExp(`^(?:${pattern})$`, 'v').test('USD'), true);
  assert.equal(new RegExp(`^(?:${pattern})$`, 'v').test('US'), false);
});
