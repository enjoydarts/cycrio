import assert from 'node:assert/strict';
import test from 'node:test';
import { createImportExecutionsAPI } from './import-executions-api.ts';
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
