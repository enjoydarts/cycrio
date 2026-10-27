import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { ApiError } from './api.ts';
import {
  createMailProcessingAPI,
  parseProcessingPreview,
  parseProcessingRun,
  parseProcessingResult,
  parsePolicyState,
} from './mail-processing-api.ts';
import {
  processingIDs as ids,
  processingPreview,
  processingRun,
  processingResult,
  processingPolicy,
} from './mail-processing-fixtures.ts';
test('public DTO parsers whitelist fields, preserve null pricing and decimal extraction without inventing values', () => {
  const preview = processingPreview();
  const parsed = parseProcessingPreview(
    {
      ...preview,
      owner_id: 'private',
      primary: {
        ...preview.primary,
        credential_version_id: 'secret',
        workspace_id: 'secret',
      },
    },
    ids.account,
  );
  assert.deepEqual(parsed, preview);
  const result = processingResult({
    state: 'succeeded',
    classification: 'subscription_related',
    error_code: '',
    events: [
      {
        service: 'サンプル',
        plan: null,
        event_type: 'invoice',
        amount: '0.000000000000000001',
        currency: 'JPY',
        billed_at: '2026-10-25',
        period_start: null,
        period_end: null,
        next_billing_at: null,
        interval_unit: null,
        interval_count: null,
      },
    ],
  });
  assert.deepEqual(
    parseProcessingResult({
      ...result,
      body: 'private',
      events: result.events.map((event) => ({ ...event, key: 'secret' })),
    }),
    result,
  );
  assert.deepEqual(
    parsePolicyState({ ...processingPolicy(), credential: 'secret' }),
    processingPolicy(),
  );
});
test('DTO parsers reject malformed types, enums, nil UUID, impossible date, excessive counts, decimal and routing errors', () => {
  for (const patch of [
    { ready: 1 },
    { account_id: ids.profile },
    { profile_id: '00000000-0000-0000-0000-000000000000' },
    { blocked_reason: 'private upstream error' },
    { pending_count: -1 },
    { runtime_version: 1.5 },
    { primary: undefined },
    { secondary: { ...processingPreview().secondary, routing_provider: '' } },
    { primary: { ...processingPreview().primary, routing_provider: 'openai' } },
  ])
    assert.throws(
      () =>
        parseProcessingPreview(
          { ...processingPreview(), ...patch },
          ids.account,
        ),
      ApiError,
    );
  for (const patch of [
    { state: 'limited' },
    { max_messages: 101 },
    { completed_count: 3 },
    { created_at: '2026-02-30T00:00:00Z' },
    { started_at: undefined },
  ])
    assert.throws(
      () => parseProcessingRun({ ...processingRun(), ...patch }, ids.account),
      ApiError,
    );
  for (const patch of [
    { state: 'complete' },
    { classification: 'yes' },
    { error_code: 'provider raw error' },
    { events: null },
  ])
    assert.throws(
      () => parseProcessingResult({ ...processingResult(), ...patch }),
      ApiError,
    );
  const event = {
    service: 's',
    plan: null,
    event_type: 'payment',
    amount: null,
    currency: null,
    billed_at: null,
    period_start: null,
    period_end: null,
    next_billing_at: null,
    interval_unit: null,
    interval_count: null,
  };
  for (const patch of [
    { amount: 10 },
    { amount: '1e-2' },
    { amount: '1234567890123456789' },
    { currency: 'ZZZ' },
    { billed_at: '2026-02-30' },
    { interval_count: 10001 },
    { service: 'a'.repeat(129) },
    { interval_unit: 'monthly' },
  ])
    assert.throws(
      () =>
        parseProcessingResult({
          ...processingResult(),
          events: [{ ...event, ...patch }],
        }),
      ApiError,
    );
});
test('API obtains a fresh token, omits cookies, honors abort and sends only allowed fields', async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  let tokens = 0;
  const api = createMailProcessingAPI(
    'https://api.example/',
    async (url, init) => {
      calls.push({ url: String(url), init: init! });
      return Response.json(
        String(url).endsWith('processing-preview')
          ? processingPreview()
          : String(url).endsWith('processing-runs') && init?.method === 'GET'
            ? { runs: [] }
            : processingRun(),
      );
    },
  );
  const token = async () => `fresh-${++tokens}`;
  await api.preview(ids.account, token);
  await api.runs(ids.account, token);
  const input = {
    expected_version: 1,
    runtime_version: 2,
    profile_id: ids.profile,
    policy_version: 3,
    request_id: ids.request,
    max_messages: 2,
    confirmed: true,
    key: 'must not send',
  };
  await api.start(ids.account, input, token);
  assert.equal(tokens, 3);
  assert.equal(calls[0].init.credentials, 'omit');
  assert.equal(calls[0].init.cache, 'no-store');
  assert.equal(
    new Headers(calls[2].init.headers).get('Authorization'),
    'Bearer fresh-3',
  );
  assert.deepEqual(JSON.parse(String(calls[2].init.body)), {
    expected_version: 1,
    runtime_version: 2,
    profile_id: ids.profile,
    policy_version: 3,
    request_id: ids.request,
    max_messages: 2,
    confirmed: true,
  });
  const abort = new AbortController();
  abort.abort();
  await assert.rejects(api.preview(ids.account, token, abort.signal), {
    name: 'AbortError',
  });
  assert.equal(calls.length, 3);
  await assert.rejects(
    api.start(ids.account, { ...input, max_messages: 0 }, token),
    ApiError,
  );
});
test('actual Go-serialized synthetic DTOs parse without dropping price precision or nullable extraction fields', async () => {
  const fixtures = JSON.parse(
    await readFile(
      new URL('./mail-processing-go-fixtures.json', import.meta.url),
      'utf8',
    ),
  );
  const preview = parseProcessingPreview(fixtures.preview, fixtures.account.id);
  assert.equal(preview.secondary?.price?.rates.input.amount, '0.0000001');
  assert.equal(preview.secondary?.price?.rates.input.unit, 'token');
  assert.deepEqual(
    parseProcessingRun(fixtures.queued, fixtures.account.id),
    fixtures.queued,
  );
  assert.deepEqual(
    parseProcessingRun(fixtures.completed, fixtures.account.id),
    fixtures.completed,
  );
  for (const name of ['related', 'unrelated', 'failed', 'unknown'])
    assert.deepEqual(parseProcessingResult(fixtures[name]), fixtures[name]);
  const policy = parsePolicyState(fixtures.policy);
  assert.equal(policy.profile?.secondary.price?.currency, 'USD');
  assert.equal(policy.policy?.enabled, true);
});
test('policy accepts initial version zero and inactive state but rejects malformed enabled policy or excessive/control retentions', () => {
  const initial = processingPolicy({
    policy: { ...processingPolicy().policy!, version: 0 },
  });
  assert.equal(parsePolicyState(initial).policy?.version, 0);
  assert.deepEqual(
    parsePolicyState({ runtime_version: 0, profile: null, policy: null }),
    { runtime_version: 0, profile: null, policy: null },
  );
  for (const patch of [
    { enabled: 'true' },
    { primary_confirmed: 1 },
    { primary_retention: 'x\nsecret' },
    { secondary_retention: 'x'.repeat(501) },
    { enabled: true, primary_confirmed: false },
  ])
    assert.throws(
      () =>
        parsePolicyState({
          ...processingPolicy(),
          policy: { ...processingPolicy().policy, ...patch },
        }),
      ApiError,
    );
});
test('retry, cancel, result and policy routes use exact public bodies and fixed HTTP errors without reading upstream text', async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  const api = createMailProcessingAPI(
    'https://api.example',
    async (url, init) => {
      calls.push({ url: String(url), init: init! });
      return Response.json(
        String(url).endsWith('/policy')
          ? processingPolicy()
          : String(url).endsWith('processing-results')
            ? { results: [processingResult()] }
            : processingRun(),
      );
    },
  );
  const input = {
    expected_version: 1,
    runtime_version: 2,
    profile_id: ids.profile,
    policy_version: 3,
    request_id: ids.request,
    max_messages: 1,
    confirmed: true,
    expected_result_id: ids.result,
    confirm_unknown: true,
    body: 'private',
  };
  const signal = new AbortController().signal,
    token = async () => 'fresh';
  await api.retry(ids.account, ids.message, input, token, signal);
  await api.cancel(ids.account, ids.run, token, signal);
  await api.results(ids.account, token, signal);
  await api.policy(token, signal);
  await api.savePolicy(
    {
      ...processingPolicy().policy!,
      expected_version: 3,
      key: 'private',
    } as Parameters<typeof api.savePolicy>[0],
    token,
    signal,
  );
  assert.equal(
    calls[0].url,
    `https://api.example/v1/mail/accounts/${ids.account}/processing-results/${ids.message}/retry`,
  );
  const { body, ...expected } = input;
  void body;
  assert.deepEqual(JSON.parse(String(calls[0].init.body)), expected);
  assert.deepEqual(JSON.parse(String(calls[1].init.body)), {});
  assert.deepEqual(
    Object.keys(JSON.parse(String(calls[4].init.body))).sort(),
    [
      'expected_version',
      'full_email_allowed',
      'profile_id',
      'enabled',
      'primary_retention',
      'secondary_retention',
      'primary_confirmed',
      'secondary_confirmed',
    ].sort(),
  );
  for (const call of calls) {
    assert.equal(call.init.credentials, 'omit');
    assert.equal(call.init.signal, signal);
  }
  for (const [status, code] of [
    [400, 'invalid_request'],
    [401, 'authentication_required'],
    [403, 'access_denied'],
    [404, 'not_found'],
    [409, 'conflict'],
    [503, 'dependency_unavailable'],
  ] as const) {
    const bad = createMailProcessingAPI(
      '',
      async () => new Response('private provider output', { status }),
    );
    await assert.rejects(
      bad.preview(ids.account, token),
      (error) =>
        error instanceof ApiError &&
        error.code === code &&
        !error.message.includes('private'),
    );
  }
});
