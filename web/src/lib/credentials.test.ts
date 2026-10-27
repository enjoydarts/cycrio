import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createAPI } from './api.ts';
import {
  createCredentialsController,
  validSecret,
  type CredentialsState,
} from './credentials.ts';

const rows = [
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
function fixture(
  handle?: (url: string, init?: RequestInit) => Promise<Response>,
) {
  const states: CredentialsState[] = [];
  let denied = 0;
  const calls: string[] = [];
  const controller = createCredentialsController(
    createAPI('', async (url, init) => {
      calls.push(`${init?.method} ${url}`);
      if (handle) return handle(String(url), init);
      return Response.json(
        String(url).endsWith('/audit') ? { events: [] } : { credentials: rows },
      );
    }),
    async () => 'current-session',
    (state) => states.push(state),
    () => {
      denied++;
    },
  );
  return {
    controller,
    states,
    calls,
    get denied() {
      return denied;
    },
  };
}

test('secret validation accepts only 8..4096 visible ASCII characters', () => {
  for (const value of ['12345678', '!'.repeat(4096)])
    assert.equal(validSecret(value), true);
  for (const value of [
    '',
    '1234567',
    '!'.repeat(4097),
    'abcdefgh ',
    'abcdefgh\n',
    'abcdefあh',
    'abcd\u007fefgh',
  ])
    assert.equal(validSecret(value), false);
});

test('load publishes metadata and audit only, without storing secret inputs', async () => {
  const f = fixture();
  await f.controller.load();
  assert.equal(f.controller.state.status, 'ready');
  assert.equal(f.controller.state.credentials.length, 7);
  assert.deepEqual(f.controller.state.events, []);
  await f.controller.put('openai', 'invalid', 0);
  assert.equal(f.controller.state.error, 'invalid_request');
  assert.equal(f.calls.length, 2);
  assert.equal(JSON.stringify(f.states).includes('invalid"'), false);
});

test('saving prevents concurrent requests and only sends secret in mutation body', async () => {
  let release!: (value: Response) => void;
  const f = fixture(async (url, init) => {
    if (init?.method === 'PUT') {
      assert.deepEqual(JSON.parse(String(init.body)), {
        secret: 'private-secret',
        expected_version: 0,
      });
      return new Promise<Response>((resolve) => {
        release = resolve;
      });
    }
    return Response.json(
      url.endsWith('/audit') ? { events: [] } : { credentials: rows },
    );
  });
  await f.controller.load();
  const running = f.controller.put('openai', 'private-secret', 0);
  await new Promise((resolve) => setTimeout(resolve, 0));
  await f.controller.put('openai', 'other-secret', 0);
  await f.controller.disable('openai', 0);
  await f.controller.load();
  assert.equal(f.calls.length, 3);
  assert.equal(f.controller.state.status, 'saving');
  release(
    Response.json({
      provider: 'openai',
      state: 'configured',
      version: 1,
      updated_at: '2026-10-25T00:00:00Z',
    }),
  );
  await running;
  assert.equal(f.controller.state.status, 'ready');
  assert.equal(f.controller.state.success, 'saved');
  assert.equal(JSON.stringify(f.states).includes('private-secret'), false);
});

test('conflict reloads current metadata and requires a new explicit submit', async () => {
  const f = fixture(async (url, init) =>
    Response.json(
      init?.method === 'PUT'
        ? {}
        : url.endsWith('/audit')
          ? { events: [] }
          : { credentials: rows },
      init?.method === 'PUT' ? { status: 409 } : undefined,
    ),
  );
  await f.controller.load();
  await f.controller.put('openai', 'private-secret', 0);
  assert.equal(f.controller.state.error, 'conflict');
  assert.equal(f.controller.state.credentials.length, 7);
  assert.equal(f.calls.filter((call) => call.startsWith('PUT')).length, 1);
  assert.equal(f.calls.length, 5);
});

for (const status of [401, 403]) {
  test(`HTTP ${status} clears management data and refreshes parent authorization`, async () => {
    let fail = false;
    const f = fixture(async (url) =>
      fail
        ? new Response(null, { status })
        : Response.json(
            url.endsWith('/audit') ? { events: [] } : { credentials: rows },
          ),
    );
    await f.controller.load();
    fail = true;
    await f.controller.put('openai', 'private-secret', 0);
    assert.equal(f.controller.state.status, 'denied');
    assert.deepEqual(f.controller.state.credentials, []);
    assert.deepEqual(f.controller.state.events, []);
    assert.equal(f.denied, 1);
    const count = f.calls.length;
    await f.controller.load();
    assert.equal(f.calls.length, count);
  });
}

test('dispose aborts work, clears metadata, and rejects late responses', async () => {
  let release!: (value: Response) => void;
  let signal: AbortSignal | undefined;
  let delay = false;
  const f = fixture(async (url, init) => {
    if (delay && !url.endsWith('/audit')) {
      signal = init?.signal ?? undefined;
      return new Promise((resolve) => {
        release = resolve;
      });
    }
    return Response.json(
      url.endsWith('/audit') ? { events: [] } : { credentials: rows },
    );
  });
  await f.controller.load();
  delay = true;
  const running = f.controller.load();
  await new Promise((resolve) => setTimeout(resolve, 0));
  f.controller.dispose();
  assert.equal(signal?.aborted, true);
  const count = f.states.length;
  release(Response.json({ credentials: rows }));
  await running;
  assert.deepEqual(f.controller.state.credentials, []);
  assert.equal(f.states.length, count);
});

test('configured credential disable reloads summaries and reports completion', async () => {
  const f = fixture(async (url, init) => {
    if (init?.method === 'DELETE')
      return Response.json({
        provider: 'openai',
        state: 'disabled',
        version: 2,
        updated_at: '2026-10-25T00:00:00Z',
      });
    return Response.json(
      url.endsWith('/audit')
        ? { events: [] }
        : {
            credentials: rows.map((row) =>
              row.provider === 'openai'
                ? {
                    ...row,
                    state: 'configured',
                    version: 1,
                    updated_at: '2026-10-25T00:00:00Z',
                  }
                : row,
            ),
          },
    );
  });
  await f.controller.load();
  await f.controller.disable('openai', 1);
  assert.equal(f.controller.state.success, 'disabled');
  assert.equal(f.calls.filter((call) => call.startsWith('DELETE')).length, 1);
});

test('cannot disable an unconfigured credential or announce a previous save as disable', async () => {
  const f = fixture(async (url, init) =>
    Response.json(
      init?.method === 'PUT'
        ? {
            provider: 'openai',
            state: 'configured',
            version: 1,
            updated_at: '2026-10-25T00:00:00Z',
          }
        : url.endsWith('/audit')
          ? { events: [] }
          : { credentials: rows },
    ),
  );
  await f.controller.load();
  await f.controller.put('openai', 'private-secret', 0);
  await f.controller.disable('resend', 0);
  assert.equal(f.controller.state.success, 'saved');
  assert.equal(
    f.calls.some((call) => call.startsWith('DELETE')),
    false,
  );
});
