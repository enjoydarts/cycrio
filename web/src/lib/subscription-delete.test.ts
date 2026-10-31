import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createSubscriptionsAPI } from './subscriptions-api.ts';
import { createSubscriptionDelete } from './subscription-delete.ts';
import { id } from './subscriptions-fixtures.ts';

test('delete validates confirmation and version before sending a minimal POST', async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  const api = createSubscriptionsAPI('', async (url, init) => {
    calls.push({ url: String(url), init });
    return new Response('{"deleted":true}');
  });
  for (const body of [
    { expected_version: 0, confirmed: true },
    { expected_version: 1, confirmed: false },
  ])
    await assert.rejects(
      api.delete(id, body, async () => 't'),
      { code: 'invalid_request' },
    );
  assert.equal(calls.length, 0);
  assert.deepEqual(
    await api.delete(
      id,
      { expected_version: 3, confirmed: true },
      async () => 't',
    ),
    { deleted: true },
  );
  assert.equal(calls[0].url, `/v1/subscriptions/${id}/delete`);
  assert.equal(calls[0].init?.method, 'POST');
  assert.deepEqual(JSON.parse(String(calls[0].init?.body)), {
    expected_version: 3,
    confirmed: true,
  });
});
test('delete rejects an unconfirmed success response', async () => {
  const api = createSubscriptionsAPI(
    '',
    async () => new Response('{"deleted":false}'),
  );
  await assert.rejects(
    api.delete(id, { expected_version: 1, confirmed: true }, async () => 't'),
    { code: 'dependency_unavailable' },
  );
});
test('delete freezes close and navigation while pending and uncertain, replaying the same version', async () => {
  const bodies: unknown[] = [];
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  let done = 0;
  const c = createSubscriptionDelete(
    {
      delete: async (_id, body) => {
        bodies.push(body);
        if (bodies.length === 1) {
          await gate;
          throw new ApiError('dependency_unavailable');
        }
        return { deleted: true };
      },
    },
    async () => 't',
    id,
    4,
    () => {},
    () => {
      done++;
    },
    () => {},
  );
  const first = c.submit();
  assert.equal(c.state().locked, true);
  await c.submit();
  assert.equal(bodies.length, 1);
  release();
  await first;
  assert.equal(c.state().uncertain, true);
  assert.equal(c.state().locked, true);
  await c.submit();
  assert.deepEqual(bodies, [
    { expected_version: 4, confirmed: true },
    { expected_version: 4, confirmed: true },
  ]);
  assert.equal(done, 1);
  assert.equal(c.state().locked, false);
});
test('conflict and denial end uncertainty without allowing a blind version retry', async () => {
  for (const code of ['conflict', 'access_denied'] as const) {
    let count = 0,
      denied = 0;
    const c = createSubscriptionDelete(
      {
        delete: async () => {
          count++;
          throw new ApiError(code);
        },
      },
      async () => 't',
      id,
      1,
      () => {},
      () => {},
      () => {
        denied++;
      },
    );
    await c.submit();
    await c.submit();
    assert.equal(count, 1);
    assert.equal(c.state().locked, false);
    assert.equal(c.state().error, code);
    assert.equal(denied, code === 'access_denied' ? 1 : 0);
  }
});
