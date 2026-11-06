import assert from 'node:assert/strict';
import test from 'node:test';
import { createDefaultStackParser } from '@posthog/core/error-tracking';
import {
  scrubText,
  sanitizeError,
  sanitizeException,
  filterTelemetryEvent,
} from './telemetry.ts';

const origin = 'https://app.test';
const asset = origin + '/_app/immutable/chunks/ABC-123.js';
const assets = new Set([asset]);
const chunk = '11111111-2222-4333-8444-555555555555';

test('ordinary technical text, quoted identifiers and multiline causes stay useful', () => {
  for (const text of [
    "Cannot read properties of undefined (reading 'foo')",
    'ReferenceError: subscriptionState is not defined',
    "SQLSTATE '23505': duplicate key",
    'Rendering failed\nCause: invalid transition\nRetry count: 2',
  ])
    assert.equal(scrubText(text), text);
});

test('short technical identifiers survive while personal URL path segments do not', () => {
  assert.equal(
    scrubText('Missing column sk_foo in schema'),
    'Missing column sk_foo in schema',
  );
  assert.equal(
    scrubText(
      'GET https://alice:password@api.test/v1/run?token=private#x: timeout',
    ),
    'GET https://api.test/v1/run: timeout',
  );
  assert.equal(
    scrubText('GET https://api.test/users/alice%40example.com/preferences'),
    'GET https://api.test/users/[redacted]/preferences',
  );
  assert.equal(
    scrubText(
      'provider 503 body={"subject":"Hidden subject"}\nCause: parse failed',
    ),
    'provider 503 body=[redacted]\nCause: parse failed',
  );
});

test('Firefox stack functions and custom technical Error names remain available', () => {
  const error = new Error('loadSubscription failed');
  error.name = 'SubscriptionRenderError';
  error.stack = `loadSubscription@${asset}:42:7\nrender@${asset}:10:2`;
  const safe = sanitizeException(error, origin, assets);
  assert.equal(safe.type, 'SubscriptionRenderError');
  assert.deepEqual(
    safe.stacktrace?.frames.map((frame) => frame.function),
    ['render', 'loadSubscription'],
  );
});

test('sensitive field values, credentials, email and URL secrets are scrubbed', () => {
  const messages = [
    'TypeError for private@example.com with Bearer abcdefghijklmnop',
    'request failed: sk_live_123456789012345678901 phx_12345678901234567890 eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJzZWNyZXQifQ.c2lnbmF0dXJl',
    'provider 503 body={"email":"private@example.com","subject":"Hidden subject","content":"Private mail text"}\nCause: parse failed',
    'invalid payload: {"from":"private@example.com","analysis_result":{"service":"Private parsed result"}}',
    'request headers={"Authorization":"Bearer abcdefghijklmnop","Cookie":"session=private-cookie"} timeout=30',
    'response subject="Hidden subject" from="Private Sender" to="Private Recipient"',
    'Authorization: Basic dXNlcjpwYXNzd29yZA==\nCookie: session=private-cookie; account=private-account',
  ];
  for (const text of messages) {
    const safe = scrubText(text);
    assert.doesNotMatch(
      safe,
      /private@example|abcdefghijklmnop|sk_live_|phx_|eyJhb|Hidden subject|Private mail text|Private parsed result|Private Sender|Private Recipient|private-cookie|private-account|dXNlcj/,
    );
    assert.match(safe, /\[redacted\]/);
  }
  const url = scrubText(
    'GET https://alice:password@api.test/v1/run?token=private#fragment: timeout',
  );
  assert.doesNotMatch(url, /alice|password|token=|fragment|private/);
  assert.match(url, /timeout/);
});

test('quoted invalid-input snippets and embedded JSON payloads are removed without masking technical identifiers', () => {
  assert.equal(
    scrubText('invalid input syntax for integer: "John Doe"'),
    'invalid input syntax for integer: [redacted]',
  );
  assert.equal(
    scrubText(
      'upstream rejected {"customer":"John Doe","address":"123 Main St"} at parse step',
    ),
    'upstream rejected [redacted] at parse step',
  );
  assert.equal(
    scrubText("Cannot read properties of undefined (reading 'foo')"),
    "Cannot read properties of undefined (reading 'foo')",
  );
  assert.equal(
    scrubText('unexpected interface{} type'),
    'unexpected interface{} type',
  );
});

test('OAuth, application-password, webhook and provider credential vectors are masked', () => {
  for (const value of [
    'token=ya29.a0AfH6SMprivatecredential',
    'client_secret=GOCSPX-privatecredential',
    'app_password=private-application-password',
    'provider rejected ya29.a0AfH6SMprivatecredential',
    'provider rejected GOCSPX-privatecredential',
    'invalid webhook key whsec_privatecredential0123456789',
    'invalid Resend key re_privatecredential0123456789',
  ]) {
    const result = scrubText(value);
    assert.match(result, /\[redacted\]/);
    assert.doesNotMatch(
      result,
      /privatecredential|private-application-password/,
    );
  }
});

