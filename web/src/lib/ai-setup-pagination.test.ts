import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createAISetupController,
  canSaveSetup,
} from './ai-setup-controller.ts';
import {
  setupFixture,
  routeFixture,
  routesFixture,
  secondaryID,
} from './ai-setup-test-fixtures.ts';
import type { RoutesResult, RoutesFilter } from './ai-routes-api.ts';
const otherSnapshot = '33333333-3333-4333-8333-333333333333';
function page(offset: number): RoutesResult {
  const routes = Array.from({ length: 101 }, (_, i) => ({
    ...routeFixture(),
    id: `${(i + 1).toString(16).padStart(8, '0')}-1111-4111-8111-111111111111`,
    model_id: secondaryID,
  }));
  return {
    ...routesFixture(),
    total: 101,
    routes: routes.slice(offset, offset + 100),
  };
}
test('all 101 eligible rows come from one snapshot and use one immutable capacity filter', async () => {
  const { api } = setupFixture();
  const filters: RoutesFilter[] = [];
  api.routes.list = async (filter) => {
    filters.push({ ...filter });
    const result = page(filter.offset ?? 0);
    filter.max_input_bytes = 65536;
    return result;
  };
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  assert.equal(controller.state().routes?.routes.length, 101);
  assert.equal(canSaveSetup(controller.state()), true);
  assert.deepEqual(
    filters.map((f) => [f.offset, f.max_input_bytes, f.max_output_tokens]),
    [
      [0, 8192, 1024],
      [100, 8192, 1024],
    ],
  );
});
for (const difference of [
  'snapshot',
  'fresh',
  'key',
  'total',
  'refresh',
  'capturedAt',
  'source',
  'rowSnapshot',
  'empty',
  'duplicate',
] as const)
  test(`a second route page with changed ${difference} is discarded as a whole and can be retried`, async () => {
    const { api } = setupFixture();
    const controller = createAISetupController(
      api,
      async () => 't',
      () => {},
      () => {},
    );
    await controller.load();
    controller.choose('secondary', secondaryID);
    const baseline = controller.state().selection!.baseline_version;
    let calls = 0,
      unstable = true;
    api.routes.list = async (filter) => {
      calls++;
      const result = page(filter.offset ?? 0);
      if (unstable && filter.offset) {
        if (difference === 'snapshot')
          result.snapshot = { ...result.snapshot!, id: otherSnapshot };
        if (difference === 'fresh') result.fresh = false;
        if (difference === 'key') result.key_available = false;
        if (difference === 'total') result.total = 102;
        if (difference === 'refresh')
          result.refresh = { ...result.refresh, status: 'running' };
        if (difference === 'capturedAt')
          result.snapshot = {
            ...result.snapshot!,
            captured_at: '2026-10-26T00:00:00Z',
          };
        if (difference === 'source')
          result.snapshot = {
            ...result.snapshot!,
            source_url: 'https://example.test/another-source',
          };
        if (difference === 'rowSnapshot')
          result.routes = [{ ...result.routes[0], snapshot_id: otherSnapshot }];
        if (difference === 'empty') result.routes = [];
        if (difference === 'duplicate') result.routes = [page(0).routes[0]];
      }
      return result;
    };
    await controller.refresh();
    assert.equal(controller.state().status, 'error');
    assert.equal(controller.state().routeError, true);
    assert.equal(controller.state().routes, null);
    assert.equal(canSaveSetup(controller.state()), false);
    assert.equal(controller.state().selection?.baseline_version, baseline);
    assert.equal(controller.state().selection?.dirty, true);
    assert.equal(calls, 2);
    unstable = false;
    await controller.refresh();
    assert.equal(controller.state().status, 'ready');
    assert.equal(controller.state().routes?.routes.length, 101);
    assert.equal(controller.state().routeError, false);
    assert.equal(canSaveSetup(controller.state()), true);
  });
test('continuously changing snapshot pages stop after a bounded read and preserve a manual retry', async () => {
  const { api } = setupFixture();
  let calls = 0;
  api.routes.list = async (filter) => {
    calls++;
    const result = page(filter.offset ?? 0);
    if (filter.offset)
      result.snapshot = { ...result.snapshot!, id: otherSnapshot };
    return result;
  };
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  assert.equal(controller.state().status, 'error');
  assert.equal(calls, 2);
  await controller.refresh();
  assert.equal(controller.state().status, 'error');
  assert.equal(calls, 4);
  assert.equal(controller.state().routeLoading, false);
});
test('metadata POST followed by mixed pages is rejected without retaining a fresh selectable route set', async () => {
  const { api } = setupFixture();
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  api.routes.list = async (filter) => {
    const result = page(filter.offset ?? 0);
    if (filter.offset) result.key_available = false;
    return result;
  };
  await controller.refreshRoutes();
  assert.equal(controller.state().routeLoading, false);
  assert.equal(controller.state().routeError, true);
  assert.equal(controller.state().routes, null);
  assert.equal(canSaveSetup(controller.state()), false);
  api.routes.list = async (filter) => page(filter.offset ?? 0);
  await controller.refresh();
  assert.equal(controller.state().routes?.routes.length, 101);
  assert.equal(controller.state().routeError, false);
});
