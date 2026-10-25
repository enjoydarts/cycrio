import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  createAIRuntimeAPI,
  parseRuntimeState,
  parseTestRun,
  parseCost,
  parseCall,
  parseCostReport,
} from './airuntime-api.ts';
import {
  id,
  now,
  modelRef,
  profile,
  runtime,
  run,
  usage,
  call,
  report,
} from './airuntime-test-fixtures.ts';

test('runtime DTOs strictly validate nullable fields, enums, UUIDs and timestamps', () => {
  assert.equal(
    parseRuntimeState(runtime()).draft?.primary.credential_version,
    1,
  );
  for (const patch of [
    { version: '1' },
    { active_ready: 1 },
    { active_blocked_reason: undefined },
    { draft: { ...profile(), id: 'bad' } },
    { draft: { ...profile(), created_at: '2026-02-30T00:00:00Z' } },
    { draft: { ...profile(), primary: { ...modelRef(), provider: 'resend' } } },
  ])
    assert.throws(
      () => parseRuntimeState({ ...runtime(), ...patch }),
      ApiError,
    );
  for (const patch of [
    { state: 'secret' },
    { primary_outcome: 'upstream secret' },
    { classification: 'other' },
    { extracted_events: -1 },
    { started_at: undefined },
  ])
    assert.throws(() => parseTestRun({ ...run(), ...patch }), ApiError);
  assert.equal(
    parseRuntimeState({ ...runtime(), active_blocked_reason: 'future_reason' })
      .active_blocked_reason,
    'future_reason',
  );
});
test('runtime price uses Go currency/unit and retains exact decimals', () => {
  const p = {
    id,
    model_id: id,
    source: 'api',
    currency: 'USD',
    rates: { input: { amount: '0.00000000000000012345', unit: 'USD/token' } },
    raw_pricing: null,
    effective_from: null,
    captured_at: now,
    source_url: 'https://example.com',
  };
  const state = runtime();
  state.draft.primary.price = p as never;
  assert.equal(
    parseRuntimeState(state).draft?.primary.price?.rates.input.amount,
    p.rates.input.amount,
  );
});
test('execution availability failures are accepted as fixed unknown outcomes', () => {
  const value = parseTestRun({
    ...run(),
    state: 'unknown',
    primary_outcome: 'execution_unavailable',
    finished_at: now,
  });
  assert.equal(value.state, 'unknown');
  assert.equal(value.primary_outcome, 'execution_unavailable');
  assert.equal(
    parseCall({ ...call(), outcome: 'execution_unavailable' }).outcome,
    'execution_unavailable',
  );
});
test('unknown costs remain null and reported zero remains exact zero', () => {
  assert.equal(parseCost(call().cost).amount, null);
  assert.equal(
    parseCost({
      status: 'provider_reported',
      amount: '0',
      currency: 'USD',
      reason: '',
    }).amount,
    '0',
  );
  for (const value of [
    { status: 'estimated', amount: 0, currency: 'USD', reason: '' },
    { status: 'unknown', amount: '0', currency: null, reason: '' },
    { status: 'estimated', amount: '1e-5', currency: 'USD', reason: '' },
    { status: 'reported', amount: '0', currency: 'USD', reason: '' },
    { status: 'estimated', amount: null, currency: 'USD', reason: '' },
  ])
    assert.throws(() => parseCost(value), ApiError);
  assert.equal(
    'credential_id' in parseCall({ ...call(), credential_id: 'secret' }),
    false,
  );
  for (const patch of [
    { scope: 'other' },
    { status: 'pending' },
    { stage: 'other' },
    { usage: { ...usage(), valid: 'true' } },
    { consuming_user_id: 'bad' },
  ])
    assert.throws(() => parseCall({ ...call(), ...patch }), ApiError);
  assert.equal(parseCostReport(report()).users_total, 0);
  assert.throws(
    () => parseCostReport({ ...report(), month: '2026-13' }),
    ApiError,
  );
});
test('runtime API mounts GET only, authenticates, whitelists mutations and keeps costs read only', async () => {
  const requests: { url: string; init: RequestInit }[] = [];
  const api = createAIRuntimeAPI('https://example.test/', async (url, init) => {
    requests.push({ url: String(url), init: init! });
    return Response.json(
      String(url).endsWith('/runtime/tests') ? run() : runtime(),
    );
  });
  await api.state(async () => 'token');
  await api.save(
    {
      ...profile(),
      expected_version: 1,
      catalog_settings_id: id,
      secret: 'hidden',
    } as never,
    async () => 'token',
  );
  await api.requestTest(
    { expected_version: 1, profile_id: id, request_id: id },
    async () => 'token',
  );
  assert.equal(requests[0].init.method, 'GET');
  assert.equal(
    new Headers(requests[0].init.headers).get('Authorization'),
    'Bearer token',
  );
  assert.deepEqual(
    Object.keys(JSON.parse(String(requests[1].init.body))).sort(),
    [
      'expected_version',
      'catalog_settings_id',
      'max_input_bytes',
      'max_output_tokens',
      'timeout_seconds',
      'openrouter_provider',
    ].sort(),
  );
  assert.equal(
    requests[2].url,
    'https://example.test/v1/admin/ai/runtime/tests',
  );
  assert.equal(JSON.parse(String(requests[2].init.body)).request_id, id);
});
test('runtime API denies absent tokens and recognizes credentials_required without exposing upstream text', async () => {
  const api = createAIRuntimeAPI('', async () =>
    Response.json(
      { code: 'credentials_required', message: 'secret' },
      { status: 400 },
    ),
  );
  await assert.rejects(
    api.state(async () => null),
    { code: 'authentication_required' },
  );
  await assert.rejects(
    api.state(async () => 'token'),
    { code: 'credentials_required' },
  );
});
test('cost filter requests are server paged and reconciliation sends an empty body', async () => {
  const requests: { url: string; init: RequestInit }[] = [];
  const api = createAIRuntimeAPI('', async (url, init) => {
    requests.push({ url: String(url), init: init! });
    return Response.json(
      String(url).includes('/reconcile')
        ? call()
        : String(url).includes('/calls?')
          ? { calls: [], total: 0 }
          : report(),
    );
  });
  await api.costs(
    {
      month: '2026-10',
      user_id: id,
      provider: 'openai',
      model_id: id,
      stage: 'extraction',
      limit: 50,
      offset: 100,
    },
    async () => 'token',
  );
  await api.calls(
    { month: '2026-10', scope: 'admin_test', offset: 50 },
    async () => 'token',
  );
  await api.reconcile(id, async () => 'token');
  const query = new URL(`https://test${requests[0].url}`).searchParams;
  assert.equal(query.get('user_id'), id);
  assert.equal(query.get('offset'), '100');
  assert.equal(query.get('limit'), '50');
  assert.equal(
    new URL(`https://test${requests[1].url}`).searchParams.get('offset'),
    '50',
  );
  assert.deepEqual(JSON.parse(String(requests[2].init.body)), {});
});
test('cancellation during token lookup never sends a stale private request', async () => {
  const abort = new AbortController();
  let fetches = 0;
  const api = createAIRuntimeAPI('', async () => {
    fetches++;
    return Response.json(runtime());
  });
  await assert.rejects(
    api.state(async () => {
      abort.abort();
      return 'token';
    }, abort.signal),
    { name: 'AbortError' },
  );
  assert.equal(fetches, 0);
});
