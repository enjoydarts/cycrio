import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createAISetupController } from './ai-setup-controller.ts';
import { setupFixture, routesFixture } from './ai-setup-test-fixtures.ts';
import type { RoutesResult } from './ai-routes-api.ts';
import { priceFields, type PriceInput } from './catalog-api.ts';
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
const standardInput = {
  expected_version: 1,
  runtime_version: 1,
  profile_id: '22222222-2222-4222-8222-222222222222',
  enabled: true,
  primary_mode: 'standard' as const,
  primary_terms_id: 'liquid-standard-2026-09-30',
  primary_retention: '',
  primary_confirmed: true,
  secondary_route_id: '00000000-0000-4000-8000-000000000001',
  secondary_confirmed: true,
};
for (const action of [
  'price',
  'standard',
  'customStop',
  'runtimeStop',
  'status',
] as const)
  test(`superseding a delayed metadata refresh with ${action} clears only the cancelled loading state`, async () => {
    const { api, state } = setupFixture();
    state.runtime.active = state.runtime.draft;
    state.runtime.active_ready = true;
    const pending = deferred<RoutesResult>();
    api.routes.refresh = () => pending.promise;
    api.catalog.savePrice = async () => state.models[0];
    api.processing.saveStandardPolicy = async () => state.policy;
    api.processing.savePolicy = async () => state.policy;
    api.runtime.deactivate = async () => ({
      ...state.runtime,
      active: null,
      active_ready: false,
    });
    const controller = createAISetupController(
      api,
      async () => 'token',
      () => {},
      () => {},
    );
    await controller.load();
    controller.choose('primary', state.models[0].id);
    const loading = controller.refreshRoutes();
    assert.equal(controller.state().routeLoading, true);
    if (action === 'price') {
      const input = {
        expected_version: 1,
        currency: 'USD',
        effective_from: '2026-10-27T00:00:00+09:00',
        source_url: 'https://example.test/prices',
        ...Object.fromEntries(priceFields.map((key) => [key, null])),
      } as PriceInput;
      await controller.savePrice(state.models[0], input);
    } else if (action === 'standard')
      await controller.saveStandard(standardInput);
    else if (action === 'customStop')
      await controller.saveCustom({
        expected_version: 1,
        profile_id: standardInput.profile_id,
        enabled: false,
        primary_retention: '契約条件',
        secondary_retention: '契約条件',
        primary_confirmed: true,
        secondary_confirmed: true,
      });
    else if (action === 'runtimeStop') await controller.deactivate();
    else await controller.refresh();
    assert.equal(controller.state().routeLoading, false);
    assert.equal(controller.state().status, 'ready');
    assert.equal(controller.state().selection?.dirty, true);
    pending.resolve(routesFixture());
    await loading;
    assert.equal(controller.state().routeLoading, false);
    assert.equal(controller.state().routeError, false);
    await controller.refresh();
    assert.equal(controller.state().routeLoading, false);
  });
test('an old cancelled refresh cannot release or overwrite a newer refresh owner', async () => {
  const { api } = setupFixture();
  const first = deferred<RoutesResult>(),
    second = deferred<RoutesResult>();
  let requests = 0;
  api.routes.refresh = () =>
    ++requests === 1 ? first.promise : second.promise;
  const controller = createAISetupController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  await controller.load();
  const old = controller.refreshRoutes();
  await controller.refresh();
  const current = controller.refreshRoutes();
  assert.equal(requests, 2);
  first.reject(new ApiError('access_denied'));
  await old;
  assert.equal(controller.state().status, 'ready');
  assert.equal(controller.state().routeLoading, true);
  const coalesced = controller.refreshRoutes();
  assert.equal(requests, 2);
  second.resolve(routesFixture());
  await Promise.all([current, coalesced]);
  assert.equal(controller.state().routeLoading, false);
  assert.equal(controller.state().routeError, false);
});
for (const code of [
  'dependency_unavailable',
  'access_denied',
  'authentication_required',
] as const)
  test(`current metadata ${code} finishes its loading state and respects the authorization boundary`, async () => {
    const { api } = setupFixture();
    const pending = deferred<RoutesResult>();
    api.routes.refresh = () => pending.promise;
    let denied = 0;
    const controller = createAISetupController(
      api,
      async () => 'token',
      () => {},
      () => {
        denied++;
      },
    );
    await controller.load();
    const loading = controller.refreshRoutes();
    pending.reject(new ApiError(code));
    await loading;
    assert.equal(controller.state().routeLoading, false);
    if (code === 'dependency_unavailable') {
      assert.equal(controller.state().routeError, true);
      assert.equal(denied, 0);
    } else {
      assert.equal(controller.state().status, 'denied');
      assert.equal(controller.state().selection, null);
      assert.equal(controller.state().runtime, null);
      assert.equal(denied, 1);
    }
  });
