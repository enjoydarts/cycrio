import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createWorkspaceController } from './workspace-controller.ts';
import type { OverviewReport } from './overview-api.ts';

const report = { attention_messages: 3 } as OverviewReport;
test('workspace shares independent reads and hides stale values during refresh and failure', async () => {
  let fail = false;
  const c = createWorkspaceController(
    { list: async () => [] },
    {
      read: async () => {
        if (fail) throw new Error();
        return report;
      },
    },
    async () => 'token',
    () => {},
    () => {},
  );
  await c.refresh();
  assert.equal(c.state().overview.value, report);
  fail = true;
  const loading = c.refresh();
  assert.equal(c.state().overview.value, null);
  assert.equal(c.state().overview.status, 'loading');
  await loading;
  assert.equal(c.state().accounts.status, 'ready');
  assert.equal(c.state().overview.status, 'error');
  assert.equal(c.state().overview.value, null);
});
for (const code of ['authentication_required', 'access_denied'] as const)
  test(`${code} purges both sources and ignores late completion and future refresh`, async () => {
    let resolve!: (v: OverviewReport) => void;
    let denied = 0;
    const c = createWorkspaceController(
      {
        list: async () => {
          throw new ApiError(code);
        },
      },
      {
        read: () =>
          new Promise((r) => {
            resolve = r;
          }),
      },
      async () => 'token',
      () => {},
      () => {
        denied++;
      },
    );
    const loading = c.refresh();
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(c.state().accounts.status, 'denied');
    assert.equal(c.state().overview.status, 'denied');
    resolve(report);
    await loading;
    await c.refresh();
    assert.equal(c.state().overview.value, null);
    assert.equal(denied, 1);
  });
test('refresh and disposal abort previous work and ignore late results', async () => {
  const pending: {
    resolve: (v: OverviewReport) => void;
    signal?: AbortSignal;
  }[] = [];
  const c = createWorkspaceController(
    { list: async () => [] },
    {
      read: (_t, signal) =>
        new Promise((resolve) => pending.push({ resolve, signal })),
    },
    async () => 'token',
    () => {},
    () => {},
  );
  const first = c.refresh();
  const second = c.refresh();
  assert.equal(pending[0].signal?.aborted, true);
  pending[1].resolve(report);
  await second;
  pending[0].resolve({ ...report, attention_messages: 99 });
  await first;
  assert.equal(c.state().overview.value?.attention_messages, 3);
  const third = c.refresh();
  c.dispose();
  pending[2].resolve(report);
  await third;
  assert.equal(c.state().overview.value, null);
  assert.equal(pending[2].signal?.aborted, true);
});
