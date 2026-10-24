import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ApiError, createAPI } from './api.ts';

test('public configuration omits cookies and authorization', async () => {
  const api = createAPI('https://api.example.test/', async (_url, init) => {
    assert.equal(_url, 'https://api.example.test/v1/public/config');
    assert.equal(init?.credentials, 'omit');
    assert.equal(new Headers(init?.headers).has('Authorization'), false);
    return Response.json({ configured: false, clerk_publishable_key: '' });
  });
  assert.deepEqual(await api.configuration(), {
    configured: false,
    clerk_publishable_key: '',
  });
});

test('each authenticated request gets a current bearer token and omits cookies', async () => {
  let tokenRequests = 0;
  const api = createAPI('https://api.example.test', async (_url, init) => {
    assert.equal(init?.credentials, 'omit');
    assert.equal(
      new Headers(init?.headers).get('Authorization'),
      `Bearer token-${tokenRequests}`,
    );
    return Response.json({ id: 'internal-user', role: 'user' });
  });
  const token = async () => `token-${++tokenRequests}`;
  await api.me(token);
  await api.me(token);
  assert.equal(tokenRequests, 2);
});

test('missing session token never sends an unauthenticated protected request', async () => {
  const api = createAPI('https://api.example.test', async () => {
    assert.fail('fetch must not be called');
  });
  await assert.rejects(
    api.me(async () => null),
    (error: unknown) =>
      error instanceof ApiError && error.code === 'authentication_required',
  );
});

for (const [status, code] of [
  [401, 'authentication_required'],
  [403, 'access_denied'],
  [503, 'dependency_unavailable'],
  [500, 'dependency_unavailable'],
] as const) {
  test(`HTTP ${status} is mapped safely without external error text`, async () => {
    const api = createAPI('https://api.example.test', async () =>
      Response.json(
        { message: 'sensitive upstream error', code: 'untrusted' },
        { status },
      ),
    );
    await assert.rejects(
      api.me(async () => 'token'),
      (error: unknown) => {
        assert.ok(error instanceof ApiError);
        assert.equal(error.code, code);
        assert.equal(error.message.includes('sensitive'), false);
        return true;
      },
    );
  });
}

test('administrator status is verified by the protected server endpoint', async () => {
  const api = createAPI('https://api.example.test', async (url, init) => {
    assert.equal(url, 'https://api.example.test/v1/admin/status');
    assert.equal(
      new Headers(init?.headers).get('Authorization'),
      'Bearer session',
    );
    return Response.json({ role: 'platform_admin' });
  });
  assert.deepEqual(await api.adminStatus(async () => 'session'), {
    role: 'platform_admin',
  });
});

test('network failures are safe dependency errors', async () => {
  const api = createAPI('https://api.example.test', async () => {
    throw new Error('private network address');
  });
  await assert.rejects(
    api.configuration(),
    (error: unknown) =>
      error instanceof ApiError &&
      error.code === 'dependency_unavailable' &&
      !error.message.includes('private'),
  );
});

test('malformed success payloads fail closed', async () => {
  for (const body of [
    {},
    { role: 'platform_admin' },
    { id: 'user', role: 'invented' },
  ]) {
    const api = createAPI('https://api.example.test', async () =>
      Response.json(body),
    );
    await assert.rejects(
      api.me(async () => 'session'),
      ApiError,
    );
  }
  const api = createAPI('https://api.example.test', async () =>
    Response.json({ role: 'user' }),
  );
  await assert.rejects(
    api.adminStatus(async () => 'session'),
    ApiError,
  );
});

test('cancellation is passed to fetch and remains distinguishable', async () => {
  const controller = new AbortController();
  controller.abort();
  const api = createAPI('https://api.example.test', async (_url, init) => {
    assert.equal(init?.signal, controller.signal);
    throw new DOMException('Aborted', 'AbortError');
  });
  await assert.rejects(api.configuration(controller.signal), {
    name: 'AbortError',
  });
});
