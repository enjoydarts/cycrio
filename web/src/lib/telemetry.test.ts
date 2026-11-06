import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createTelemetryController,
  sanitizeException,
  filterTelemetryEvent,
} from './telemetry.ts';
import { serializePostHogCredential } from './credentials.ts';

const config = {
  enabled: true as const,
  project_token: 'phc_test_token',
  host: 'https://us.i.posthog.com',
};
const owner = '11111111-1111-4111-8111-111111111111';
const turn = async () => {
  await new Promise<void>((resolve) => setTimeout(resolve, 0));
};
function fixture(production = true) {
  const target = new EventTarget();
  const calls: { event: string; properties: Record<string, unknown> }[] = [];
  const resets: boolean[] = [];
  const options: Record<string, unknown>[] = [];
  let enabled = true;
  let token = config.project_token;
  let fail = false;
  const sdk = {
    capture(event: string, properties: Record<string, unknown>) {
      if (fail) throw new Error('private');
      calls.push({ event, properties });
    },
    reset() {
      resets.push(true);
    },
    disable() {},
  };
  const controller = createTelemetryController({
    production,
    target,
    getConfiguration: async () =>
      enabled ? { ...config, project_token: token } : { enabled: false },
    loadSDK: async () => ({
      initialize: (_token, opts) => {
        options.push(opts);
        return sdk;
      },
    }),
    refreshMilliseconds: 1_000_000,
  });
  return {
    controller,
    target,
    calls,
    resets,
    options,
    rotate: () => {
      token = 'phc_new_project_token';
    },
    disable: () => {
      enabled = false;
    },
    fail: () => {
      fail = true;
    },
  };
}

test('PostHog config accepts only project tokens and exact supported ingest hosts', () => {
  assert.deepEqual(
    JSON.parse(
      serializePostHogCredential({
        project_token: config.project_token,
        host: config.host,
      }),
    ),
    { project_token: config.project_token, host: config.host },
  );
  for (const value of ['phx_private_key', '', 'phc_x', 'phc_has space'])
    assert.throws(() =>
      serializePostHogCredential({ project_token: value, host: config.host }),
    );
  for (const host of [
    'http://us.i.posthog.com',
    'https://us.i.posthog.com/',
    'https://us.i.posthog.com.evil.test',
    'https://user@us.i.posthog.com',
    'https://us.i.posthog.com?token=private',
  ])
    assert.throws(() =>
      serializePostHogCredential({ project_token: config.project_token, host }),
    );
});

test('production gate, exception-only capture, safe identity and reset', async () => {
  const dev = fixture(false);
  dev.controller.start();
  await turn();
  assert.equal(dev.options.length, 0);
  dev.controller.dispose();
  const f = fixture();
  f.controller.start();
  await turn();
  f.controller.setUser(owner);
  f.controller.capture(new Error('private@example.com'));
  assert.equal(f.calls.length, 1);
  assert.equal(f.calls[0].event, '$exception');
  assert.equal(f.calls[0].properties.distinct_id, owner);
  assert.doesNotMatch(
    JSON.stringify(f.calls),
    /private@example|https?:|\bError: /,
  );
  f.controller.setUser(null);
  f.controller.capture(new Error('anonymous'));
  assert.equal(f.calls.length, 2);
  assert.equal(f.calls[1].properties.distinct_id, 'cycrio-browser-anonymous');
  assert.ok(f.resets.length > 0);
  f.controller.dispose();
});

test('all capture entrypoints deduplicate Error objects and teardown listeners', async () => {
  const f = fixture();
  f.controller.start();
  await turn();
  const error = new Error('private');
  const event = new Event('error');
  Object.defineProperty(event, 'error', { value: error });
  f.target.dispatchEvent(event);
  f.controller.capture(error);
  const rejection = new Event('unhandledrejection');
  Object.defineProperty(rejection, 'reason', { value: error });
  f.target.dispatchEvent(rejection);
  assert.equal(f.calls.length, 1);
  f.controller.dispose();
  f.target.dispatchEvent(new Event('error'));
  f.controller.capture(new Error('late'));
  assert.equal(f.calls.length, 1);
});

