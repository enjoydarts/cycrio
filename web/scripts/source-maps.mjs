import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import {
  copyFile,
  mkdir,
  readFile,
  readdir,
  rm,
  writeFile,
} from 'node:fs/promises';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const defaultBuild = resolve('build');
const defaultPrivate = resolve('.wrangler/posthog-sourcemaps');
const projectId = '714781';
const host = 'https://us.posthog.com';
const releaseName = 'cycrio-web';
const mapDirective =
  /^\s*(?:\/\/[#@]\s*sourceMappingURL=[^\r\n]*|\/\*[#@]\s*sourceMappingURL=[^\r\n]*\*\/)\s*$/m;
const hash = (data) => createHash('sha256').update(data).digest('hex');

async function files(directory) {
  const result = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) result.push(...(await files(path)));
    else if (entry.isFile()) result.push(path);
    else throw new Error('source map artifacts must contain regular files');
  }
  return result.sort();
}

function validateMap(map) {
  if (
    map.version !== 3 ||
    typeof map.mappings !== 'string' ||
    !map.mappings.length ||
    !Array.isArray(map.sources) ||
    !map.sources.length ||
    !Array.isArray(map.sourcesContent) ||
    map.sourcesContent.length !== map.sources.length ||
    !map.sourcesContent.some(
      (source) => typeof source === 'string' && source.length > 0,
    ) ||
    map.sources.some(
      (source) =>
        typeof source !== 'string' ||
        /(?:^|[/\\])(?:Users|home)[/\\]/.test(source),
    )
  ) {
    throw new Error(
      'build did not produce a usable source map with source content',
    );
  }
}

async function runCLI(args, env = {}) {
  const childEnv = { ...process.env, ...env, POSTHOG_CLI_DRY_RUN: 'false' };
  // Authentication comes only from the CI step, never a developer dotenv file.
  delete childEnv.POSTHOG_CLI_DOTENV_FILE;
  await new Promise((resolveRun, reject) => {
    const child = spawn(resolve('node_modules/.bin/posthog-cli'), args, {
      env: childEnv,
      stdio: 'inherit',
    });
    child.once('error', () =>
      reject(new Error('PostHog source map CLI could not start')),
    );
    child.once('exit', (code) =>
      code === 0
        ? resolveRun()
        : reject(new Error('PostHog source map CLI failed')),
    );
  });
}

function options(input) {
  return {
    buildDir: defaultBuild,
    privateDir: defaultPrivate,
    release: process.env.GITHUB_SHA || 'development',
    run: runCLI,
    ...input,
  };
}

function releaseArgs(release) {
  return [
    '--release-name',
    releaseName,
    '--release-version',
    release,
    '--release-mode',
    // Keep maps release-independent: unchanged chunks share their content ID
    // across commits. Binding a release here changes the uploaded map content
    // under that same ID and makes the next deployment fail with a conflict.
    'event',
  ];
}

export async function prepareSourceMaps(input = {}) {
  const { buildDir, privateDir, release, run } = options(input);
  await rm(privateDir, { recursive: true, force: true });
  const before = await files(buildDir);
  const maps = before.filter((path) => /\.m?js\.map$/.test(path));
  if (!maps.length) throw new Error('build did not produce source maps');
  for (const path of maps)
    validateMap(JSON.parse(await readFile(path, 'utf8')));
  // Release lookup requires an authenticated API call. Inject independently;
  // the later upload keeps these exact symbol sets independent of releases.
  await run(['sourcemap', 'inject', '--directory', buildDir], {
    // CLI 0.19 validates the token before selecting even the offline inject
    // command. This public dummy satisfies that parser; it cannot upload.
    POSTHOG_CLI_API_KEY: 'phx_offline_source_map_injection',
    POSTHOG_CLI_PROJECT_ID: projectId,
    POSTHOG_CLI_HOST: host,
    RUST_LOG: 'error',
  });
  const manifest = { release, assets: [], maps: [] };
  for (const path of await files(buildDir)) {
    const name = relative(buildDir, path);
    if (/\.m?js$/.test(path)) {
      const script = await readFile(path, 'utf8');
      if (mapDirective.test(script))
        throw new Error('public sourceMappingURL must not be deployed');
      if (
        maps.includes(path + '.map') &&
        (!/_posthogChunkIds/.test(script) ||
          !/chunkId=[0-9a-f-]{36}/.test(script))
      ) {
        throw new Error('PostHog chunk ID injection missing');
      }
      const target = join(privateDir, name);
      await mkdir(dirname(target), { recursive: true });
      await copyFile(path, target);
      manifest.assets.push({ path: name, sha256: hash(script) });
    } else if (path.endsWith('.map')) {
      if (maps.includes(path)) {
        const target = join(privateDir, name);
        await mkdir(dirname(target), { recursive: true });
        await copyFile(path, target);
        manifest.maps.push({ path: name, sha256: hash(await readFile(path)) });
      }
      await rm(path);
    }
  }
  await writeFile(join(privateDir, 'manifest.json'), JSON.stringify(manifest));
  await verifySourceMaps({ buildDir, privateDir, release });
  console.log(
    `Prepared ${manifest.maps.length} private source maps; public JavaScript verified.`,
  );
  return manifest;
}

