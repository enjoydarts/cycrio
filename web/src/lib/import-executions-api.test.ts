import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createImportExecutionsAPI,
  parseImportExecution,
} from './import-executions-api.ts';
import { processingIDs as ids } from './mail-processing-fixtures.ts';
export const importExecutionFixture = () => ({
  id: `automation:${ids.run}:1`,
  kind: 'automatic',
  created_at: '2026-10-30T00:00:00Z',
  since: '2025-10-30',
  before: '2026-10-31',
  state: 'watching',
  is_current: true,
  incremental: true,
  discovered_count: 12000,
  pending_count: 100,
  completed_count: 11800,
  candidate_count: 18,
  registered_count: 7,
  failed_count: 90,
  unknown_count: 10,
  active_run_id: ids.run,
});
test('reanalysis is an independent execution kind with nullable period and unrestricted population', () => {
  const row = parseImportExecution({
    ...importExecutionFixture(),
    id: `manual:${ids.run}`,
    kind: 'reanalysis',
    since: null,
    before: null,
  });
  assert.equal(row.kind, 'reanalysis');
  assert.equal(row.discovered_count, 12000);
});
test('execution history preserves full population and a logical run identifier beyond internal batches', async () => {
  let path = '';
  const api = createImportExecutionsAPI('', async (url) => {
    path = String(url);
    return Response.json({
      history: [{ ...importExecutionFixture(), subject: 'do not retain' }],
      total: 123,
      limit: 20,
      offset: 20,
    });
  });
  const page = await api.list(
    { account_id: ids.account, limit: 20, offset: 20 },
    async () => 't',
  );
  assert.ok(path.includes(`/v1/mail/accounts/${ids.account}/import-history?`));
  assert.equal(page.total, 123);
  assert.equal(page.history[0].discovered_count, 12000);
  assert.equal(page.history[0].active_run_id, ids.run);
  assert.equal('subject' in page.history[0], false);
});
test('history cannot be loaded without one specific account', async () => {
  let calls = 0;
  const api = createImportExecutionsAPI('', async () => {
    calls++;
    return Response.json({});
  });
  await assert.rejects(
    api.list({ account_id: '', limit: 20, offset: 0 }, async () => 't'),
  );
  assert.equal(calls, 0);
});

test('history retains account execution times independently of an empty page', async () => {
  const activity = {
    last_sync_started_at: '2026-11-03T14:59:58Z',
    last_analysis_started_at: '2026-11-03T15:00:01Z',
    last_sync_activity: { state: 'completed', added_count: 0 },
    last_analysis_activity: { state: 'failed' },
    automation_activity: {
      state: 'enabled',
      next_check_at: '2026-11-04T01:00:00Z',
      retry_at: null,
      busy: false,
    },
  };
  const api = createImportExecutionsAPI('', async () =>
    Response.json({
      history: [],
      total: 123,
      limit: 20,
      offset: 200,
      ...activity,
    }),
  );
  const page = await api.list(
    { account_id: ids.account, limit: 20, offset: 200 },
    async () => 't',
  );
  for (const [key, value] of Object.entries(activity))
    assert.deepEqual((page as unknown as Record<string, unknown>)[key], value);
});

test('history validates activity counts, states, scheduling and legacy nulls', async () => {
  for (const metadata of [
    { last_sync_activity: { state: 'completed', added_count: -1 } },
    { last_sync_activity: { state: 'completed', added_count: null } },
    { last_analysis_activity: { state: 42 } },
    {
      automation_activity: {
        state: 'enabled',
        next_check_at: 'invalid',
        retry_at: null,
        busy: false,
      },
    },
  ]) {
    const api = createImportExecutionsAPI('', async () =>
      Response.json({
        history: [],
        total: 0,
        limit: 20,
        offset: 0,
        ...metadata,
      }),
    );
    await assert.rejects(
      api.list(
        { account_id: ids.account, limit: 20, offset: 0 },
        async () => 't',
      ),
    );
  }
  const api = createImportExecutionsAPI('', async () =>
    Response.json({
      history: [],
      total: 0,
      limit: 20,
      offset: 0,
      last_sync_activity: null,
      last_analysis_activity: null,
      automation_activity: null,
    }),
  );
  const page = await api.list(
    { account_id: ids.account, limit: 20, offset: 0 },
    async () => 't',
  );
  assert.equal(
    (page as unknown as Record<string, unknown>).last_sync_activity,
    null,
  );
});

test('history rejects malformed execution timestamps while accepting null and older responses', async () => {
  for (const value of [
    null,
    undefined,
    '2026-02-30T00:00:00Z',
    'not-a-date',
    42,
  ]) {
    const api = createImportExecutionsAPI('', async () =>
      Response.json({
        history: [],
        total: 0,
        limit: 20,
        offset: 0,
        last_sync_started_at: value,
        last_analysis_started_at: value,
      }),
    );
    const load = () =>
      api.list(
        { account_id: ids.account, limit: 20, offset: 0 },
        async () => 't',
      );
    if (value === null || value === undefined) await load();
    else await assert.rejects(load());
  }
});
