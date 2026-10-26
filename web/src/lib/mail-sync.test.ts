import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { account, ids } from './mail-test-fixtures.ts';
import {
  createMailSyncAPI,
  validSyncRange,
  type MailSyncAPI,
  type SyncRun,
} from './mail-sync-api.ts';
import { createMailSyncController } from './mail-sync-controller.ts';
export const run = (patch: Partial<SyncRun> = {}): SyncRun => ({
  id: ids.probe,
  account_id: ids.account,
  state: 'queued',
  since: '2026-10-01',
  before: '2026-10-02',
  max_messages: 20,
  examined_count: 0,
  added_count: 0,
  folder_count: 1,
  completed_folders: 0,
  error_code: '',
  created_at: '2026-10-25T00:00:00Z',
  started_at: null,
  finished_at: null,
  ...patch,
});
const ready = () =>
  account({
    state: 'ready',
    folders: [
      {
        id: ids.folder,
        name: 'INBOX',
        delimiter: '/',
        available: true,
        selectable: true,
        selected: true,
      },
    ],
  });
function setup(overrides: Partial<MailSyncAPI> = {}) {
  let denied = 0,
    refreshes = 0,
    uuid = 0;
  const api: MailSyncAPI = {
    list: async () => [],
    start: async () => run(),
    cancel: async () => run({ state: 'cancelled' }),
    ...overrides,
  };
  const c = createMailSyncController(
    api,
    async () => 'session',
    () => {},
    () => denied++,
    () => {
      refreshes++;
    },
    () => `30000000-0000-4000-8000-${String(++uuid).padStart(12, '0')}`,
  );
  c.setAccount(ready());
  const edit = () =>
    c.edit({ since: '2026-10-01', before: '2026-10-02', max_messages: '20' });
  return { c, edit, denied: () => denied, refreshes: () => refreshes };
}
test('range validates actual calendar dates, exclusive upper boundary, leap days and count limits', () => {
  for (const [since, before, max] of [
    ['2024-02-29', '2025-03-01', '1000'],
    ['1900-01-01', '1900-01-02', '1'],
  ])
    assert.equal(validSyncRange(since, before, max), true);
  for (const [since, before, max] of [
    ['2026-02-29', '2026-03-01', '1'],
    ['1899-12-31', '1900-01-01', '1'],
    ['2026-01-01', '2026-01-01', '1'],
    ['2025-01-01', '2026-01-03', '1'],
    ['2026-01-01', '2026-01-02', '0'],
    ['2026-01-01', '2026-01-02', '1.2'],
    ['2026-01-01', '2026-01-02', '1001'],
    ['', '', ''],
  ])
    assert.equal(validSyncRange(since, before, max), false);
});
test('API uses owner routes/auth/no-store/abort and preserves nullable DTO timestamps', async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  const api = createMailSyncAPI('https://example.test/', async (url, init) => {
    calls.push({ url: String(url), init: init! });
    return Response.json(calls.length === 1 ? { runs: [run()] } : run());
  });
  const signal = new AbortController().signal;
  assert.equal(
    (await api.list(ids.account, async () => 'token', signal))[0].started_at,
    null,
  );
  await api.start(
    ids.account,
    {
      expected_version: 1,
      request_id: ids.request,
      since: '2026-10-01',
      before: '2026-10-02',
      max_messages: 20,
    },
    async () => 'token',
    signal,
  );
  await api.cancel(ids.account, ids.probe, async () => 'token', signal);
  assert.equal(
    calls[0].url,
    `https://example.test/v1/mail/accounts/${ids.account}/sync-runs`,
  );
  for (const { init } of calls) {
    assert.equal(init.cache, 'no-store');
    assert.equal(init.signal, signal);
    assert.equal(
      new Headers(init.headers).get('Authorization'),
      'Bearer token',
    );
    assert.equal(new Headers(init.headers).has('Origin'), false);
  }
  assert.equal(calls[2].init.body, '{}');
});
test('API rejects malformed, cross-owner and duplicate history without displaying raw data', async () => {
  for (const patch of [
    { account_id: ids.other },
    { id: 'bad' },
    { state: 'succeeded' },
    { since: '2026-02-29' },
    { since: ['2026-10-01'] },
    { started_at: undefined },
    { examined_count: 21 },
    { added_count: 1 },
    { folder_count: 0 },
    { completed_folders: 2 },
    { error_code: 'secret' },
    { created_at: 'bad' },
    { max_messages: 1001 },
  ]) {
    const api = createMailSyncAPI('', async () =>
      Response.json({ runs: [run(patch as Partial<SyncRun>)] }),
    );
    await assert.rejects(
      api.list(ids.account, async () => 'token'),
      (e: unknown) =>
        e instanceof ApiError && e.code === 'dependency_unavailable',
    );
  }
  for (const runs of [[run(), run()], Array.from({ length: 21 }, () => run())])
    await assert.rejects(
      createMailSyncAPI('', async () => Response.json({ runs })).list(
        ids.account,
        async () => 'token',
      ),
    );
});
test('sync authorization failure refreshes account metadata after a newly observed terminal history result', async () => {
  let terminal = false;
  const { c, edit, refreshes } = setup({
    list: async () => [
      run(
        terminal
          ? { state: 'failed', error_code: 'authentication_failed' }
          : {},
      ),
    ],
  });
  edit();
  await c.start();
  terminal = true;
  await c.poll();
  assert.equal(refreshes(), 1);
  await c.refresh();
  assert.equal(refreshes(), 1);
});
test('API cancellation response must match the requested run and queued resumptions retain progress', async () => {
  const resumed = run({
    started_at: '2026-10-25T00:01:00Z',
    examined_count: 1,
    added_count: 1,
  });
  assert.equal(
    (
      await createMailSyncAPI('', async () =>
        Response.json({ runs: [resumed] }),
      ).list(ids.account, async () => 'token')
    )[0].examined_count,
    1,
  );
  await assert.rejects(
    createMailSyncAPI('', async () =>
      Response.json(run({ id: ids.other })),
    ).cancel(ids.account, ids.probe, async () => 'token'),
  );
});
test('switching away and remounting preserves an uncertain request UUID without exposing other owner data', async () => {
  const requests = new Map();
  const inputs: string[] = [];
  const api: MailSyncAPI = {
    list: async () => [],
    cancel: async () => run(),
    start: async (_id, input) => {
      inputs.push(input.request_id);
      throw new ApiError('dependency_unavailable');
    },
  };
  const create = () =>
    createMailSyncController(
      api,
      async () => 'token',
      () => {},
      () => {},
      () => {},
      () => ids.request,
      requests,
    );
  const first = create();
  first.setAccount(ready());
  first.edit({ since: '2026-10-01', before: '2026-10-02', max_messages: '20' });
  await first.start();
  first.dispose();
  const second = create();
  second.setAccount({ ...ready(), id: ids.other });
  assert.equal(second.state().uncertain, false);
  second.setAccount(ready());
  assert.equal(second.state().uncertain, true);
  assert.equal(second.state().form.since, '2026-10-01');
  await second.start();
  assert.equal(inputs.length, 1);
  await second.retry();
  assert.deepEqual(inputs, [ids.request, ids.request]);
});
test('load never starts and input remains blank; dirty baseline survives account updates until explicit discard', async () => {
  let posts = 0;
  const { c, edit } = setup({
    start: async () => {
      posts++;
      return run();
    },
  });
  await c.refresh();
  await c.poll();
  assert.equal(posts, 0);
  assert.equal(c.state().form.since, '');
  edit();
  c.setAccount({ ...ready(), version: 2 });
  assert.equal(c.state().form.expected_version, 1);
  assert.equal(c.state().conflict, true);
  c.discard();
  assert.equal(c.state().form.expected_version, 2);
  assert.equal(c.state().form.since, '');
});
test('uncertain POST is never automatic; explicit retry reuses identical request and terminal receipt refreshes history', async () => {
  const inputs: unknown[] = [];
  let lists = 0;
  const { c, edit } = setup({
    list: async () => {
      lists++;
      return [run({ state: 'completed' })];
    },
    start: async (_id, input) => {
      inputs.push(input);
      if (inputs.length === 1) throw new ApiError('dependency_unavailable');
      return run({ state: 'completed' });
    },
  });
  edit();
  await c.start();
  assert.equal(c.state().uncertain, true);
  await c.poll();
  await c.start();
  assert.equal(inputs.length, 1);
  await c.retry();
  assert.deepEqual(inputs[0], inputs[1]);
  assert.equal(c.state().uncertain, false);
  assert.ok(lists >= 2);
  assert.equal(c.state().runs.length, 1);
});
test('start gates ready selected folders and valid inputs, deduplicates concurrent mutations', async () => {
  let posts = 0,
    finish!: (r: SyncRun) => void;
  const { c, edit } = setup({
    start: async () => {
      posts++;
      return new Promise((resolve) => (finish = resolve));
    },
  });
  await c.start();
  assert.equal(posts, 0);
  edit();
  c.setAccount({ ...ready(), state: 'needs_reauth' });
  await c.start();
  assert.equal(posts, 0);
  c.setAccount(ready());
  const first = c.start();
  await c.start();
  await c.refresh();
  await c.poll();
  assert.equal(posts, 1);
  finish(run());
  await first;
});
test('account switch aborts requests and prevents old owner results; disposal only aborts HTTP', async () => {
  let signal: AbortSignal | undefined,
    finish!: (r: SyncRun[]) => void,
    cancels = 0;
  const { c } = setup({
    list: async (_id, _t, s) => {
      signal = s;
      return new Promise((resolve) => (finish = resolve));
    },
    cancel: async () => {
      cancels++;
      return run();
    },
  });
  const loading = c.refresh();
  c.setAccount({ ...ready(), id: ids.other });
  assert.equal(signal?.aborted, true);
  assert.deepEqual(c.state().runs, []);
  finish([run()]);
  await loading;
  assert.deepEqual(c.state().runs, []);
  c.dispose();
  assert.equal(cancels, 0);
});
test('409 keeps form and baseline and requests account refresh; denial on history clears all owner data', async () => {
  const { c, edit, denied, refreshes } = setup({
    start: async () => {
      throw new ApiError('conflict');
    },
    list: async () => {
      throw new ApiError('access_denied');
    },
  });
  edit();
  await c.start();
  assert.equal(c.state().form.since, '2026-10-01');
  assert.equal(c.state().form.expected_version, 1);
  assert.equal(refreshes(), 1);
  assert.equal(c.state().conflict, true);
  await c.refresh();
  assert.equal(denied(), 1);
  assert.equal(c.state().account, null);
  assert.deepEqual(c.state().runs, []);
  assert.equal(c.state().form.since, '');
});
test('manual GET is not overlapped by polling; stopping an owned run aborts GET without leaving loading stuck', async () => {
  let lists = 0,
    signal: AbortSignal | undefined,
    finish!: (r: SyncRun[]) => void;
  const { c, edit } = setup({
    list: async (_id, _t, s) => {
      lists++;
      signal = s;
      return new Promise((resolve) => (finish = resolve));
    },
  });
  edit();
  await c.start();
  const loading = c.refresh();
  await c.poll();
  assert.equal(lists, 1);
  await c.cancel(ids.probe);
  assert.equal(signal?.aborted, true);
  assert.equal(c.state().status, 'ready');
  finish([run()]);
  await loading;
  assert.equal(c.state().runs[0].state, 'cancelled');
});
