import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  createCatalogChangesAPI,
  parseCatalogChanges,
  safeCatalogSource,
  describeCatalogChanges,
} from './catalog-changes-api.ts';
import { createCatalogChangesController } from './catalog-changes-controller.ts';

const id = '11111111-1111-4111-8111-111111111111';
const snapshot = {
  display_name: 'Future',
  enabled: true,
  available: true,
  text_input: null,
  structured_output: null,
  context_length: 200000,
  max_input_tokens: null,
  max_output_tokens: 8192,
  price: null,
};
const change = {
  id,
  model_id: id,
  provider: 'openai',
  model_name: 'Future',
  upstream_model_id: 'future',
  kind: 'updated',
  occurred_at: '2026-11-01T00:00:00Z',
  source_url: 'https://openai.com/api/pricing/',
  before: snapshot,
  after: { ...snapshot, context_length: 400000, available: false },
};
const page = (offset = 0) => ({
  changes: [change],
  total: 45,
  limit: 20,
  offset,
});
const token = async () => 'token';

test('catalog changes request filters and pagination through the admin read-only route', async () => {
  let address = '',
    init: RequestInit | undefined;
  const api = createCatalogChangesAPI(
    'https://api.example/',
    async (url, options) => {
      address = String(url);
      init = options;
      return Response.json(page(20));
    },
  );
  const result = await api.list(
    { provider: 'openai', kind: 'price_changed', model_id: id, offset: 20 },
    token,
  );
  const url = new URL(address);
  assert.equal(url.pathname, '/v1/admin/ai/catalog-changes');
  assert.equal(url.searchParams.get('provider'), 'openai');
  assert.equal(url.searchParams.get('kind'), 'price_changed');
  assert.equal(url.searchParams.get('model_id'), id);
  assert.equal(url.searchParams.get('offset'), '20');
  assert.equal(url.searchParams.get('limit'), '20');
  assert.equal(init?.credentials, 'omit');
  assert.equal(init?.cache, 'no-store');
  assert.equal(result.total, 45);
});

test('catalog changes parser rejects malformed snapshots while safe source links require official HTTPS hosts', () => {
  assert.equal(
    parseCatalogChanges(page()).changes[0].before?.context_length,
    200000,
  );
  for (const value of [
    { ...page(), total: -1 },
    { ...page(), changes: [{ ...change, kind: 'unknown' }] },
    {
      ...page(),
      changes: [{ ...change, after: { ...snapshot, enabled: 'yes' } }],
    },
  ])
    assert.throws(() => parseCatalogChanges(value), ApiError);
  assert.equal(
    safeCatalogSource('https://openai.com/api/pricing/'),
    'https://openai.com/api/pricing/',
  );
  for (const value of [
    'javascript:alert(1)',
    'https://evil.example/',
    'https://openai.com.evil.example/',
    'https://secret@openai.com/',
    'http://openai.com/',
    'https://openai.com:8443/',
    'https://openai.com/?key=SECRET',
  ])
    assert.equal(safeCatalogSource(value), null);
});

test('catalog changes describe real changed fields and token prices without exposing raw objects', () => {
  const lines = describeCatalogChanges(parseCatalogChanges(page()).changes[0]);
  assert.deepEqual(
    lines.map((line) => line.label),
    ['利用可能', 'コンテキスト長'],
  );
  assert.deepEqual(lines[0], {
    label: '利用可能',
    before: 'はい',
    after: 'いいえ',
  });
  const priced = parseCatalogChanges({
    ...page(),
    changes: [
      {
        ...change,
        kind: 'price_changed',
        after: {
          ...snapshot,
          price: {
            currency: 'USD',
            source: 'research',
            source_url: '',
            rates: { input: { amount: '0.00000125', unit: 'USD/token' } },
          },
        },
      },
    ],
  }).changes[0];
  assert.match(
    describeCatalogChanges(priced).find((line) => line.label === '料金')!.after,
    /1.25.*100万トークン/,
  );
});

test('catalog changes controller resets filter pages and drops stale or unauthorized responses', async () => {
  const pending: ((value: unknown) => void)[] = [];
  const api = createCatalogChangesAPI(
    '',
    async () =>
      new Promise((resolve) =>
        pending.push((value) => resolve(Response.json(value))),
      ),
  );
  let denied = 0;
  const controller = createCatalogChangesController(
    api,
    token,
    () => {},
    () => denied++,
  );
  const first = controller.load({ offset: 20 });
  await Promise.resolve();
  const second = controller.load({ provider: 'anthropic' });
  await Promise.resolve();
  pending[1](page());
  await second;
  pending[0](page(20));
  await first;
  assert.equal(controller.state.filter.provider, 'anthropic');
  assert.equal(controller.state.offset, 0);
  controller.dispose();
  assert.equal(controller.state.status, 'disposed');
  const deniedAPI = createCatalogChangesAPI(
    '',
    async () => new Response('', { status: 403 }),
  );
  const guarded = createCatalogChangesController(
    deniedAPI,
    token,
    () => {},
    () => denied++,
  );
  await guarded.load();
  assert.equal(guarded.state.status, 'denied');
  assert.equal(guarded.state.changes.length, 0);
  assert.equal(denied, 1);
});

test('catalog changes filter failures hide previous results rather than mislabeling old rows', async () => {
  let unavailable = false;
  const api = createCatalogChangesAPI('', async () =>
    unavailable ? new Response('', { status: 503 }) : Response.json(page()),
  );
  const controller = createCatalogChangesController(
    api,
    token,
    () => {},
    () => {},
  );
  await controller.load();
  assert.equal(controller.state.changes.length, 1);
  unavailable = true;
  await controller.load({ provider: 'liquid' });
  assert.equal(controller.state.status, 'error');
  assert.equal(controller.state.changes.length, 0);
  assert.equal(controller.state.total, 0);
});
