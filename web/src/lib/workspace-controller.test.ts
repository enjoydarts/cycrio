import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createWorkspaceController } from './workspace-controller.ts';
import type { OverviewReport } from './overview-api.ts';
import { overviewFixture } from './overview-fixtures.ts';
import { mailConnectionIndicator } from './mail-connection-indicator.ts';

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
for (const background of [false, true])
  test(`${background ? 'background' : 'manual'} refresh and disposal abort previous work and ignore late results`, async () => {
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
    const first = c.refresh({ background });
    const second = c.refresh({ background });
    assert.equal(pending[0].signal?.aborted, true);
    pending[1].resolve(report);
    await second;
    pending[0].resolve({ ...report, attention_messages: 99 });
    await first;
    assert.equal(c.state().overview.value?.attention_messages, 3);
    const third = c.refresh({ background });
    c.dispose();
    pending[2].resolve(report);
    await third;
    assert.equal(c.state().overview.value, null);
    assert.equal(pending[2].signal?.aborted, true);
  });

test('background refresh keeps the list and current report until completion, then clears failed overview', async () => {
  let fail = false;
  let resolve!: (v: OverviewReport) => void;
  let first = true;
  const c = createWorkspaceController(
    { list: async () => [] },
    {
      read: async () => {
        if (first) {
          first = false;
          return report;
        }
        if (fail) throw new Error('offline');
        return new Promise<OverviewReport>((r) => {
          resolve = r;
        });
      },
    },
    async () => 'token',
    () => {},
    () => {},
  );
  await c.refresh();
  const loading = c.refresh({ background: true });
  assert.equal(c.state().accounts.status, 'ready');
  assert.equal(c.state().overview.value, report);
  resolve({ ...report, attention_messages: 9 });
  await loading;
  assert.equal(c.state().overview.value?.attention_messages, 9);
  fail = true;
  await c.refresh({ background: true });
  assert.equal(c.state().accounts.status, 'ready');
  assert.equal(c.state().overview.value, null);
  assert.equal(c.state().overview.status, 'error');
});

for (const exit of ['dispose', 'denied'] as const)
  test(`visible polling and focus update stopped indicators, then ${exit} releases subscriptions`, async () => {
    let visible = false;
    let tick!: () => void;
    let wake!: () => void;
    let intervalRemoved = false,
      listenerRemoved = false;
    let stopped = false,
      deny = false;
    const page = {
      ...overviewFixture(),
      connections: [
        {
          ...overviewFixture().connections[0],
          state: 'ready' as const,
          automation_state: 'enabled' as const,
        },
      ],
    } as OverviewReport;
    const c = createWorkspaceController(
      { list: async () => [] },
      {
        read: async () => {
          if (deny) throw new ApiError('access_denied');
          return stopped
            ? {
                ...page,
                connections: [
                  { ...page.connections[0], automation_state: 'stopped' },
                ],
              }
            : page;
        },
      },
      async () => 'token',
      () => {},
      () => {},
    );
    const flush = () => new Promise((r) => setTimeout(r, 0));
    const indicator = () =>
      mailConnectionIndicator(
        { state: 'ready', last_error: '' },
        c.state().overview.value?.connections[0],
      );
    await c.refresh();
    c.startPolling({
      isVisible: () => visible,
      every: (callback, milliseconds) => {
        assert.equal(milliseconds, 15_000);
        tick = callback;
        return () => {
          intervalRemoved = true;
        };
      },
      onWake: (callback) => {
        wake = callback;
        return () => {
          listenerRemoved = true;
        };
      },
    });
    stopped = true;
    tick();
    await flush();
    assert.equal(indicator().tone, 'ready');
    visible = true;
    tick();
    await flush();
    assert.equal(indicator().tone, 'neutral');
    stopped = false;
    wake();
    await flush();
    assert.equal(indicator().tone, 'ready');
    if (exit === 'dispose') c.dispose();
    else {
      deny = true;
      wake();
      await flush();
    }
    assert.equal(intervalRemoved, true);
    assert.equal(listenerRemoved, true);
    stopped = true;
    tick();
    wake();
    await flush();
    assert.equal(c.state().overview.value, null);
  });
