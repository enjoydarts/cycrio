import assert from 'node:assert/strict';
import test from 'node:test';
import { parseProfile, parseTestRun } from './airuntime-api.ts';
import {
  parsePolicyState,
  parseProcessingPreview,
  createMailProcessingAPI,
} from './mail-processing-api.ts';
import { profile, run, id, now } from './airuntime-test-fixtures.ts';
import {
  processingPolicy,
  processingPreview,
  processingIDs,
} from './mail-processing-fixtures.ts';
const proof = () => ({
  ambiguous: false,
  id,
  snapshot_id: id,
  captured_at: now,
  source_url: 'https://openrouter.ai/api/v1/endpoints/zdr',
  upstream_model_id: 'a/b',
  model_name: 'Model',
  provider_name: 'DeepInfra',
  tag: 'deepinfra/fp4',
  service_tier: '',
  quantization: 'fp4',
  context_length: 0,
  max_prompt_tokens: null,
  max_completion_tokens: 0,
  supported_parameters: [],
  status: -1,
  zdr: true,
  price: null,
  pricing_hash: 'a'.repeat(64),
});
test('standard permission client only sends the ten consent fields and rejects incomplete fresh confirmation', async () => {
  const calls: RequestInit[] = [];
  const api = createMailProcessingAPI(
    'https://example.test',
    async (_url, init) => {
      calls.push(init!);
      return Response.json(processingPolicy());
    },
  );
  const input = {
    expected_version: 1,
    runtime_version: 2,
    profile_id: processingIDs.profile,
    enabled: true,
    primary_mode: 'standard' as const,
    primary_terms_id: 'liquid-standard-2026-09-30',
    primary_retention: '',
    primary_confirmed: true,
    secondary_route_id: id,
    secondary_confirmed: true,
  };
  await assert.rejects(
    api.saveStandardPolicy(
      { ...input, primary_confirmed: false },
      async () => 't',
    ),
    { code: 'invalid_request' },
  );
  assert.equal(calls.length, 0);
  const extraInput = { ...input, actor_id: 'must-not-send' };
  await api.saveStandardPolicy(extraInput, async () => 't');
  assert.deepEqual(JSON.parse(String(calls[0].body)), input);
  assert.equal(calls[0].cache, 'no-store');
  assert.equal(calls[0].credentials, 'omit');
});
test('runtime preserves immutable exact route proof and independent safe stage failures', () => {
  assert.deepEqual(
    parseProfile({ ...profile(), route_proof: proof() }).route_proof,
    proof(),
  );
  const failure = {
    http_status: 402,
    reason: 'payment_required',
    hint: '入金を確認してください。',
  };
  const parsed = parseTestRun({
    ...run(),
    primary_failure: null,
    secondary_failure: failure,
  });
  assert.equal(parsed.primary_failure, null);
  assert.deepEqual(parsed.secondary_failure, failure);
});
test('direct and custom secondary consent serialize optional fields without requiring a route', async () => {
  const bodies: unknown[] = [];
  const api = createMailProcessingAPI(
    'https://example.test',
    async (_url, init) => {
      bodies.push(JSON.parse(String(init!.body)));
      return Response.json(processingPolicy());
    },
  );
  const input = {
    expected_version: 1,
    runtime_version: 2,
    profile_id: processingIDs.profile,
    enabled: true,
    primary_mode: 'standard' as const,
    primary_terms_id: 'liquid-standard',
    primary_retention: '',
    primary_confirmed: true,
    secondary_mode: 'standard' as const,
    secondary_terms_id: 'openai-standard',
    secondary_retention: '',
    secondary_route_id: '',
    secondary_confirmed: true,
  };
  await api.saveStandardPolicy(input, async () => 't');
  assert.deepEqual(bodies[0], input);
  await api.saveStandardPolicy(
    {
      ...input,
      secondary_mode: 'custom',
      secondary_terms_id: '',
      secondary_retention: '個別契約の保持条件',
    },
    async () => 't',
  );
  for (const patch of [
    { secondary_confirmed: false },
    { secondary_terms_id: '' },
    { secondary_retention: 'unexpected' },
  ]) {
    await assert.rejects(
      api.saveStandardPolicy({ ...input, ...patch }, async () => 't'),
      { code: 'invalid_request' },
    );
  }
  assert.equal(bodies.length, 2);
});
test('direct standard terms and nullable proof route parse alongside legacy route proofs', () => {
  const terms = {
    id: 'openai-standard',
    version: '1',
    source_url: 'https://example.test/terms',
    summary: '標準条件',
    zdr: false,
    training_permitted: false,
    fixed_retention_days: 30,
  };
  const state = processingPolicy();
  state.standard_terms = {
    primary: terms,
    secondary: terms,
    secondary_route: null,
    ready: true,
    blocked_reason: '',
  };
  state.policy!.standard_proof = {
    profile_id: processingIDs.profile,
    policy_version: 1,
    primary_mode: 'standard',
    primary_terms: terms,
    primary_retention: '',
    secondary_mode: 'standard',
    secondary_terms: terms,
    secondary_route: null,
    secondary_retention: '',
    actor_id: id,
    confirmed_at: now,
  };
  const parsed = parsePolicyState(state);
  assert.deepEqual(parsed.standard_terms!.secondary, terms);
  assert.equal(parsed.policy!.standard_proof!.secondary_route, null);
  assert.deepEqual(parsed.policy!.standard_proof!.secondary_terms, terms);
  state.policy!.standard_proof!.secondary_route = proof();
  delete state.policy!.standard_proof!.secondary_mode;
  delete state.policy!.standard_proof!.secondary_terms;
  assert.deepEqual(
    parsePolicyState(state).policy!.standard_proof!.secondary_route,
    proof(),
  );
});
test('HTTP 400 request rejection guidance parses without weakening status validation', () => {
  const failure = {
    http_status: 400,
    reason: 'request_rejected',
    hint: '送信形式を確認してください。',
  };
  assert.deepEqual(
    parseTestRun({ ...run(), secondary_failure: failure }).secondary_failure,
    failure,
  );
  for (const patch of [{ http_status: 401 }, { reason: 'unknown_reason' }]) {
    assert.throws(
      () =>
        parseTestRun({ ...run(), secondary_failure: { ...failure, ...patch } }),
      { code: 'dependency_unavailable' },
    );
  }
});
test('policy preserves standard terms and public reconfirmation state', () => {
  const terms = {
    id: 'liquid-standard-2026-09-30',
    version: '2026-09-30',
    source_url: 'https://www.liquid.ai/terms-conditions',
    summary: '標準保持・学習利用可能・固定日数なし',
    zdr: false,
    training_permitted: true,
    fixed_retention_days: null,
  };
  const state = {
    ...processingPolicy(),
    standard_terms: {
      primary: terms,
      secondary_route: proof(),
      ready: true,
      blocked_reason: '',
    },
  };
  assert.deepEqual(
    parsePolicyState(state).standard_terms,
    state.standard_terms,
  );
  assert.equal(
    parseProcessingPreview(
      {
        ...processingPreview(),
        ready: false,
        blocked_reason: 'policy_reconfirmation_required',
      },
      processingIDs.account,
    ).blocked_reason,
    'policy_reconfirmation_required',
  );
});
