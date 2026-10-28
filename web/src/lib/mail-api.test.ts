import assert from 'node:assert/strict';
import test from 'node:test';
import { createMailAPI } from './mail-api.ts';
import { ApiError } from './api.ts';
import { account, probe, ids } from './mail-test-fixtures.ts';

test('owner API uses authenticated no-store requests and strips private extra response fields', async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  const api = createMailAPI('https://api.test/', async (url, init) => {
    calls.push({ url: String(url), init: init! });
    return Response.json({
      accounts: [{ ...account(), login: 'private', app_password: 'private' }],
    });
  });
  const result = await api.list(async () => 'session');
  assert.equal(calls[0].url, 'https://api.test/v1/mail/accounts');
  assert.equal(
    new Headers(calls[0].init.headers).get('Authorization'),
    'Bearer session',
  );
  assert.equal(calls[0].init.cache, 'no-store');
  assert.equal(calls[0].init.credentials, 'omit');
  assert.equal('login' in result[0], false);
});
test('save and probe send only fixed owner API bodies', async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  const api = createMailAPI('', async (url, init) => {
    calls.push({ url: String(url), init: init! });
    return Response.json(String(url).endsWith('/probes') ? probe() : account());
  });
  await api.save(
    ids.account,
    {
      expected_version: 0,
      label: 'メール',
      login: 'me@example.com',
      app_password: 'password',
    },
    async () => 's',
  );
  await api.requestProbe(
    ids.account,
    { expected_version: 1, request_id: ids.request },
    async () => 's',
  );
  assert.equal(calls[0].init.method, 'PUT');
  assert.deepEqual(JSON.parse(String(calls[1].init.body)), {
    expected_version: 1,
    request_id: ids.request,
  });
  assert.equal(calls[1].url, `/v1/mail/accounts/${ids.account}/probes`);
});
test('retained unavailable folder history may exceed the remote LIST limit', async () => {
  const folders = Array.from({ length: 1001 }, (_, i) => ({
    id: `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`,
    name: String(i),
    delimiter: '/',
    selectable: true,
    available: false,
    selected: false,
  }));
  const api = createMailAPI('', async () =>
    Response.json({ accounts: [account({ folders })] }),
  );
  assert.equal((await api.list(async () => 's'))[0].folders.length, 1001);
});

test('folder display names survive parsing while provider IDs remain unchanged', async () => {
  const folder = {
    id: ids.folder,
    name: 'Label_123456',
    display_name: '請求メール',
    delimiter: '',
    selectable: true,
    available: true,
    selected: false,
  };
  const api = createMailAPI('', async () =>
    Response.json({
      accounts: [account({ provider: 'gmail', folders: [folder] })],
    }),
  );
  const result = (await api.list(async () => 's'))[0].folders[0];
  assert.equal(result.name, 'Label_123456');
  assert.equal(result.display_name, '請求メール');
  for (const bad of [null, 123, ['請求メール']]) {
    const malformed = createMailAPI('', async () =>
      Response.json({
        accounts: [
          account({ folders: [{ ...folder, display_name: bad } as never] }),
        ],
      }),
    );
    await assert.rejects(
      malformed.list(async () => 's'),
      ApiError,
    );
  }
});
test('malformed data and unsafe upstream errors produce only safe application errors', async () => {
  for (const body of [
    { accounts: [account({ version: 0 })] },
    { accounts: [{ ...account(), provider: ['gmail'] }] },
    { accounts: [{ ...account(), last_error: 'secret server reply' }] },
    { accounts: [account({ latest_probe: probe({ account_id: ids.other }) })] },
  ]) {
    const api = createMailAPI('', async () => Response.json(body));
    await assert.rejects(
      api.list(async () => 's'),
      (e: unknown) =>
        e instanceof ApiError && e.code === 'dependency_unavailable',
    );
  }
  for (const [status, code] of [
    [401, 'authentication_required'],
    [403, 'access_denied'],
    [404, 'not_found'],
    [409, 'conflict'],
    [400, 'invalid_request'],
    [500, 'dependency_unavailable'],
  ] as const) {
    const api = createMailAPI(
      '',
      async () => new Response('secret', { status }),
    );
    await assert.rejects(
      api.list(async () => 's'),
      (e: unknown) => e instanceof ApiError && e.message === code,
    );
  }
});

