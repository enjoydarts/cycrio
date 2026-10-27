import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createAISetupController } from './ai-setup-controller.ts';
import { createStandardReview } from './standard-policy-draft.ts';
import { setupFixture, routeFixture } from './ai-setup-test-fixtures.ts';
import type {
  PolicyState,
  PolicyInput,
  StandardPolicyInput,
} from './mail-processing-api.ts';
const terms = {
  id: 'liquid-standard-2026-09-30',
  version: '2026-09-30',
  source_url: 'https://www.liquid.ai/terms-conditions',
  summary: '標準保持・学習利用可能・固定日数なし・非ZDR',
  zdr: false,
  training_permitted: true,
  fixed_retention_days: null,
};
function setup() {
  const { api, state } = setupFixture();
  state.runtime.active = state.runtime.draft;
  state.runtime.active_ready = true;
  const profileID = state.runtime.active!.id;
  let latest: PolicyState = {
    ...state.policy,
    runtime_version: state.runtime.version,
    profile: { ...state.policy.profile!, id: profileID },
    policy: {
      ...state.policy.policy!,
      profile_id: profileID,
      version: 1,
      enabled: true,
      primary_retention: terms.summary,
      secondary_retention: '確認済みZDR',
      primary_confirmed: true,
      secondary_confirmed: true,
      standard_proof: {
        profile_id: profileID,
        policy_version: 1,
        primary_mode: 'standard',
        primary_terms: terms,
        primary_retention: terms.summary,
        secondary_retention: '確認済みZDR',
        secondary_route: routeFixture(),
        actor_id: profileID,
        confirmed_at: '2026-10-27T00:00:00Z',
      },
    },
    standard_terms: {
      primary: terms,
      secondary_route: routeFixture(),
      ready: true,
      blocked_reason: '',
    },
  };
  let readError: ApiError | null = null,
    legacyWrites = 0,
    standardWrites = 0,
    denied = 0;
  api.processing.policy = async () => {
    if (readError) throw readError;
    return latest;
  };
  api.processing.savePolicy = async (input) => {
    legacyWrites++;
    latest = {
      ...latest,
      policy: {
        ...latest.policy!,
        ...input,
        version: latest.policy!.version + 1,
        standard_proof: undefined,
      },
    };
    const { standard_terms: _terms, ...legacy } = latest;
    return legacy;
  };
  api.processing.saveStandardPolicy = async (input) => {
    standardWrites++;
    latest = {
      ...latest,
      policy: {
        ...latest.policy!,
        version: latest.policy!.version + 1,
        enabled: true,
        standard_proof: {
          profile_id: input.profile_id,
          policy_version: latest.policy!.version + 1,
          primary_mode: input.primary_mode,
          primary_terms: terms,
          primary_retention: terms.summary,
          secondary_retention: '確認済みZDR',
          secondary_route: routeFixture(),
          actor_id: profileID,
          confirmed_at: '2026-10-27T00:00:00Z',
        },
      },
    };
    return latest;
  };
  const controller = createAISetupController(
    api,
    async () => 'token',
    () => {},
    () => {
      denied++;
    },
  );
  return {
    api,
    state,
    controller,
    latest: () => latest,
    setLatest: (value: PolicyState) => (latest = value),
    readError: (code: ApiError['code'] | null) =>
      (readError = code ? new ApiError(code) : null),
    counts: () => ({ legacyWrites, standardWrites, denied }),
  };
}
function stopInput(state: PolicyState): PolicyInput {
  return {
    ...state.policy!,
    expected_version: state.policy!.version,
    enabled: false,
  };
}
function standardInput(state: PolicyState): StandardPolicyInput {
  return {
    expected_version: state.policy!.version,
    runtime_version: state.runtime_version,
    profile_id: state.profile!.id,
    enabled: true,
    primary_mode: 'standard',
    primary_terms_id: terms.id,
    primary_retention: '',
    primary_confirmed: true,
    secondary_route_id: state.standard_terms!.secondary_route!.id,
    secondary_confirmed: true,
  };
}
test('stopping a standard permission reloads omitted metadata and permits fresh standard confirmation on the same page', async () => {
  const x = setup();
  await x.controller.load();
  assert.equal(await x.controller.saveCustom(stopInput(x.latest())), true);
  const stopped = x.controller.state().policy!;
  assert.ok(stopped.standard_terms);
  assert.equal(stopped.policy!.enabled, false);
  const review = createStandardReview(stopped);
  assert.equal(review.draft.values.primary_mode, 'standard');
  assert.equal(review.draft.values.primary_confirmed, false);
  assert.equal(review.draft.values.secondary_confirmed, false);
  await x.controller.saveStandard(standardInput(stopped));
  assert.equal(x.counts().standardWrites, 1);
  assert.equal(
    x.controller.state().policy?.policy?.standard_proof?.primary_mode,
    'standard',
  );
});
test('acknowledged stop survives metadata failure, blocks repermission until a successful read, and never falls back to custom', async () => {
  const x = setup();
  await x.controller.load();
  x.readError('dependency_unavailable');
  assert.equal(await x.controller.saveCustom(stopInput(x.latest())), true);
  assert.equal(x.controller.state().status, 'ready');
  assert.equal(x.controller.state().policy?.policy?.enabled, false);
  assert.equal(x.controller.state().policyReloadRequired, true);
  assert.match(x.controller.state().success ?? '', /停止/);
  assert.match(x.controller.state().success ?? '', /再取得|読み直/);
  assert.equal(x.controller.state().error, null);
  assert.equal(
    createStandardReview(x.controller.state().policy!).draft.values
      .primary_mode,
    'standard',
  );
  await x.controller.saveStandard(standardInput(x.latest()));
  await x.controller.saveCustom({ ...stopInput(x.latest()), enabled: true });
  assert.equal(x.counts().standardWrites, 0);
  assert.equal(x.counts().legacyWrites, 1);
  x.readError(null);
  await x.controller.refresh();
  assert.equal(x.controller.state().policyReloadRequired, false);
  assert.match(x.controller.state().success ?? '', /読み直しました/);
  await x.controller.saveStandard(standardInput(x.latest()));
  assert.equal(x.counts().standardWrites, 1);
});
test('metadata authorization denial after an acknowledged stop purges every private view', async () => {
  const x = setup();
  await x.controller.load();
  x.readError('access_denied');
  assert.equal(await x.controller.saveCustom(stopInput(x.latest())), true);
  assert.equal(x.controller.state().status, 'denied');
  assert.equal(x.controller.state().policy, null);
  assert.equal(x.controller.state().selection, null);
  assert.equal(x.controller.state().runtime, null);
  assert.equal(x.counts().denied, 1);
});
test('a new active profile returned after stopping the previous profile requires a complete status read', async () => {
  const x = setup();
  await x.controller.load();
  const original = x.api.processing.savePolicy;
  x.api.processing.savePolicy = async (input) => {
    const ack = await original(input, async () => 't');
    const id = '55555555-5555-4555-8555-555555555555';
    x.setLatest({
      ...x.latest(),
      runtime_version: 2,
      profile: { ...x.latest().profile!, id },
      policy: { ...x.latest().policy!, profile_id: id, enabled: true },
    });
    return ack;
  };
  await x.controller.saveCustom(stopInput(x.latest()));
  assert.equal(
    x.controller.state().policy?.profile?.id,
    '55555555-5555-4555-8555-555555555555',
  );
  assert.equal(x.controller.state().policyReloadRequired, true);
  assert.match(x.controller.state().success ?? '', /有効版|設定.*変/);
  assert.equal(
    await x.controller.saveStandard(standardInput(x.latest())),
    false,
  );
  await x.controller.refresh();
  assert.equal(x.controller.state().policyReloadRequired, true);
  x.state.runtime.version = 2;
  x.state.runtime.active = {
    ...x.state.runtime.active!,
    id: x.latest().profile!.id,
  };
  x.state.runtime.draft = x.state.runtime.active;
  await x.controller.refresh();
  assert.equal(x.controller.state().policyReloadRequired, false);
});
test('stopping remains possible with unavailable route metadata and credentials', async () => {
  const x = setup();
  x.state.routes.key_available = false;
  x.state.routes.fresh = false;
  await x.controller.load();
  x.readError('dependency_unavailable');
  assert.equal(await x.controller.saveCustom(stopInput(x.latest())), true);
  assert.equal(x.counts().legacyWrites, 1);
  assert.equal(x.controller.state().policy?.policy?.enabled, false);
});
test('a failed optional route-list read still exposes the existing permission for stopping', async () => {
  const x = setup();
  x.api.routes.list = async () => {
    throw new ApiError('dependency_unavailable');
  };
  await x.controller.load();
  assert.ok(x.controller.state().policy?.policy?.enabled);
  assert.equal(await x.controller.saveCustom(stopInput(x.latest())), true);
  assert.equal(x.counts().legacyWrites, 1);
  assert.equal(x.controller.state().policy?.policy?.enabled, false);
});
test('legacy direct-provider custom conditions retain the explicit custom path after stopping', async () => {
  const x = setup();
  const current = x.latest();
  const { standard_terms: _terms, ...legacy } = current;
  x.setLatest({
    ...legacy,
    profile: {
      ...legacy.profile!,
      secondary: {
        ...legacy.profile!.secondary,
        provider: 'openai',
        routing_provider: '',
      },
    },
    policy: {
      ...legacy.policy!,
      standard_proof: undefined,
      primary_retention: '一次の個別契約',
      secondary_retention: '二次の個別契約',
    },
  });
  await x.controller.load();
  await x.controller.saveCustom(stopInput(x.latest()));
  assert.equal(x.controller.state().policyReloadRequired, false);
  const review = createStandardReview(x.controller.state().policy!);
  assert.equal(review.draft.values.primary_mode, 'custom');
  assert.equal(review.draft.values.primary_retention, '一次の個別契約');
  assert.equal(review.draft.values.primary_confirmed, false);
  await x.controller.saveCustom({
    ...stopInput(x.latest()),
    enabled: true,
    primary_confirmed: true,
    secondary_confirmed: true,
  });
  assert.equal(x.counts().legacyWrites, 2);
  assert.equal(x.counts().standardWrites, 0);
});
