import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createAIRoutesAPI,
  parseRoutes,
  parseEndpointPrice,
} from './ai-routes-api.ts';
import { id, now } from './airuntime-test-fixtures.ts';
import { routeFixture, routesFixture } from './ai-setup-test-fixtures.ts';
test('route metadata accepts unknown zero/negative facts and endpoint quote without catalog UUID', () => {
  const r = routeFixture();
  assert.equal(
    parseRoutes({
      ...routesFixture(),
      routes: [{ ...r, status: -1, context_length: 0 }],
    }).routes[0].status,
    -1,
  );
  const price = {
    id,
    model_id: '',
    source: 'api',
    currency: 'USD',
    rates: {},
    raw_pricing: null,
    effective_from: null,
    captured_at: now,
    source_url: r.source_url,
  };
  assert.equal(parseEndpointPrice(price).model_id, '');
  assert.throws(() => parseEndpointPrice({ ...price, model_id: 'bad' }));
  assert.throws(() =>
    parseRoutes({ ...routesFixture(), routes: [{ ...r, eligible: 'true' }] }),
  );
});
test('refresh details preserve nil Go supported-parameter slices for unavailable rows', () => {
  const result = {
    ...routesFixture(),
    routes: [
      {
        ...routeFixture(),
        eligible: false,
        supported_parameters: null,
        reasons: ['response_format_required'],
      },
    ],
  };
  assert.equal(parseRoutes(result).routes[0].supported_parameters, null);
});
test('route metadata rejects impossible capture dates and nil identities', () => {
  assert.throws(() =>
    parseRoutes({
      ...routesFixture(),
      snapshot: {
        ...routesFixture().snapshot,
        captured_at: '2026-02-30T00:00:00Z',
      },
    }),
  );
  assert.throws(() =>
    parseRoutes({
      ...routesFixture(),
      routes: [
        { ...routeFixture(), id: '00000000-0000-0000-0000-000000000000' },
      ],
    }),
  );
});
test('route client authenticates private no-store calls and safely encodes search', async () => {
  const calls: { url: string; init: RequestInit | undefined }[] = [];
  const api = createAIRoutesAPI('https://example.test/', async (url, init) => {
    calls.push({ url: String(url), init });
    return Response.json(routesFixture());
  });
  const controller = new AbortController();
  await api.list(
    { search: 'a&b/日本語', offset: 100 },
    async () => 'token',
    controller.signal,
  );
  await api.refresh(async () => 'token');
  assert.equal(new URL(calls[0].url).searchParams.get('search'), 'a&b/日本語');
  assert.equal(calls[0].init?.cache, 'no-store');
  assert.equal(calls[0].init?.credentials, 'omit');
  assert.equal(calls[0].init?.signal, controller.signal);
  assert.equal(calls[1].init?.body, '{}');
  assert.equal(calls[1].init?.method, 'POST');
});
