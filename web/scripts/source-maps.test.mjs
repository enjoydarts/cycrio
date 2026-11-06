import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  prepareSourceMaps,
  uploadSourceMaps,
  verifySourceMaps,
} from './source-maps.mjs';

const release = 'a'.repeat(40);
async function fixture() {
  const root = await mkdtemp(join(tmpdir(), 'cycrio-maps-'));
  const buildDir = join(root, 'build');
  const privateDir = join(root, 'private');
  await mkdir(join(buildDir, '_app'), { recursive: true });
  await writeFile(join(buildDir, '_app/app.js'), 'console.log("source");\n');
  await writeFile(
    join(buildDir, '_app/app.js.map'),
    JSON.stringify({
      version: 3,
      file: 'app.js',
      sources: ['../src/fixture.ts'],
      sourcesContent: ['console.log("source");\n'],
      names: [],
      mappings: 'AAAA',
    }),
  );
  return { buildDir, privateDir, release };
}

test('official offline injection retains usable private maps and identical public JS', async () => {
  const options = await fixture();
  await prepareSourceMaps(options);
  const script = await readFile(join(options.buildDir, '_app/app.js'), 'utf8');
  assert.match(script, /_posthogChunkIds/);
  assert.match(script, /chunkId=[0-9a-f-]{36}/);
  assert.doesNotMatch(script, /sourceMappingURL/);
  assert.deepEqual(await readdir(join(options.buildDir, '_app')), ['app.js']);
  assert.equal(
    script,
    await readFile(join(options.privateDir, '_app/app.js'), 'utf8'),
  );
  const map = JSON.parse(
    await readFile(join(options.privateDir, '_app/app.js.map'), 'utf8'),
  );
  assert.ok(map.mappings.length > 0 && map.sourcesContent[0].length > 0);
  await verifySourceMaps(options);
});

test('empty mappings and missing source content fail before publishing', async () => {
  for (const field of ['mappings', 'sourcesContent']) {
    const options = await fixture();
    const path = join(options.buildDir, '_app/app.js.map');
    const map = JSON.parse(await readFile(path, 'utf8'));
    map[field] = field === 'mappings' ? '' : [];
    await writeFile(path, JSON.stringify(map));
    await assert.rejects(prepareSourceMaps(options), /usable source map/);
  }
});

test('upload fails closed without PAT and never re-injects or changes deployed bytes', async () => {
  const options = await fixture();
  await prepareSourceMaps(options);
  await assert.rejects(
    uploadSourceMaps({ ...options, env: {} }),
    /POSTHOG_CLI_API_KEY/,
  );
  let uploaded = false;
  await uploadSourceMaps({
    ...options,
    env: { POSTHOG_CLI_API_KEY: 'private-test-token' },
    run: async (args, env) => {
      uploaded = true;
      assert.deepEqual(args.slice(0, 2), ['sourcemap', 'upload']);
      assert.ok(args.includes(options.privateDir));
      assert.ok(args.includes(release));
      assert.equal(args[args.indexOf('--release-mode') + 1], 'event');
      assert.ok(!args.includes('--skip-on-conflict'));
      assert.equal(env.POSTHOG_CLI_PROJECT_ID, '714781');
      assert.equal(env.POSTHOG_CLI_HOST, 'https://us.posthog.com');
      assert.equal(env.POSTHOG_CLI_API_KEY, 'private-test-token');
    },
  });
  assert.ok(uploaded);
  await verifySourceMaps(options);
  await writeFile(join(options.buildDir, '_app/app.js'), 'changed');
  await assert.rejects(verifySourceMaps(options), /changed/);
});

test('public source maps and release mismatch block deployment', async () => {
  const options = await fixture();
  await prepareSourceMaps(options);
  await assert.rejects(
    verifySourceMaps({ ...options, release: 'b'.repeat(40) }),
    /release/,
  );
  await writeFile(join(options.buildDir, 'leaked.map'), '{}');
  await assert.rejects(verifySourceMaps(options), /public source map/);
});

test('directive-looking JavaScript string values are preserved', async () => {
  const options = await fixture();
  const source = 'console.log("//# sourceMappingURL=literal-not-a-comment");\n';
  await writeFile(join(options.buildDir, '_app/app.js'), source);
  await prepareSourceMaps(options);
  assert.ok(
    (await readFile(join(options.buildDir, '_app/app.js'), 'utf8')).includes(
      source,
    ),
  );
});

test('unlisted public JavaScript blocks publication', async () => {
  const options = await fixture();
  await prepareSourceMaps(options);
  await writeFile(
    join(options.buildDir, 'unexpected.js'),
    'console.log("unexpected");',
  );
  await assert.rejects(verifySourceMaps(options), /JavaScript.*changed/);
});

test('official injection is deterministic for identical emitted assets', async () => {
  const first = await fixture();
  const second = await fixture();
  await prepareSourceMaps(first);
  await prepareSourceMaps(second);
  assert.equal(
    await readFile(join(first.buildDir, '_app/app.js'), 'utf8'),
    await readFile(join(second.buildDir, '_app/app.js'), 'utf8'),
  );
});
