import assert from 'node:assert/strict';
import test from 'node:test';
import { createErasureAPI } from './erasure-api.ts';
const id = '10000000-0000-4000-8000-000000000001';
test('withdrawal uses only explicit confirmation and checks the receipt nonce', async () => {
  const calls: any[] = [];
  const api = createErasureAPI('/api/', async (url, options) => {
    calls.push({ url, ...options });
    return Response.json({ request_id: id, status: 'pending' });
  });
  await api.request(
    { request_id: id, confirmed: true, confirmation: '退会する' },
    async () => 't',
  );
  await api.status(id, async () => 't');
  assert.equal(calls[0].url, '/api/v1/me/withdrawal');
  assert.deepEqual(JSON.parse(calls[0].body), {
    request_id: id,
    confirmed: true,
    confirmation: '退会する',
  });
  assert.equal(calls[1].method, 'GET');
  assert.equal(calls[1].url, `/api/v1/me/withdrawal?request_id=${id}`);
  assert.equal(calls[0].cache, 'no-store');
  for (const patch of [
    { confirmed: false },
    { confirmation: '退会する ' },
    { request_id: 'bad' },
  ])
    await assert.rejects(
      api.request(
        {
          request_id: id,
          confirmed: true,
          confirmation: '退会する',
          ...patch,
        } as never,
        async () => 't',
      ),
      { code: 'invalid_request' },
    );
  assert.equal(calls.length, 2);
  const wrong = createErasureAPI('', async () =>
    Response.json({
      request_id: '20000000-0000-4000-8000-000000000001',
      status: 'completed',
    }),
  );
  await assert.rejects(
    wrong.status(id, async () => 't'),
    { code: 'dependency_unavailable' },
  );
});
