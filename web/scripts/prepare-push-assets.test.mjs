import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  cp,
  mkdtemp,
  mkdir,
  readFile,
  readdir,
  rm,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { test } from 'node:test';
import { preparePushAssets } from './prepare-push-assets.mjs';
import { prepareSourceMaps, verifySourceMaps } from './source-maps.mjs';

const vendorDir = resolve('vendor/onesignal/160800');
const hashes = {
  'OneSignalSDK.page.es6.js':
    '0292910a0838f663f308d769d79ee22b74457c8c5b89d26147a64712f06a3e82',
  'OneSignalSDK.sw.js':
    'd49a931f9d170f5c2bdf477c2b3d431bcf4d551c82eca66921e2c506888d91bf',
};
const digest = (value) => createHash('sha256').update(value).digest('hex');

test('official assets match pinned hashes and preparation strips only source map directives', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'cycrio-push-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const staticDir = join(root, 'static');
  await preparePushAssets({ vendorDir, staticDir });
  for (const [name, hash] of Object.entries(hashes)) {
    const original = await readFile(join(vendorDir, name), 'utf8');
    assert.equal(digest(original), hash);
    const generated = await readFile(
      join(staticDir, 'vendor/onesignal/160800', name),
      'utf8',
    );
    assert.equal(
      generated,
      original.replace(
        /^\/\/[#@]\s*sourceMappingURL=[^\r\n]*(?:\r?\n)?$/gm,
        '',
      ),
    );
    assert.doesNotMatch(generated, /sourceMappingURL/);
  }
  const workerPath = join(staticDir, 'OneSignalSDKWorker.js');
  const worker = await readFile(workerPath, 'utf8');
  assert.equal(
    worker,
    "importScripts('/vendor/onesignal/160800/OneSignalSDK.sw.js');\n",
  );
  assert.doesNotMatch(worker, /https?:|caches|fetch|CacheStorage/);
  const before = digest(await readFile(workerPath));
  await preparePushAssets({ vendorDir, staticDir });
  assert.equal(digest(await readFile(workerPath)), before);
  assert.deepEqual(await readdir(join(staticDir, 'vendor/onesignal/160800')), [
    'LICENSE',
    ...Object.keys(hashes),
  ]);
  assert.equal(
    await readFile(join(staticDir, 'vendor/onesignal/160800/LICENSE'), 'utf8'),
    await readFile(join(vendorDir, 'LICENSE'), 'utf8'),
  );
});

test('integrity failure rejects all publication before creating public files', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'cycrio-push-integrity-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const copiedVendor = join(root, 'vendor');
  await cp(vendorDir, copiedVendor, { recursive: true });
  await writeFile(
    join(copiedVendor, 'OneSignalSDK.sw.js'),
    'changed SDK bytes',
  );
  const staticDir = join(root, 'static');
  await assert.rejects(
    preparePushAssets({ vendorDir: copiedVendor, staticDir }),
    /integrity/,
  );
  await assert.rejects(readFile(join(staticDir, 'OneSignalSDKWorker.js')), {
    code: 'ENOENT',
  });
  await assert.rejects(
    readFile(
      join(staticDir, 'vendor/onesignal/160800/OneSignalSDK.page.es6.js'),
    ),
    { code: 'ENOENT' },
  );
});

test('manifest has stable install identity, root scope and standalone display', async () => {
  const manifest = JSON.parse(
    await readFile('static/site.webmanifest', 'utf8'),
  );
  assert.equal(manifest.id, '/');
  assert.equal(manifest.scope, '/');
  assert.equal(manifest.start_url, '/');
  assert.equal(manifest.display, 'standalone');
  assert.deepEqual(
    manifest.icons.map((icon) => icon.src),
    ['/icon-192.png', '/icon-512.png'],
  );
});

test('dev, check, test and build prepare generated SDK files', async () => {
  const { scripts } = JSON.parse(await readFile('package.json', 'utf8'));
  for (const phase of ['dev', 'check', 'test', 'build']) {
    assert.match(
      scripts[`pre${phase}`],
      /node scripts\/prepare-push-assets\.mjs/,
    );
  }
  const ignored = await readFile('.prettierignore', 'utf8');
  assert.match(ignored, /vendor\/onesignal\//);
  assert.match(ignored, /static\/vendor\/onesignal\//);
  assert.match(ignored, /static\/OneSignalSDKWorker\.js/);
});

test('SDK and worker are compatible with private build source map verification', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'cycrio-push-maps-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const buildDir = join(root, 'build');
  await preparePushAssets({ vendorDir, staticDir: buildDir });
  await mkdir(join(buildDir, '_app'), { recursive: true });
  await writeFile(join(buildDir, '_app/app.js'), 'console.log("test");\n');
  await writeFile(
    join(buildDir, '_app/app.js.map'),
    JSON.stringify({
      version: 3,
      file: 'app.js',
      sources: ['test.js'],
      sourcesContent: ['console.log("test");'],
      names: [],
      mappings: 'AAAA',
    }),
  );
  const options = {
    buildDir,
    privateDir: join(root, 'maps'),
    release: 'a'.repeat(40),
  };
  await prepareSourceMaps(options);
  await verifySourceMaps(options);
  const manifest = JSON.parse(
    await readFile(join(options.privateDir, 'manifest.json'), 'utf8'),
  );
  assert.ok(
    manifest.assets.some(({ path }) => path === 'OneSignalSDKWorker.js'),
  );
  assert.ok(
    manifest.assets.some(
      ({ path }) => path === 'vendor/onesignal/160800/OneSignalSDK.sw.js',
    ),
  );
});