test('disabled or failed configuration stops capture and capture errors do not escape', async () => {
  const f = fixture();
  f.controller.start();
  await turn();
  f.fail();
  assert.doesNotThrow(() => f.controller.capture(new Error('network')));
  f.disable();
  await f.controller.refresh();
  f.controller.capture(new Error('disabled'));
  assert.equal(f.calls.length, 0);
  f.controller.dispose();
  let reject = false;
  const g = fixture();
  g.controller.dispose();
  const c = createTelemetryController({
    production: true,
    target: new EventTarget(),
    getConfiguration: async () => {
      if (reject) throw new Error('private');
      return config;
    },
    loadSDK: async () => ({
      initialize: () => ({
        capture() {
          throw new Error('should not escape');
        },
        reset() {},
        disable() {},
      }),
    }),
    refreshMilliseconds: 1_000_000,
  });
  c.start();
  await turn();
  reject = true;
  await c.refresh();
  assert.doesNotThrow(() => c.capture('private'));
  c.dispose();
});

test('late configuration and SDK completion cannot initialize after disposal', async () => {
  let complete!: (value: typeof config) => void;
  let loads = 0;
  const c = createTelemetryController({
    production: true,
    target: new EventTarget(),
    getConfiguration: () =>
      new Promise((resolve) => {
        complete = resolve;
      }),
    loadSDK: async () => {
      loads++;
      throw new Error('unexpected');
    },
    refreshMilliseconds: 1_000_000,
  });
  c.start();
  c.dispose();
  complete(config);
  await turn();
  assert.equal(loads, 0);
});

test('raw errors are replaced and only safe static asset frames survive', () => {
  const e = new Error('private@example.com');
  e.stack =
    'Error: private\n at privateFunction (https://app.test/_app/immutable/chunks/ABC-123.js?email=private:14:7)\n at secret (https://app.test/subscriptions/private-id:1:2)\n at https://external.test/_app/immutable/chunks/other.js:1:2';
  const result = sanitizeException(
    e,
    'https://app.test',
    new Set(['/_app/immutable/chunks/ABC-123.js']),
  );
  assert.doesNotMatch(
    JSON.stringify(result),
    /private|external|https?:|email|secret|Function/,
  );
  assert.equal(result.type, 'Error');
  assert.equal(result.value, 'Unhandled application error');
  assert.equal(result.stacktrace?.type, 'raw');
  assert.deepEqual(result.stacktrace?.frames, [
    {
      platform: 'web:javascript',
      filename: '/_app/immutable/chunks/ABC-123.js',
      function: '<redacted>',
      in_app: true,
      lineno: 14,
      colno: 7,
    },
  ]);
  assert.doesNotThrow(() =>
    sanitizeException({
      get stack() {
        throw new Error('private');
      },
    }),
  );
  const forged = new Error(
    'private\n at https://app.test/_app/immutable/chunks/private_customer_secret.js:1:1',
  );
  assert.equal(
    sanitizeException(
      forged,
      'https://app.test',
      new Set(['/_app/immutable/chunks/ABC-123.js']),
    ).stacktrace,
    undefined,
  );
});

test('final exception allowlist reconstructs the SDK raw stacktrace schema', () => {
  const filename = '/_app/immutable/chunks/ABC-123.js';
  const result = filterTelemetryEvent(
    {
      event: '$exception',
      properties: {
        $exception_list: [
          {
            type: 'Error',
            value: 'Unhandled application error',
            stacktrace: {
              type: 'raw',
              frames: [
                {
                  platform: 'private-unsupported-platform',
                  filename,
                  lineno: 14,
                  colno: 7,
                  function: 'privateFunction',
                  vars: { secret: 'private' },
                },
              ],
            },
          },
        ],
      },
    },
    new Set([filename]),
  );
  assert.deepEqual(result?.properties.$exception_list, [
    {
      type: 'Error',
      value: 'Unhandled application error',
      stacktrace: {
        type: 'raw',
        frames: [
          {
            platform: 'web:javascript',
            filename,
            function: '<redacted>',
            in_app: true,
            lineno: 14,
            colno: 7,
          },
        ],
      },
    },
  ]);
});

