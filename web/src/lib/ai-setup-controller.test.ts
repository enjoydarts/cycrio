import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createAISetupController,
  preferredRoute,
  setupStage,
  emptyAISetupView,
  canSaveSetup,
} from './ai-setup-controller.ts';
import {
  routeFixture,
  setupFixture,
  secondaryID,
} from './ai-setup-test-fixtures.ts';
import { parseRuntimeState } from './airuntime-api.ts';
import { runtime, id } from './airuntime-test-fixtures.ts';
test('preferred route keeps exact tag across immutable snapshot UUID changes', () => {
  const route = routeFixture();
  const other = {
    ...route,
    id: '22222222-2222-4222-8222-222222222222',
    tag: 'deepinfra/other',
  };
  assert.equal(
    preferredRoute([other, route], id, 'deepinfra/fp4')?.tag,
    'deepinfra/fp4',
  );
  assert.equal(
    preferredRoute([{ ...route, eligible: false }], id, 'deepinfra/fp4'),
    null,
  );
});
test('a provider filter never saves a retained selection from another provider', async () => {
  const { api } = setupFixture();
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  const selection = structuredClone(controller.state().selection);
  assert.equal(canSaveSetup(controller.state(), 'openrouter'), true);
  assert.equal(canSaveSetup(controller.state(), 'openai'), false);
  assert.deepEqual(controller.state().selection, selection);
});
test('initial eligibility uses the current profile limits and metadata refresh reads updated policy', async () => {
  const { api, state } = setupFixture();
  state.routes.fresh = false;
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  assert.equal(state.refreshes, 1);
  assert.equal(
    new URL(
      state.requests.find((r) => r.path.startsWith('/v1/admin/ai/routes?'))!
        .path,
      'https://example.test',
    ).searchParams.get('max_input_bytes'),
    '8192',
  );
  assert.equal(
    state.requests.filter((r) => r.path === '/v1/admin/mail-processing/policy')
      .length,
    2,
  );
  await controller.refresh();
  assert.equal(state.refreshes, 1);
});
test('a missing current key never triggers metadata POST and all route pages remain selectable', async () => {
  const { api, state } = setupFixture();
  state.routes.fresh = false;
  state.routes.key_available = false;
  state.routes.routes = Array.from({ length: 101 }, (_, i) => ({
    ...routeFixture(),
    id: `${(i + 1).toString(16).padStart(8, '0')}-1111-4111-8111-111111111111`,
    model_id: secondaryID,
  }));
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  await controller.refreshRoutes();
  assert.equal(state.refreshes, 0);
  assert.equal(controller.state().routes?.routes.length, 101);
  assert.equal(
    state.requests.filter((r) => r.path.includes('offset=100')).length,
    1,
  );
});
test('uncertain paid requests replay one nonce, accepted requests permit a fresh explicitly confirmed test', async () => {
  const { api, state } = setupFixture();
  let next = 0;
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
    () => `11111111-1111-4111-8111-${String(++next).padStart(12, '0')}`,
  );
  await controller.load();
  state.failure.set('POST /v1/admin/ai/runtime/tests', 503);
  await controller.requestTest(false);
  assert.equal(next, 0);
  await controller.requestTest(true);
  assert.equal(controller.state().requestUncertain, true);
  controller.choose('primary', id);
  assert.equal(controller.state().selection?.dirty, false);
  state.failure.clear();
  await controller.requestTest(true);
  assert.equal(controller.state().requestUncertain, false);
  await controller.requestTest(true);
  const requests = state.requests.filter(
    (r) => r.path === '/v1/admin/ai/runtime/tests',
  );
  assert.equal(requests.length, 3);
  assert.deepEqual(requests[0].body, requests[1].body);
  assert.notEqual(requests[1].body.request_id, requests[2].body.request_id);
});
test('partial model save reports success separately and preserves the editing and runtime baselines', async () => {
  const { api, state } = setupFixture();
  state.failure.set('PUT /v1/admin/ai/runtime', 503);
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  controller.choose('primary', id);
  await controller.save();
  assert.equal(controller.state().status, 'error');
  assert.equal(controller.state().partialSaved, true);
  assert.equal(controller.state().settings?.version, 2);
  assert.equal(controller.state().selection?.baseline_version, 1);
  assert.equal(controller.state().runtimeBaseline, 1);
  assert.equal(
    state.requests.some((r) => r.path.endsWith('/runtime/tests')),
    false,
  );
});
test('late metadata cannot silently replace the selected dirty provider variant', async () => {
  const { api, state } = setupFixture();
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  controller.choose('secondary', secondaryID);
  state.routes.routes = [
    { ...routeFixture(), model_id: secondaryID, tag: 'deepinfra/fp8' },
  ];
  await controller.refreshRoutes();
  assert.equal(controller.state().selection?.values.secondary, secondaryID);
  assert.equal(controller.state().selection?.values.route_tag, 'deepinfra/fp4');
  assert.equal(controller.state().selection?.values.route_id, '');
  assert.equal(controller.state().selection?.baseline_version, 1);
});
test('missing AI credentials remain an actionable safe error after partial settings save', async () => {
  const { api } = setupFixture();
  api.runtime.save = async () => {
    throw Object.assign(new Error(), { code: 'credentials_required' });
  };
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  await controller.save();
  assert.equal(controller.state().error, 'credentials_required');
  assert.equal(controller.state().partialSaved, true);
});
test('changed capacity limits require rechecking eligibility and unknown reference prices do not block saving', async () => {
  const { api, state } = setupFixture();
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  const saves = () =>
    state.requests.filter(
      (r) => r.method === 'PUT' && r.path.endsWith('/runtime'),
    ).length;
  await controller.load();
  await controller.save();
  assert.equal(saves(), 1);
  controller.editLimits({ max_input_bytes: 16384 });
  await controller.save();
  assert.equal(saves(), 1);
  await controller.refreshRoutes();
  await controller.save();
  assert.equal(saves(), 2);
  controller.editLimits({ max_input_bytes: 0 });
  await controller.save();
  assert.equal(saves(), 2);
});
test('explicit runtime stop stays available and never discards dirty model inputs', async () => {
  const { api, state } = setupFixture();
  state.runtime.active = state.runtime.draft;
  state.runtime.active_ready = true;
  let stopped = 0;
  api.runtime.deactivate = async () => {
    stopped++;
    return { ...state.runtime, version: 2, active: null, active_ready: false };
  };
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  controller.choose('primary', id);
  assert.equal(typeof controller.deactivate, 'function');
  await controller.deactivate();
  assert.equal(stopped, 1);
  assert.equal(controller.state().runtime?.active, null);
  assert.equal(controller.state().selection?.dirty, true);
  assert.equal(controller.state().selection?.baseline_version, 1);
});
test('new saved draft proceeds to testing while the previous active version remains usable', () => {
  const r = parseRuntimeState(runtime());
  r.active = { ...r.draft!, id: '22222222-2222-4222-8222-222222222222' };
  r.active_ready = true;
  assert.equal(
    setupStage({
      ...emptyAISetupView(),
      runtime: r,
      settings: {
        version: 1,
        id,
        primary_model_id: id,
        secondary_model_id: id,
        primary_price_id: null,
        secondary_price_id: null,
        updated_at: null,
      },
    }),
    'test',
  );
});
test('rotated registered credentials send a frozen old-key draft to model saving before another paid test', async () => {
  const { api, state } = setupFixture();
  state.runtime.active = state.runtime.draft;
  state.runtime.active_ready = false;
  state.runtime.active_blocked_reason = 'credentials_required';
  const controller = createAISetupController(
    {
      ...api,
      keys: {
        credentials: async () => [
          {
            provider: 'liquid',
            state: 'configured',
            version: 2,
            updated_at: null,
          },
          {
            provider: 'openrouter',
            state: 'configured',
            version: 1,
            updated_at: null,
          },
        ],
      },
    },
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  assert.equal(setupStage(controller.state()), 'models');
  await controller.requestTest(true);
  assert.equal(
    state.requests.some((r) => r.path.endsWith('/runtime/tests')),
    false,
  );
});
test('setup composes active state, preserves selection baseline on refresh, and clears all private state on auxiliary denial', async () => {
  const { api, state } = setupFixture();
  const controller = createAISetupController(
    api,
    async () => 't',
    () => {},
    () => {},
  );
  await controller.load();
  controller.choose('primary', id);
  state.settings.version = 2;
  await controller.refresh();
  assert.equal(controller.state().selection?.baseline_version, 1);
  assert.equal(controller.state().selection?.dirty, true);
  assert.equal(state.refreshes, 0);
  state.failure.set('GET /v1/admin/ai/routes', 403);
  await controller.refresh();
  assert.equal(controller.state().status, 'denied');
  assert.equal(controller.state().runtime, null);
  assert.equal(controller.state().selection, null);
  assert.deepEqual(controller.state().models, []);
});
