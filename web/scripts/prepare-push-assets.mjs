import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const version = '160800';
const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const originalHashes = {
  'OneSignalSDK.page.es6.js':
    '0292910a0838f663f308d769d79ee22b74457c8c5b89d26147a64712f06a3e82',
  'OneSignalSDK.sw.js':
    'd49a931f9d170f5c2bdf477c2b3d431bcf4d551c82eca66921e2c506888d91bf',
};

// ビルド時のネットワーク取得は行わない。全原本の検証後に公開ファイルを書き、
// 独立したsourceMappingURL行以外のSDKバイト列を維持する。
export async function preparePushAssets({
  vendorDir = join(webRoot, 'vendor/onesignal', version),
  staticDir = join(webRoot, 'static'),
} = {}) {
  const assets = [];
  for (const [name, expected] of Object.entries(originalHashes)) {
    const bytes = await readFile(join(vendorDir, name));
    if (createHash('sha256').update(bytes).digest('hex') !== expected) {
      throw new Error(`OneSignal SDK integrity check failed: ${name}`);
    }
    assets.push([
      name,
      bytes
        .toString('utf8')
        .replace(/^\/\/[#@]\s*sourceMappingURL=[^\r\n]*(?:\r?\n)?$/gm, ''),
    ]);
  }
  const targetDir = join(staticDir, 'vendor/onesignal', version);
  const license = await readFile(join(vendorDir, 'LICENSE'));
  await mkdir(targetDir, { recursive: true });
  for (const [name, script] of assets) {
    await writeFile(join(targetDir, name), script);
  }
  await writeFile(join(targetDir, 'LICENSE'), license);
  await writeFile(
    join(staticDir, 'OneSignalSDKWorker.js'),
    `importScripts('/vendor/onesignal/${version}/OneSignalSDK.sw.js');\n`,
  );
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  await preparePushAssets();
}