test('recognizable mail header blocks include the message body in redaction', () => {
  const result = scrubText(
    'Mail parse failed\nSubject: invoice\nFrom: person@example.test\nTo: receiver@example.test\n\nDear John Doe,\nyour subscription costs 99 USD.',
  );
  assert.equal(result, 'Mail parse failed\n[redacted]');
  assert.equal(
    scrubText('Rendering failed\nCause: invalid transition\nRetry count: 2'),
    'Rendering failed\nCause: invalid transition\nRetry count: 2',
  );
});

test('DB/mail connection URLs, account labels and Healthchecks path credentials are scrubbed', () => {
  for (const scheme of [
    'postgresql',
    'postgres',
    'imap',
    'imaps',
    'smtp',
    'smtps',
    'mysql',
    'redis',
    'rediss',
    'mongodb',
    'mongodb+srv',
    'sqlserver',
  ])
    assert.equal(
      scrubText(
        `connect ${scheme}://alice:password@db.test/cycrio?token=private#x`,
      ),
      `connect ${scheme}://db.test/cycrio`,
    );
  for (const label of ['login', 'username', 'user_name', 'user', 'email'])
    assert.equal(scrubText(`${label}=private-account`), `${label}=[redacted]`);
  assert.equal(
    scrubText('ping https://hc-ping.com/11111111-1111-4111-8111-111111111111'),
    'ping https://hc-ping.com/[redacted]',
  );
});

test('internationalized email addresses are scrubbed along with ASCII addresses', () => {
  assert.equal(
    scrubText('User 名前@例え.テスト not found'),
    'User [redacted] not found',
  );
});