test('disconnect, folder selection and probe polling use exact owner paths and bodies', async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  const api = createMailAPI('', async (url, init) => {
    calls.push({ url: String(url), init: init! });
    return Response.json(
      String(url).includes(`/probes/${ids.probe}`) ? probe() : account(),
    );
  });
  await api.disconnect(ids.account, 4, async () => 's');
  await api.saveFolders(ids.account, 5, [], async () => 's');
  await api.probe(ids.account, ids.probe, async () => 's');
  assert.equal(calls[0].init.method, 'POST');
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), {
    expected_version: 4,
  });
  assert.equal(calls[1].init.method, 'PUT');
  assert.deepEqual(JSON.parse(String(calls[1].init.body)), {
    expected_version: 5,
    folder_ids: [],
  });
  assert.equal(calls[2].init.method, 'GET');
  assert.equal(calls[2].init.body, undefined);
  assert.equal(
    calls[2].url,
    `/v1/mail/accounts/${ids.account}/probes/${ids.probe}`,
  );
});
test('missing tokens and cancellation during token lookup never send a private request', async () => {
  let calls = 0;
  const api = createMailAPI('', async () => {
    calls++;
    return Response.json({ accounts: [] });
  });
  await assert.rejects(
    api.list(async () => null),
    (e: unknown) =>
      e instanceof ApiError && e.code === 'authentication_required',
  );
  const abort = new AbortController();
  await assert.rejects(
    api.list(async () => {
      abort.abort();
      return 'session';
    }, abort.signal),
    (e: unknown) => e instanceof DOMException && e.name === 'AbortError',
  );
  assert.equal(calls, 0);
});

test('mail API accepts all supported providers and preserves only IMAP endpoint metadata', async () => {
  const api = createMailAPI('', async () =>
    Response.json({
      accounts: ['fastmail', 'imap', 'gmail', 'outlook'].map((provider, i) => ({
        ...account(),
        id: `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`,
        provider,
        ...(provider === 'imap' ? { host: 'imap.example.com', port: 143 } : {}),
      })),
    }),
  );
  const rows = await api.list(async () => 's');
  assert.deepEqual(
    rows.map((row) => row.provider),
    ['fastmail', 'imap', 'gmail', 'outlook'],
  );
  assert.equal(rows[1].host, 'imap.example.com');
  assert.equal(rows[1].port, 143);
});

test('OAuth start alone receives cookies and only official authorization URLs are accepted', async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  let authorization =
    'https://accounts.google.com/o/oauth2/v2/auth?state=random';
  const api = createMailAPI('https://api.test', async (url, init) => {
    calls.push({ url: String(url), init: init! });
    return Response.json(
      String(url).endsWith('/providers')
        ? {
            providers: [
              { provider: 'gmail', ready: true },
              { provider: 'outlook', ready: false },
            ],
          }
        : { authorization_url: authorization },
    );
  });
  assert.deepEqual(await api.oauthProviders!(async () => 's'), [
    { provider: 'gmail', ready: true },
    { provider: 'outlook', ready: false },
  ]);
  assert.equal(
    await api.startOAuth!(
      ids.account,
      { provider: 'gmail', label: '個人', expected_version: 0 },
      async () => 's',
    ),
    authorization,
  );
  assert.equal(calls[0].url, 'https://api.test/v1/mail/oauth/providers');
  assert.equal(calls[0].init.credentials, 'omit');
  assert.equal(
    calls[1].url,
    `https://api.test/v1/mail/accounts/${ids.account}/oauth`,
  );
  assert.equal(calls[1].init.credentials, 'include');
  assert.deepEqual(JSON.parse(String(calls[1].init.body)), {
    provider: 'gmail',
    label: '個人',
    expected_version: 0,
  });
  for (const bad of [
    'https://accounts.google.com.evil.test/o/oauth2/v2/auth',
    'http://accounts.google.com/o/oauth2/v2/auth',
    'https://user@accounts.google.com/o/oauth2/v2/auth',
    'https://login.microsoftonline.com/common/oauth2/v2.0/authorize',
    'https://accounts.google.com/other',
  ]) {
    authorization = bad;
    await assert.rejects(
      api.startOAuth!(
        ids.account,
        { provider: 'gmail', label: '個人', expected_version: 0 },
        async () => 's',
      ),
      ApiError,
    );
  }
});

test('generic IMAP save sends endpoint fields and OAuth providers cannot use password save', async () => {
  let sent: unknown;
  const api = createMailAPI('', async (_url, init) => {
    sent = JSON.parse(String(init?.body));
    return Response.json({
      ...account(),
      provider: 'imap',
      host: 'imap.example.com',
      port: 993,
    });
  });
  await api.save(
    ids.account,
    {
      provider: 'imap',
      host: 'imap.example.com',
      port: 993,
      label: 'IMAP',
      login: 'owner@example.com',
      app_password: 'password',
      expected_version: 0,
    },
    async () => 's',
  );
  assert.deepEqual(sent, {
    provider: 'imap',
    host: 'imap.example.com',
    port: 993,
    label: 'IMAP',
    login: 'owner@example.com',
    app_password: 'password',
    expected_version: 0,
  });
  await assert.rejects(
    api.save(
      ids.account,
      {
        provider: 'gmail',
        label: 'OAuth',
        login: '',
        app_password: '',
        expected_version: 0,
      } as never,
      async () => 's',
    ),
    ApiError,
  );
});
