import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createMessageAPI,
  parseMessage,
  stageStates,
} from './analysis-messages-api.ts';
import {
  createMessageHistory,
  pollMessageHistory,
} from './analysis-messages-controller.ts';
import { ApiError } from './api.ts';
import { messageName } from './analysis-messages-text.ts';
import { processingResult, processingIDs } from './mail-processing-fixtures.ts';
const id = '11111111-1111-4111-8111-111111111111';
const row = {
  id,
  account_id: id,
  account_label: 'メール',
  discovered_at: '2026-10-29T00:00:00Z',
  primary: 'completed',
  secondary: 'waiting',
  registration: 'not_started',
  result_id: null,
  service: null,
  amount: null,
  currency: null,
  event_type: null,
  error_code: '',
};
const counts = Object.fromEntries(
  [
    'not_started',
    'waiting',
    'running',
    'completed',
    'skipped',
    'needs_review',
    'failed',
    'unknown',
    'cancelled',
  ].map((s) => [s, 0]),
);
const page = {
  messages: [row],
  total: 81,
  limit: 20,
  offset: 40,
  summary: {
    discovered: 81,
    primary: counts,
    secondary: counts,
    registration: counts,
  },
};
test('failure before AI invocation describes mail retrieval without claiming primary failure', () => {
  const pending = {
    ...row,
    primary: 'not_started',
    secondary: 'not_started',
  } as Parameters<typeof messageName>[0];
  assert.equal(
    messageName({ ...pending, error_code: 'source_missing' }),
    'メールを取得できませんでした',
  );
  assert.equal(
    messageName({ ...pending, error_code: 'input_too_large' }),
    '処理できるサイズを超えたメール',
  );
  assert.equal(messageName(pending), '判定待ちのメール');
});
test('poll refreshes every five seconds, pauses while busy, and stops on cleanup', (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });
  let calls = 0,
    ready = true;
  const stop = pollMessageHistory(
    () => calls++,
    () => ready,
  );
  t.mock.timers.tick(4999);
  assert.equal(calls, 0);
  t.mock.timers.tick(1);
  assert.equal(calls, 1);
  ready = false;
  t.mock.timers.tick(5000);
  assert.equal(calls, 1);
  ready = true;
  t.mock.timers.tick(5000);
  assert.equal(calls, 2);
  stop();
  t.mock.timers.tick(5000);
  assert.equal(calls, 2);
});
test('an in-flight refresh cannot replace a selected detail while its action is busy', async () => {
  let resolve: ((value: Response) => void) | undefined,
    calls = 0,
    busy = false;
  const api = createMessageAPI('', async () => {
    calls++;
    if (calls === 1) return Response.json({ message: row, result: null });
    return new Promise<Response>((r) => (resolve = r));
  });
  const history = createMessageHistory(
    api,
    async () => 't',
    () => {},
    () => {},
    () => busy,
  );
  await history.select(id);
  const refresh = history.refresh();
  await Promise.resolve();
  await Promise.resolve();
  busy = true;
  resolve?.(
    Response.json({
      message: { ...row, secondary: 'completed' },
      result: null,
    }),
  );
  await refresh;
  assert.equal(history.state().selected?.message.secondary, 'waiting');
  history.dispose();
});
test('message parsing keeps only extracted facts and validates stages and dates', () => {
  const parsed = parseMessage({
    ...row,
    subject: 'private',
    sender: 'private',
    body: 'private',
    headers: 'private',
  });
  assert.deepEqual(parsed, row);
  for (const state of stageStates)
    assert.equal(parseMessage({ ...row, primary: state }).primary, state);
  assert.throws(() => parseMessage({ ...row, secondary: 'future_state' }));
  assert.throws(() =>
    parseMessage({ ...row, discovered_at: '2026-02-30T00:00:00Z' }),
  );
});
test('message API carries stage, account and pagination; rejects unknown filter and malformed summary', async () => {
  let path = '';
  let calls = 0;
  const api = createMessageAPI('', async (url) => {
    path = String(url);
    calls++;
    return Response.json(page);
  });
  const result = await api.messages(
    { account_id: id, stage: 'secondary_waiting', limit: 20, offset: 40 },
    async () => 't',
  );
  assert.equal(result.total, 81);
  assert.match(path, /account_id=1111/);
  assert.match(path, /stage=secondary_waiting/);
  assert.match(path, /offset=40/);
  await assert.rejects(
    api.messages(
      { account_id: '', stage: 'bad' as never, limit: 20, offset: 0 },
      async () => 't',
    ),
    { code: 'invalid_request' },
  );
  assert.equal(calls, 1);
  const bad = createMessageAPI('', async () =>
    Response.json({
      ...page,
      summary: { ...page.summary, primary: { ...counts, failed: -1 } },
    }),
  );
  await assert.rejects(
    bad.messages(
      { account_id: '', stage: 'all', limit: 20, offset: 0 },
      async () => 't',
    ),
  );
});
test('message detail uses its direct endpoint even for old rows', async () => {
  let path = '';
  const api = createMessageAPI('', async (url) => {
    path = String(url);
    return Response.json({ message: row, result: null });
  });
  assert.equal((await api.message(id, async () => 't')).message.id, id);
  assert.equal(path, `/v1/analysis/messages/${id}`);
});
test('detail rejects a result belonging to another discovered message', async () => {
  const result = {
    ...processingResult(),
    account_id: id,
    account_label: 'メール',
    registrations: [],
  };
  const api = createMessageAPI('', async () =>
    Response.json({
      message: { ...row, result_id: processingIDs.result },
      result,
    }),
  );
  await assert.rejects(
    api.message(id, async () => 't'),
    { code: 'dependency_unavailable' },
  );
  const matching = createMessageAPI('', async () =>
    Response.json({
      message: { ...row, result_id: processingIDs.result },
      result: { ...result, message_id: id },
    }),
  );
  assert.equal(
    (await matching.message(id, async () => 't')).result?.message_id,
    id,
  );
});
test('history polling preserves selected detail on transient failure and purges it on owner denial', async () => {
  let fail = '';
  let calls = 0;
  const api = createMessageAPI('', async (url) => {
    calls++;
    if (fail) throw new ApiError(fail as 'access_denied');
    return Response.json(
      String(url).includes('?') ? page : { message: row, result: null },
    );
  });
  let denied = 0;
  const history = createMessageHistory(
    api,
    async () => 't',
    () => {},
    () => denied++,
  );
  const filter = {
    account_id: '',
    stage: 'all' as const,
    limit: 20,
    offset: 0,
  };
  await history.load(filter);
  await history.select(id);
  assert.equal(history.state().selected?.message.id, id);
  fail = 'dependency_unavailable';
  await history.refresh();
  assert.equal(history.state().selected?.message.id, id);
  fail = 'access_denied';
  await history.refresh();
  assert.equal(history.state().selected, null);
  assert.equal(history.state().page, null);
  assert.equal(denied, 1);
  const before = calls;
  await history.refresh();
  assert.equal(calls, before);
  history.dispose();
});
test('a late list response cannot restore owner data after detail access is denied', async () => {
  let resolve: ((value: Response) => void) | undefined;
  const api = createMessageAPI('', async (url) => {
    if (String(url).includes('?'))
      return new Promise<Response>((r) => (resolve = r));
    return new Response(null, { status: 403 });
  });
  const history = createMessageHistory(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  const load = history.load({
    account_id: '',
    stage: 'all',
    limit: 20,
    offset: 0,
  });
  await Promise.resolve();
  await Promise.resolve();
  await history.select(id);
  resolve?.(Response.json(page));
  await load;
  assert.equal(history.state().status, 'denied');
  assert.equal(history.state().page, null);
  history.dispose();
});
