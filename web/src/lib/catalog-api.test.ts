import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createCatalogAPI,
  parseModel,
  parsePrice,
  parseSync,
  parseSettings,
} from './catalog-api.ts';
import { ApiError } from './api.ts';

import {
  modelFixture,
  settingsFixture,
  syncFixture,
} from './catalog-test-fixtures.ts';
const priceFixture = () => ({
  id: modelFixture().id,
  model_id: modelFixture().id,
  source: 'api',
  currency: 'USD',
  rates: {
    input: { amount: '0.000000000000123456789', unit: 'USD/token' },
    output: { amount: '0', unit: 'USD/token' },
  },
  raw_pricing: { future: { discount: 'conditional' } },
  effective_from: null,
  captured_at: modelFixture().created_at,
  source_url: 'https://example.com/prices',
});

test('catalog DTOs reject coercion, unsafe versions, invalid dates and retain only public fields', () => {
  const value = { ...modelFixture(), secret: 'do-not-keep' };
  assert.equal('secret' in parseModel(value), false);
  for (const invalid of [
    { enabled: 'false' },
    { provider: {} },
    { version: Number.MAX_SAFE_INTEGER + 1 },
    { text_input: 'true' },
    { updated_at: '2026-02-30T00:00:00Z' },
    { raw_metadata: [] },
  ])
    assert.throws(() => parseModel({ ...value, ...invalid }), ApiError);
  assert.throws(
    () => parseSettings({ ...settingsFixture(), version: '0' }),
    ApiError,
  );
  assert.throws(
    () => parseSync({ ...syncFixture(), interval_hours: 169 }),
    ApiError,
  );
  assert.throws(() => parseSync({ ...syncFixture(), status: {} }), ApiError);
});
test('prices preserve exact decimals including explicit zero and reject numeric money', () => {
  const price = parsePrice(priceFixture());
  assert.equal(price.rates.input.amount, '0.000000000000123456789');
  assert.equal(price.rates.output.amount, '0');
  assert.deepEqual(parsePrice({ ...priceFixture(), rates: {} }).rates, {});
  assert.throws(
    () =>
      parsePrice({
        ...priceFixture(),
        rates: { input: { amount: 0, unit: 'token' } },
      }),
    ApiError,
  );
  assert.throws(
    () =>
      parsePrice({
        ...priceFixture(),
        rates: { input: { amount: '-1', unit: 'token' } },
      }),
    ApiError,
  );
  assert.throws(
    () =>
      parsePrice({
        ...priceFixture(),
        rates: { input: { amount: '1', unit: 'JPY/token' } },
      }),
    ApiError,
  );
});
test('catalog requests use fresh bearer, safe transport, server paging and whitelisted update payload', async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  let n = 0;
  const api = createCatalogAPI('https://api.example/', async (url, init) => {
    calls.push({ url: String(url), init: init! });
    return Response.json(
      calls.length === 1
        ? { models: [modelFixture()], total: 123 }
        : modelFixture(),
    );
  });
  const token = async () => `token-${++n}`;
  const page = await api.list(
    { provider: 'liquid', search: '日本語', offset: 20 },
    token,
  );
  assert.equal(page.total, 123);
  assert.match(calls[0].url, /limit=20&offset=20/);
  assert.equal(new URL(calls[0].url).searchParams.get('search'), '日本語');
  await api.update(
    modelFixture().id,
    {
      expected_version: 1,
      display_name: '',
      enabled: false,
      text_input: null,
      structured_output: null,
    },
    token,
  );
  assert.equal(calls[0].init.credentials, 'omit');
  assert.equal(calls[0].init.cache, 'no-store');
  assert.equal(
    new Headers(calls[1].init.headers).get('Authorization'),
    'Bearer token-2',
  );
  assert.deepEqual(JSON.parse(calls[1].init.body as string), {
    expected_version: 1,
    display_name: '',
    enabled: false,
    text_input: null,
    structured_output: null,
  });
});
test('status errors ignore upstream response bodies', async () => {
  for (const [status, code] of [
    [401, 'authentication_required'],
    [403, 'access_denied'],
    [409, 'conflict'],
    [500, 'dependency_unavailable'],
  ] as const) {
    const api = createCatalogAPI(
      '',
      async () => new Response('secret-provider-detail', { status }),
    );
    await assert.rejects(
      api.settings(async () => 'token'),
      (error: unknown) =>
        error instanceof ApiError &&
        error.code === code &&
        !error.message.includes('secret'),
    );
  }
});
