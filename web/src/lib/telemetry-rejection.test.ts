import assert from 'node:assert/strict';
import test from 'node:test';
import { sanitizeException, sanitizeError } from './telemetry.ts';

test('non-Error rejection keeps only redacted own diagnostic fields and trusted frames', () => {
  const error = {
    name: 'NotAllowedError',
    message: 'Notification permission failed for private@example.com',
    stack:
      'NotAllowedError: Notification permission failed for private@example.com\n    at allow (https://app.test/_app/immutable/chunks/ABC-123.js:12:3)',
    response: { body: 'private mail body' },
    token: 'private-token-value',
  };
  const safe = sanitizeException(
    error,
    'https://app.test',
    new Set(['/_app/immutable/chunks/ABC-123.js']),
  );
  assert.equal(safe.type, 'NotAllowedError');
  assert.match(safe.value, /Notification permission failed/);
  assert.doesNotMatch(
    JSON.stringify(safe),
    /private|example\.com|response|token/,
  );
  assert.equal(safe.stacktrace?.frames[0]?.lineno, 12);
  assert.equal(sanitizeError(error).name, 'NotAllowedError');
});

test('non-Error rejection never executes arbitrary diagnostic getters', () => {
  let reads = 0;
  const rejection = Object.defineProperties(
    {},
    {
      message: {
        get() {
          reads++;
          throw new Error('private');
        },
      },
      stack: {
        get() {
          reads++;
          throw new Error('private');
        },
      },
    },
  );
  assert.doesNotThrow(() => sanitizeException(rejection));
  assert.equal(reads, 0);
  assert.equal(
    sanitizeException({ message: 'request failed' }).stacktrace,
    undefined,
  );
});

test('native DOMException records its technical name without a fabricated stack', () => {
  const safe = sanitizeException(
    new DOMException('Permission denied', 'NotAllowedError'),
  );
  assert.equal(safe.type, 'NotAllowedError');
  assert.equal(safe.value, 'Permission denied');
});
