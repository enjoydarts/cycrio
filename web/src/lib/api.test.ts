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

const summaries = [
  'liquid',
  'openrouter',
  'openai',
  'anthropic',
  'resend',
  'clerk_webhook',
  'resend_webhook',
].map((provider) => ({
  provider,
  state: 'not_configured',
  version: 0,
  updated_at: null,
}));
const event = {
  id: 'event-id',
  actor_id: 'internal-admin',
  action: 'credential_created',
  provider: 'openai',
  version: 1,
  outcome: 'success',
  created_at: '2026-10-25T12:30:00Z',
};

test('credential requests use fresh bearer tokens and exact mutation bodies', async () => {
  let count = 0;
  const calls: { url: string; init: RequestInit | undefined }[] = [];
  const api = createAPI('https://api.test/', async (url, init) => {
    calls.push({ url: String(url), init });
    assert.equal(init?.credentials, 'omit');
    assert.equal(init?.cache, 'no-store');
    assert.equal(
      new Headers(init?.headers).get('Authorization'),
      `Bearer session-${count}`,
    );
    if (String(url).endsWith('/audit'))
      return Response.json({ events: [event] });
    if (init?.method === 'GET')
      return Response.json({ credentials: summaries });
    assert.equal(
      new Headers(init?.headers).get('Content-Type'),
      'application/json',
    );
    return Response.json({
      ...summaries[2],
      state: init?.method === 'PUT' ? 'configured' : 'disabled',
      version: 1,
      updated_at: event.created_at,
      secret: 'never retain me',
    });
  });
  const token = async () => `session-${++count}`;
  assert.deepEqual(await api.credentials(token), summaries);
  assert.deepEqual(await api.audit(token), [event]);
  const updated = await api.putCredential('openai', 'secret-123', 0, token);
  assert.equal('secret' in updated, false);
  await api.disableCredential('openai', 1, token);
  assert.equal(count, 4);
  assert.equal(calls[2].url, 'https://api.test/v1/admin/credentials/openai');
  assert.deepEqual(JSON.parse(String(calls[2].init?.body)), {
    secret: 'secret-123',
    expected_version: 0,
  });
  assert.equal(calls[3].init?.method, 'DELETE');
  assert.deepEqual(JSON.parse(String(calls[3].init?.body)), {
    expected_version: 1,
  });
});

test('credential metadata and audit reject malformed payloads and discard extra fields', async () => {
  for (const body of [
    { credentials: summaries.slice(1) },
    { credentials: [...summaries.slice(0, 4), summaries[0]] },
    ...[
      { provider: 'other' },
      { state: 'invented' },
      { version: -1 },
      { version: 1.5 },
      { version: Number.MAX_SAFE_INTEGER + 1 },
      { updated_at: 'yesterday' },
      { updated_at: '2026-02-30T00:00:00Z' },
      { state: 'configured' },
    ].map((change) => ({
      credentials: [{ ...summaries[0], ...change }, ...summaries.slice(1)],
    })),
  ]) {
    const api = createAPI('', async () => Response.json(body));
    await assert.rejects(
      api.credentials(async () => 'token'),
      ApiError,
    );
  }
  for (const change of [
    { id: 0 },
    { id: Number.MAX_SAFE_INTEGER + 1 },
    { actor_id: '' },
    { action: 'unknown' },
    { outcome: 'failed' },
    { provider: 'other' },
    { version: 0 },
    { created_at: null },
  ]) {
    const api = createAPI('', async () =>
      Response.json({ events: [{ ...event, ...change }] }),
    );
    await assert.rejects(
      api.audit(async () => 'token'),
      ApiError,
    );
  }
  const api = createAPI('', async (url) =>
    Response.json(
      String(url).endsWith('/audit')
        ? { events: [{ ...event, ciphertext: 'private' }] }
        : {
            credentials: summaries.map((row) => ({
              ...row,
              secret: 'private',
            })),
          },
    ),
  );
  assert.deepEqual(await api.credentials(async () => 'token'), summaries);
  assert.deepEqual(await api.audit(async () => 'token'), [event]);
});

test('RFC3339 timestamps reject normalized dates and invalid clock values', async () => {
  for (const created_at of [
    '2026-10-25T24:00:00Z',
    '2026-02-30T00:00:00Z',
    '2026-10-25T12:60:00Z',
  ]) {
    const api = createAPI('', async () =>
      Response.json({ events: [{ ...event, created_at }] }),
    );
    await assert.rejects(
      api.audit(async () => 'token'),
      ApiError,
    );
  }
});

test('credential state accepts enum strings only without coercing arrays', async () => {
  for (const state of [
    ['configured'],
    { value: 'configured' },
    null,
    1,
    true,
  ]) {
    const api = createAPI('', async () =>
      Response.json({
        credentials: [
          { ...summaries[0], state, version: 1, updated_at: event.created_at },
          ...summaries.slice(1),
        ],
      }),
    );
    await assert.rejects(
      api.credentials(async () => 'token'),
      ApiError,
    );
  }
});

test('audit action accepts enum strings only without coercing arrays', async () => {
  for (const action of [
    ['credential_created'],
    { value: 'credential_created' },
    null,
    1,
    true,
  ]) {
    const api = createAPI('', async () =>
      Response.json({ events: [{ ...event, action }] }),
    );
    await assert.rejects(
      api.audit(async () => 'token'),
      ApiError,
    );
  }
});

test('provider enums reject nonstring metadata and audit values', async () => {
  for (const provider of [['liquid'], { value: 'liquid' }, null, 1, true]) {
    const api = createAPI('', async (url) =>
      Response.json(
        String(url).endsWith('/audit')
          ? { events: [{ ...event, provider }] }
          : {
              credentials: [
                { ...summaries[0], provider },
                ...summaries.slice(1),
              ],
            },
      ),
    );
    await assert.rejects(
      api.credentials(async () => 'token'),
      ApiError,
    );
    await assert.rejects(
      api.audit(async () => 'token'),
      ApiError,
    );
  }
});

for (const [status, code] of [
  [400, 'invalid_request'],
  [404, 'not_found'],
  [409, 'conflict'],
] as const) {
  test(`credential HTTP ${status} maps to a safe fixed code`, async () => {
    const api = createAPI('', async () =>
      Response.json({ secret: 'private' }, { status }),
    );
    await assert.rejects(
      api.credentials(async () => 'token'),
      (error: unknown) =>
        error instanceof ApiError &&
        error.code === code &&
        !error.message.includes('private'),
    );
  });
}
