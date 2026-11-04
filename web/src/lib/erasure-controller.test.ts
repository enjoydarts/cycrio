import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createErasureController } from './erasure-controller.ts';
const id = '10000000-0000-4000-8000-000000000001';
test('withdrawal requires both confirmations, sends once, and purges the session on receipt', async () => {
  let posts = 0,
    signedOut = 0,
    nonces = 0;
  const c = createErasureController(
    {
      request: async (body) => {
        posts++;
        return { request_id: body.request_id, status: 'pending' };
      },
      status: async () => {
        throw Error('Unexpected GET');
      },
    },
    async () => 't',
    () => {},
    () => {
      signedOut++;
    },
    () => {
      nonces++;
      return id;
    },
  );
  await c.submit();
  c.confirm('退会する ', true);
  await c.submit();
  c.confirm('退会する', false);
  await c.submit();
  assert.equal(posts, 0);
  c.confirm('退会する', true);
  await Promise.all([c.submit(), c.submit()]);
  await c.submit();
  assert.equal(posts, 1);
  assert.equal(nonces, 1);
  assert.equal(signedOut, 1);
});
test('uncertain delivery reconciles with GET and never repeats POST even after not_found', async () => {
  let posts = 0,
    gets = 0,
    signedOut = 0;
  const c = createErasureController(
    {
      request: async () => {
        posts++;
        throw new ApiError('dependency_unavailable');
      },
      status: async (requestID) => {
        gets++;
        assert.equal(requestID, id);
        if (gets === 1) throw new ApiError('not_found');
        return { request_id: id, status: 'completed' };
      },
    },
    async () => 't',
    () => {},
    () => {
      signedOut++;
    },
    () => id,
  );
  c.confirm('退会する', true);
  await c.submit();
  await c.submit();
  assert.equal(c.state().uncertain, true);
  await c.reconcile();
  await c.submit();
  assert.equal(c.state().uncertain, true);
  await c.reconcile();
  assert.deepEqual([posts, gets, signedOut], [1, 2, 1]);
});
test('late responses after disposal never publish or sign out a new owner', async () => {
  let resolve!: (value: any) => void,
    signedOut = 0,
    changed = 0;
  const c = createErasureController(
    {
      request: () =>
        new Promise((r) => {
          resolve = r;
        }),
      status: async () => ({ request_id: id, status: 'pending' }),
    },
    async () => 't',
    () => {
      changed++;
    },
    () => {
      signedOut++;
    },
    () => id,
  );
  c.confirm('退会する', true);
  const pending = c.submit();
  c.dispose();
  const before = changed;
  resolve({ request_id: id, status: 'pending' });
  await pending;
  assert.equal(changed, before);
  assert.equal(signedOut, 0);
});