test('final SDK allowlist strips automatic enrichment and rejects analytics', () => {
  const exception = sanitizeException(new Error('private'));
  const event = filterTelemetryEvent({
    event: '$exception',
    properties: {
      $exception_list: [exception],
      distinct_id: owner,
      $current_url: 'https://private',
      $ip: '127.0.0.1',
      email: 'private@example.com',
      token: 'public',
      $device_id: 'tracking',
      $set: { email: 'private' },
    },
  });
  assert.deepEqual(event?.properties, {
    $exception_list: [exception],
    distinct_id: owner,
    $process_person_profile: false,
    $geoip_disable: true,
    runtime: 'browser',
    $lib: 'web',
    $lib_version: '1.447.2',
  });
  assert.equal(
    filterTelemetryEvent({ event: '$pageview', properties: {} }),
    null,
  );
  assert.equal(
    filterTelemetryEvent({
      event: '$exception',
      properties: { $exception_list: [{ value: 'raw private' }] },
    }),
    null,
  );
});

test('final hook replaces SDK anonymous identity and cannot reuse an old config after rotation', async () => {
  const f = fixture();
  f.controller.start();
  await turn();
  const before = f.options[0].before_send as (event: any) => any;
  const input = {
    event: '$exception',
    uuid: 'event-id',
    properties: {
      $exception_list: [sanitizeException('raw')],
      distinct_id: 'sdk-generated-id',
      $current_url: 'private',
      token: 'untrusted',
    },
  };
  assert.equal(
    before(input).properties.distinct_id,
    'cycrio-browser-anonymous',
  );
  assert.equal(before(input).properties.token, config.project_token);
  f.controller.setUser(owner);
  assert.equal(before(input).properties.distinct_id, owner);
  f.controller.setUser('user_clerkid');
  assert.equal(
    before(input).properties.distinct_id,
    'cycrio-browser-anonymous',
  );
  f.rotate();
  await f.controller.refresh();
  assert.equal(before(input), null);
  f.disable();
  await f.controller.refresh();
  assert.equal(before(input), null);
  f.controller.dispose();
});

test('SDK configuration keeps recording, analytics, flags, logs and persistence off', async () => {
  const f = fixture();
  f.controller.start();
  await turn();
  const opts = f.options[0];
  for (const key of [
    'autocapture',
    'capture_pageview',
    'capture_pageleave',
    'capture_exceptions',
    'capture_performance',
    'capture_heatmaps',
    'capture_dead_clicks',
    'capture_webmcp',
    'ip',
    'save_referrer',
    'save_campaign_params',
    'request_batching',
  ])
    assert.equal(opts[key], false, key);
  for (const key of [
    'disable_session_recording',
    'disable_persistence',
    'disable_surveys',
    'disable_product_tours',
    'disable_conversations',
    'disable_web_experiments',
    'disable_external_dependency_loading',
    'advanced_disable_flags',
  ])
    assert.equal(opts[key], true, key);
  assert.equal(opts.person_profiles, 'never');
  assert.equal(opts.persistence, 'memory');
  f.controller.dispose();
});

test('newer disabled config wins over an older enabled response', async () => {
  const resolvers: ((value: typeof config | { enabled: false }) => void)[] = [];
  let loads = 0;
  const c = createTelemetryController({
    production: true,
    target: new EventTarget(),
    getConfiguration: () => new Promise((resolve) => resolvers.push(resolve)),
    loadSDK: async () => {
      loads++;
      throw Error('unexpected');
    },
    refreshMilliseconds: 1_000_000,
  });
  c.start();
  const newer = c.refresh();
  resolvers[1]({ enabled: false });
  await newer;
  resolvers[0](config);
  await turn();
  assert.equal(loads, 0);
  c.dispose();
});
