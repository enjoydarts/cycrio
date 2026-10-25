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
test('malformed data and unsafe upstream errors produce only safe application errors', async () => {
  for (const body of [
    { accounts: [account({ version: 0 })] },
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