export async function verifySourceMaps(input = {}) {
  const { buildDir, privateDir, release } = options(input);
  const manifest = JSON.parse(
    await readFile(join(privateDir, 'manifest.json'), 'utf8'),
  );
  if (manifest.release !== release)
    throw new Error('source map release does not match deployment');
  if (!manifest.maps?.length || !manifest.assets?.length)
    throw new Error('source map manifest is empty');
  const publicFiles = await files(buildDir);
  const publicJavaScript = publicFiles
    .filter((path) => /\.m?js$/.test(path))
    .map((path) => relative(buildDir, path))
    .sort();
  const listedJavaScript = manifest.assets.map((asset) => asset.path).sort();
  if (JSON.stringify(publicJavaScript) !== JSON.stringify(listedJavaScript))
    throw new Error(
      'public JavaScript file set changed after source map injection',
    );
  for (const path of publicFiles) {
    if (path.endsWith('.map'))
      throw new Error('public source map must not be deployed');
    if (
      /\.m?js$/.test(path) &&
      mapDirective.test(await readFile(path, 'utf8'))
    ) {
      throw new Error('public sourceMappingURL must not be deployed');
    }
  }
  for (const asset of [...manifest.assets, ...manifest.maps]) {
    if (
      typeof asset.path !== 'string' ||
      asset.path.startsWith('/') ||
      asset.path.split(/[\\/]/).includes('..')
    ) {
      throw new Error('invalid source map artifact path');
    }
    const staged = await readFile(join(privateDir, asset.path));
    if (hash(staged) !== asset.sha256)
      throw new Error('private source map artifact changed');
    if (asset.path.endsWith('.map')) validateMap(JSON.parse(staged));
    else if (hash(await readFile(join(buildDir, asset.path))) !== asset.sha256)
      throw new Error('public JavaScript changed after source map injection');
  }
  return manifest;
}

export async function uploadSourceMaps(input = {}) {
  const {
    buildDir,
    privateDir,
    release,
    run,
    env = process.env,
  } = options(input);
  if (!env.POSTHOG_CLI_API_KEY)
    throw new Error(
      'POSTHOG_CLI_API_KEY is required for production source map upload',
    );
  if (!/^[0-9a-f]{40}$/.test(release))
    throw new Error('source map release must be a commit SHA');
  await verifySourceMaps({ buildDir, privateDir, release });
  await run(
    ['sourcemap', 'upload', '--directory', privateDir, ...releaseArgs(release)],
    {
      POSTHOG_CLI_API_KEY: env.POSTHOG_CLI_API_KEY,
      POSTHOG_CLI_PROJECT_ID: projectId,
      POSTHOG_CLI_HOST: host,
    },
  );
  await verifySourceMaps({ buildDir, privateDir, release });
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const action = {
    prepare: prepareSourceMaps,
    upload: uploadSourceMaps,
    verify: verifySourceMaps,
  }[process.argv[2]];
  if (!action)
    throw new Error('expected source map action: prepare, upload or verify');
  await action();
}
