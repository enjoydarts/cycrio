import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFile, stat } from 'node:fs/promises';
import { test } from 'node:test';

test('generates a broad local colored catalog and leaves unchanged output untouched', async () => {
  execFileSync(process.execPath, ['scripts/generate-service-logos.mjs']);
  const indexPath = 'src/lib/generated/service-logo-index.ts';
  const source = await readFile(indexPath, 'utf8');
  const index = JSON.parse(
    source
      .slice(source.indexOf(' = ') + 3)
      .trim()
      .replace(/;$/, ''),
  );
  assert.ok(Object.keys(index).length > 3000);
  assert.equal(index.spotify.slug, 'spotify');
  assert.equal(index.netflix.src, '/service-logos/netflix.svg');
  assert.equal(index.googleone.slug, 'googleone');
  assert.equal(index.gmail.src, '/brand/services/gmail.ico');
  assert.equal(index.snapchat.background, 'dark');
  assert.equal(index.spotify.background, undefined);
  for (const ambiguous of ['c', 'backstage', 'graphite', 'spring']) {
    assert.equal(index[ambiguous], undefined);
  }
  assert.equal(index.csharp, undefined);
  assert.equal(index.cplusplus.slug, 'cplusplus');
  for (const slug of [
    'amazon',
    'adobe',
    'slack',
    'nintendo',
    'xbox',
    'hulu',
    'unext',
    'danimestore',
  ]) {
    assert.equal(index[slug]?.slug, slug);
  }
  for (const logo of Object.values(index)) {
    assert.match(logo.src, /^\/(service-logos|brand\/services)\/[a-z0-9_.-]+$/);
    await stat(`static${logo.src}`);
  }
  const svg = await readFile(`static${index.spotify.src}`, 'utf8');
  assert.match(svg, /fill="#[0-9A-Fa-f]{6}"/);
  assert.doesNotMatch(source, /<svg|<path/);
  const before = await stat(indexPath);
  execFileSync(process.execPath, ['scripts/generate-service-logos.mjs']);
  assert.equal((await stat(indexPath)).mtimeMs, before.mtimeMs);
});