test('TypeError name, message, genuine function and public asset URL survive into safe Error', () => {
  const error = new TypeError(
    "Cannot read properties of undefined (reading 'foo')",
  );
  error.stack = `TypeError: ${error.message}\n    at loadSubscription (${asset}?access_token=private#x:42:7)\n    at privateFn (https://external.test/private/user-id:3:4)`;
  const safe = sanitizeError(error, origin, assets);
  assert.equal(safe.name, 'TypeError');
  assert.equal(safe.message, error.message);
  const frames = createDefaultStackParser()(safe.stack ?? '');
  assert.equal(frames.length, 1);
  assert.equal(frames[0].function, 'loadSubscription');
  assert.equal(frames[0].filename, asset);
  assert.equal(frames[0].lineno, 42);
  assert.equal(frames[0].colno, 7);
  assert.doesNotMatch(safe.stack ?? '', /private|external|token=|#x/);
});

test('all message lines are removed from stack parsing, including forged allowlisted frames', () => {
  const error = new TypeError(
    `Loading failed\n    at forgedFunction (${asset}:999:999)`,
  );
  error.stack = `TypeError: ${error.message}\n    at genuineFunction (${asset}:42:7)`;
  const result = sanitizeException(error, origin, assets);
  assert.deepEqual(
    result.stacktrace?.frames.map((frame) => [frame.function, frame.lineno]),
    [['genuineFunction', 42]],
  );
});

test('safe Error native SDK reparsing cannot resurrect frame lines inside a multiline message', () => {
  const message = `Loading failed\n    at forgedFunction (${asset}:999:999)`;
  const error = new TypeError(message);
  error.stack = `TypeError: ${message}\n    at genuineFunction (${asset}:42:7)`;
  const safe = sanitizeError(error, origin, assets);
  assert.equal(safe.name, 'TypeError');
  assert.equal(safe.message, message);
  assert.deepEqual(
    createDefaultStackParser()(safe.stack ?? '', 1).map((frame) => [
      frame.function,
      frame.lineno,
    ]),
    [['genuineFunction', 42]],
  );
});

test('safe Error preserves scrubbed cause without arbitrary rejected payloads or circular recursion', () => {
  const error = new Error('Loading failed', {
    cause: new TypeError('Network parse failed for private@example.com'),
  });
  const safe = sanitizeError(error, origin, assets);
  assert.ok(safe.cause instanceof Error);
  assert.equal(safe.cause.name, 'TypeError');
  assert.match(safe.cause.message, /Network parse failed/);
  assert.doesNotMatch(safe.cause.message, /private@example/);
  Object.defineProperty(error, 'cause', { value: error });
  assert.doesNotThrow(() => sanitizeError(error, origin, assets));
  assert.doesNotMatch(
    sanitizeError(
      { body: 'Secret mail text', analysis_result: 'Private result' },
      origin,
      assets,
    ).message,
    /Secret|Private/,
  );
});

test('AggregateError child errors and technical cause survive with bounded safe copies', () => {
  const original = new AggregateError(
    [
      new TypeError('render failed for private@example.com'),
      { body: 'Private text' },
    ],
    'Multiple operations failed',
    { cause: new ReferenceError('cacheState is not defined') },
  );
  const safe = sanitizeError(original, origin, assets);
  assert.ok(safe instanceof AggregateError);
  assert.equal(safe.name, 'AggregateError');
  assert.equal(safe.message, 'Multiple operations failed');
  assert.equal(safe.errors[0].name, 'TypeError');
  assert.match(safe.errors[0].message, /render failed/);
  assert.doesNotMatch(safe.errors[0].message, /private@example/);
  assert.doesNotMatch(safe.errors[1].message, /Private text/);
  assert.equal((safe.cause as Error).message, 'cacheState is not defined');
});

test('large AggregateError trees cannot exceed the sixteen-exception reporting budget', () => {
  const tree = (depth: number): Error =>
    depth
      ? new AggregateError(
          Array.from({ length: 4 }, () => tree(depth - 1)),
          'Multiple operations failed',
        )
      : new Error('Leaf failed');
  const count = (error: Error): number =>
    1 +
    (error.cause instanceof Error ? count(error.cause) : 0) +
    (error instanceof AggregateError
      ? error.errors.reduce(
          (total: number, child: Error) => total + count(child),
          0,
        )
      : 0);
  assert.ok(count(sanitizeError(tree(4), origin, assets)) <= 16);
});

test('final boundary preserves native type/message/function/chunk/mechanism and browser diagnostics only', () => {
  const event = filterTelemetryEvent(
    {
      event: '$exception',
      properties: {
        $exception_list: [
          {
            type: 'TypeError',
            value:
              "Cannot read properties of undefined (reading 'foo') private@example.com",
            mechanism: {
              type: 'generic',
              handled: true,
              synthetic: false,
              exception_id: 0,
              source: 'onerror',
              secret: 'private',
            },
            stacktrace: {
              type: 'raw',
              frames: [
                {
                  platform: 'web:javascript',
                  filename: asset + '?token=private#x',
                  function: 'loadSubscription',
                  in_app: true,
                  lineno: 42,
                  colno: 7,
                  chunk_id: chunk,
                  vars: { secret: 'private' },
                  context_line: 'private source',
                },
              ],
            },
          },
        ],
        $browser: 'Chrome',
        $browser_version: 155,
        $os: 'Linux',
        $os_version: '6.0',
        $current_url: 'https://app.test/private',
        $referrer: 'https://private.test',
        $session_id: 'private-session',
        $device_id: 'private-device',
        cookie: 'private-cookie',
        Authorization: 'Bearer private',
        request_body: 'private-body',
        email: 'private@example.com',
      },
    },
    assets,
    origin,
  );
  assert.ok(event);
  const exception = (event.properties.$exception_list as any[])[0];
  assert.equal(exception.type, 'TypeError');
  assert.match(exception.value, /reading 'foo'/);
  assert.doesNotMatch(exception.value, /private@example/);
  assert.deepEqual(exception.stacktrace, {
    type: 'raw',
    frames: [
      {
        platform: 'web:javascript',
        filename: asset,
        function: 'loadSubscription',
        in_app: true,
        lineno: 42,
        colno: 7,
        chunk_id: chunk,
      },
    ],
  });
  assert.deepEqual(exception.mechanism, {
    type: 'generic',
    handled: true,
    synthetic: false,
    exception_id: 0,
    source: 'onerror',
  });
  assert.equal(event.properties.$browser, 'Chrome');
  assert.equal(event.properties.$os, 'Linux');
  assert.doesNotMatch(
    JSON.stringify(event),
    /private|context_line|vars|request_body|\$current_url|\$referrer|\$device_id|\$session_id|Authorization/,
  );
});

test('final boundary rejects unknown external and relative private paths even when SDK appends frames', () => {
  const event = filterTelemetryEvent(
    {
      event: '$exception',
      properties: {
        $exception_list: [
          {
            type: 'ReferenceError',
            value: 'state is not defined',
            stacktrace: {
              type: 'raw',
              frames: [
                {
                  filename: 'https://external.test/private.js',
                  function: 'load',
                  lineno: 1,
                  colno: 2,
                },
                {
                  filename: '/_app/immutable/chunks/unknown-private.js',
                  function: 'load',
                  lineno: 1,
                  colno: 2,
                },
              ],
            },
          },
        ],
      },
    },
    assets,
    origin,
  );
  assert.ok(event);
  assert.equal(
    (event.properties.$exception_list as any[])[0].stacktrace,
    undefined,
  );
});
