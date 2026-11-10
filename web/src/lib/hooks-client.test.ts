import assert from 'node:assert/strict';
import { registerHooks } from 'node:module';
import test from 'node:test';

const origin = 'https://cycrio.test';
const state = {
  captured: [] as unknown[],
  checks: 0,
  check: async () => true,
};
// Only the SDK transport and SvelteKit's version endpoint are substituted;
// execute the application's real client hook and error classification.
const key = '__cycrioClientHookTest';
Object.assign(globalThis, { [key]: state, window: { location: { origin } } });
const loader = registerHooks({
  resolve(specifier, context, nextResolve) {
    const source =
      specifier === '#lib/telemetry-browser.ts'
        ? `export const captureBrowserError = error => globalThis.${key}.captured.push(error);`
        : specifier === '$app/state'
          ? `export const updated = { check() { globalThis.${key}.checks++; return globalThis.${key}.check(); } };`
          : undefined;
    return source === undefined
      ? nextResolve(specifier, context)
      : {
          url: 'data:text/javascript,' + encodeURIComponent(source),
          shortCircuit: true,
        };
  },
});
const { handleError } = await import('../hooks.client.ts');
loader.deregister();

test.beforeEach(() => {
  state.captured = [];
  state.checks = 0;
  state.check = async () => true;
});
test.after(() => {
  Reflect.deleteProperty(globalThis, key);
  Reflect.deleteProperty(globalThis, 'window');
});
const moduleError = () =>
  new TypeError(
    `error loading dynamically imported module: ${origin}/_app/immutable/nodes/5.old.js`,
  );
const report = (error: unknown, kind = 'unknown') =>
  handleError({ error, kind } as Parameters<typeof handleError>[0]);

test('a removed module from a confirmed previous release is left to SvelteKit recovery', async () => {
  await report(moduleError());
  assert.deepEqual(state.captured, []);
  assert.equal(state.checks, 1);
});

test('missing modules in the current release are still reported', async () => {
  state.check = async () => false;
  const error = moduleError();
  await report(error);
  assert.deepEqual(state.captured, [error]);
});

test('Chromium module-load errors use the same confirmed-release recovery boundary', async () => {
  await report(
    new TypeError(
      `Failed to fetch dynamically imported module: ${origin}/_app/immutable/nodes/5.old.js`,
    ),
  );
  assert.deepEqual(state.captured, []);
  assert.equal(state.checks, 1);
});

test('version check failure still reports the original module error', async () => {
  state.check = async () => {
    throw new Error('offline');
  };
  const error = moduleError();
  await report(error);
  assert.deepEqual(state.captured, [error]);
});

test('ordinary errors and foreign or non-immutable modules never trigger recovery filtering', async () => {
  const errors = [
    new Error(
      `error loading dynamically imported module: ${origin}/_app/immutable/nodes/5.old.js`,
    ),
    new TypeError('Cannot read properties of undefined'),
    new TypeError(
      'error loading dynamically imported module: https://foreign.test/_app/immutable/nodes/5.old.js',
    ),
    new TypeError(
      `error loading dynamically imported module: ${origin}/api/private.js`,
    ),
    new TypeError(
      `error loading dynamically imported module: ${origin}/_app/immutable/nodes/5.old.js?token=private`,
    ),
    new TypeError(
      `error loading dynamically imported module: ${origin}/_app/immutable/nodes/5.old.js#private`,
    ),
    new TypeError(
      'error loading dynamically imported module: https://user:secret@cycrio.test/_app/immutable/nodes/5.old.js',
    ),
    new TypeError('Importing a module script failed'),
  ];
  for (const error of errors) await report(error);
  assert.deepEqual(state.captured, errors);
  assert.equal(state.checks, 0);
});

test('app and framework errors retain SvelteKit handling without telemetry', async () => {
  await report({ message: 'Not found' }, 'app');
  await report({ message: 'Not found' }, 'framework');
  assert.deepEqual(state.captured, []);
  assert.equal(state.checks, 0);
});