test('disposing a delayed metadata refresh never restores private state', async () => {
  const { api } = setupFixture();
  const pending = deferred<RoutesResult>();
  api.routes.refresh = () => pending.promise;
  const controller = createAISetupController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  await controller.load();
  const loading = controller.refreshRoutes();
  controller.dispose();
  pending.resolve(routesFixture());
  await loading;
  assert.equal(controller.state().status, 'disposed');
  assert.equal(controller.state().routeLoading, false);
  assert.equal(controller.state().selection, null);
});
for (const action of [
  'price',
  'standard',
  'customStop',
  'runtimeStop',
  'status',
] as const)
  for (const code of ['dependency_unavailable', 'access_denied'] as const)
    test(`a superseding ${action} ${code} cannot leave the cancelled metadata spinner running`, async () => {
      const { api, state } = setupFixture();
      state.runtime.active = state.runtime.draft;
      state.runtime.active_ready = true;
      const pending = deferred<RoutesResult>();
      api.routes.refresh = () => pending.promise;
      const controller = createAISetupController(
        api,
        async () => 'token',
        () => {},
        () => {},
      );
      await controller.load();
      const loading = controller.refreshRoutes();
      const rejected = async () => {
        throw new ApiError(code);
      };
      if (action === 'price') {
        api.catalog.savePrice = rejected;
        await controller.savePrice(state.models[0], {} as PriceInput);
      } else if (action === 'standard') {
        api.processing.saveStandardPolicy = rejected;
        await controller.saveStandard(standardInput);
      } else if (action === 'customStop') {
        api.processing.savePolicy = rejected;
        await controller.saveCustom({
          ...state.policy.policy!,
          expected_version: 1,
          enabled: false,
        });
      } else if (action === 'runtimeStop') {
        api.runtime.deactivate = rejected;
        await controller.deactivate();
      } else {
        api.runtime.state = rejected;
        await controller.refresh();
      }
      assert.equal(controller.state().routeLoading, false);
      assert.equal(
        controller.state().status,
        code === 'access_denied' ? 'denied' : 'error',
      );
      pending.reject(new DOMException('Aborted', 'AbortError'));
      await loading;
      assert.equal(controller.state().routeLoading, false);
      if (code === 'access_denied')
        assert.equal(controller.state().selection, null);
    });
test('a cancelled automatic refresh cannot release the reading lock of a newer status request', async () => {
  const { api, state } = setupFixture();
  state.routes.fresh = false;
  const pending = deferred<RoutesResult>(),
    started = deferred<void>(),
    runtime = deferred<typeof state.runtime>();
  api.routes.refresh = () => {
    started.resolve();
    return pending.promise;
  };
  const controller = createAISetupController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  const initial = controller.load();
  await started.promise;
  api.runtime.state = () => runtime.promise;
  const status = controller.refresh();
  assert.equal(controller.state().status, 'loading');
  pending.reject(new DOMException('Aborted', 'AbortError'));
  await initial;
  let saves = 0;
  api.catalog.savePrice = async () => {
    saves++;
    return state.models[0];
  };
  await controller.savePrice(state.models[0], {} as PriceInput);
  assert.equal(saves, 0);
  runtime.resolve(state.runtime);
  await status;
  await controller.savePrice(state.models[0], {} as PriceInput);
  assert.equal(saves, 1);
  assert.equal(controller.state().routeLoading, false);
});
