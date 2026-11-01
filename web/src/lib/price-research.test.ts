import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createCatalogAPI, parseModel, parsePrice } from './catalog-api.ts';
import { modelFixture } from './catalog-test-fixtures.ts';
import {
  createPriceResearchAPI,
  parseResearchState,
  parseResearchRun,
  type ResearchState,
  type ResearchRun,
} from './price-research-api.ts';
import { createPriceResearchController } from './price-research-controller.ts';
import { createCatalogController } from './model-catalog.ts';

const id = '11111111-1111-4111-8111-111111111111';
const now = '2026-11-01T00:00:00Z';
const state = (values: Partial<ResearchState> = {}): ResearchState => ({
  version: 1,
  provider: 'openai',
  model_id: id,
  automatic_enabled: false,
  interval_hours: 24,
  next_run_at: null,
  ready: false,
  latest_run: null,
  ...values,
});
const run = (values: Partial<ResearchRun> = {}): ResearchRun => ({
  id,
  kind: 'test',
  target_provider: 'openai',
  state: 'queued',
  error_code: '',
  created_at: now,
  started_at: null,
  finished_at: null,
  updated_count: 0,
  unresolved_count: 0,
  call_id: null,
  sources: [],
  ...values,
});
const token = async () => 'token';

test('research API uses CAS and the dedicated routes with sanitized bodies', async () => {
  const requests: [string, RequestInit | undefined][] = [];
  const api = createPriceResearchAPI(
    'https://api.example/',
    async (url, init) => {
      requests.push([String(url), init]);
      return Response.json(
        init?.method === 'POST' || String(url).includes('/runs/')
          ? run()
          : state(),
      );
    },
  );
  await api.load(token);
  await api.save(
    {
      expected_version: 1,
      model_id: id,
      automatic_enabled: true,
      interval_hours: 48,
    },
    token,
  );
  await api.test(1, token);
  await api.update(1, 'anthropic', token);
  await api.run(id, token);
  assert.deepEqual(
    requests.map(([url]) => url),
    [
      'https://api.example/v1/admin/ai/price-research',
      'https://api.example/v1/admin/ai/price-research',
      'https://api.example/v1/admin/ai/price-research/tests',
      'https://api.example/v1/admin/ai/price-research/runs',
      `https://api.example/v1/admin/ai/price-research/runs/${id}`,
    ],
  );
  assert.deepEqual(JSON.parse(String(requests[1][1]?.body)), {
    expected_version: 1,
    model_id: id,
    automatic_enabled: true,
    interval_hours: 48,
  });
  assert.deepEqual(JSON.parse(String(requests[3][1]?.body)), {
    expected_version: 1,
    target_provider: 'anthropic',
  });
  assert.equal(requests[0][1]?.cache, 'no-store');
  assert.equal(requests[0][1]?.credentials, 'omit');
});
test('research parser rejects unsafe sources and malformed state rather than rendering provider payloads', () => {
  assert.equal(parseResearchState(state()).interval_hours, 24);
  assert.equal(parseResearchRun(run({ state: 'unknown' })).state, 'unknown');
  for (const interval_hours of [0, 169, 1.5])
    assert.throws(
      () => parseResearchState(state({ interval_hours })),
      ApiError,
    );
  for (const source of [
    'javascript:alert(1)',
    'https://evil.example/pricing',
    'https://openai.com@evil.example/',
    'https://openai.com:444/',
  ])
    assert.throws(() => parseResearchRun(run({ sources: [source] })), ApiError);
  assert.deepEqual(
    parseResearchRun(run({ sources: ['https://openai.com/api/pricing/'] }))
      .sources,
    ['https://openai.com/api/pricing/'],
  );
});
test('research prices and optional confirmation dates retain unknown prices', () => {
  assert.equal(
    parseModel({ ...modelFixture(), research_checked_at: now })
      .research_checked_at,
    now,
  );
  assert.equal(parseModel(modelFixture()).research_checked_at, undefined);
  assert.equal(
    parsePrice({
      id,
      model_id: id,
      source: 'research',
      currency: 'USD',
      rates: {},
      raw_pricing: null,
      effective_from: null,
      captured_at: now,
      source_url: 'https://openai.com/api/pricing/',
    }).source,
    'research',
  );
});

function fixture(
  overrides: Partial<ReturnType<typeof createPriceResearchAPI>> = {},
) {
  let current = state();
  let pending = run();
  const writes: unknown[] = [];
  let denied = 0;
  const api = {
    ...createPriceResearchAPI(''),
    load: async () => current,
    save: async (input: unknown) => {
      writes.push(input);
      current = state({ version: 2 });
      return current;
    },
    test: async () => {
      writes.push('test');
      return pending;
    },
    update: async () => {
      writes.push('update');
      return pending;
    },
    run: async () => pending,
    ...overrides,
  };
  const catalog = {
    ...createCatalogAPI(''),
    list: async () => ({
      models: [parseModel({ ...modelFixture(), provider: 'openai' })],
      total: 1,
    }),
  };
  const controller = createPriceResearchController(
    api,
    catalog,
    token,
    () => {},
    () => {
      denied++;
    },
  );
  return {
    controller,
    writes,
    denied: () => denied,
    setCurrent: (value: ResearchState) => {
      current = value;
    },
    setRun: (value: ResearchRun) => {
      pending = value;
    },
  };
}
test('candidate lookup keeps models without prices and excludes disabled models', async () => {
  const f = fixture();
  await f.controller.load();
  await f.controller.search('openai', '');
  assert.equal(f.controller.state.models[0].current_price, null);
  assert.equal(f.controller.state.models[0].id, id);
});
test('save retains original CAS revision and draft on conflict and requires explicit reload', async () => {
  const f = fixture({
    save: async () => {
      throw new ApiError('conflict');
    },
  });
  await f.controller.load();
  f.controller.edit({ interval_hours: 48 });
  f.setCurrent(state({ version: 2, interval_hours: 72 }));
  await f.controller.save();
  assert.equal(f.controller.state.error, 'conflict');
  assert.equal(f.controller.state.draft?.expected_version, 1);
  assert.equal(f.controller.state.draft?.interval_hours, 48);
  assert.equal(f.controller.state.saved?.version, 2);
  await f.controller.load(true);
  assert.equal(f.controller.state.draft?.interval_hours, 72);
  assert.equal(f.controller.state.dirty, false);
});
test('test and update guard dirty, readiness, pending run and duplicate submissions', async () => {
  const f = fixture();
  await f.controller.load();
  await f.controller.update('anthropic');
  assert.equal(f.writes.length, 0);
  f.controller.edit({ interval_hours: 48 });
  await f.controller.test();
  assert.equal(f.writes.length, 0);
  await f.controller.save();
  await Promise.all([f.controller.test(), f.controller.test()]);
  assert.equal(f.writes.filter((v) => v === 'test').length, 1);
  await f.controller.test();
  assert.equal(f.writes.filter((v) => v === 'test').length, 1);
  f.setRun(run({ state: 'succeeded' }));
  f.setCurrent(
    state({ version: 2, ready: true, latest_run: run({ state: 'succeeded' }) }),
  );
  await f.controller.poll();
  await f.controller.update('anthropic');
  assert.equal(f.writes.filter((v) => v === 'update').length, 1);
});
test('uncertain write blocks retries until saved state is explicitly reloaded', async () => {
  const f = fixture({
    test: async () => {
      throw new ApiError('dependency_unavailable');
    },
  });
  await f.controller.load();
  await f.controller.test();
  assert.equal(f.controller.state.uncertain, true);
  await f.controller.save();
  assert.equal(f.writes.length, 0);
  await f.controller.load(true);
  assert.equal(f.controller.state.uncertain, false);
});
test('permission loss clears settings and candidate data and denies further actions', async () => {
  const f = fixture({
    save: async () => {
      throw new ApiError('access_denied');
    },
  });
  await f.controller.load();
  await f.controller.search('openai', '');
  await f.controller.save();
  assert.equal(f.denied(), 1);
  assert.equal(f.controller.state.status, 'denied');
  assert.equal(f.controller.state.saved, null);
  assert.deepEqual(f.controller.state.models, []);
});
test('model deletion sends its CAS revision and clears the selected model after success', async () => {
  const requests: unknown[] = [];
  const api = createCatalogAPI('', async (url, init) => {
    requests.push([String(url), init?.method, init?.body]);
    return new Response(null, { status: 204 });
  });
  await api.remove(id, 4, token);
  assert.deepEqual(requests, [
    [
      `/v1/admin/ai/catalog/${id}`,
      'DELETE',
      JSON.stringify({ expected_version: 4 }),
    ],
  ]);
  const model = parseModel(modelFixture());
  const controller = createCatalogController(
    {
      ...api,
      list: async () => ({ models: [], total: 0 }),
      syncStates: async () => [],
      get: async () => model,
      prices: async () => [],
    },
    token,
    () => {},
    () => {},
  );
  await controller.loadCatalog();
  await controller.select(model);
  await controller.remove(model);
  assert.equal(controller.state.selected, null);
  assert.equal(controller.state.total, 0);
});
test('permission loss during a conflict reload purges the original draft', async () => {
  let reads = 0;
  const f = fixture({
    load: async () => {
      if (reads++) throw new ApiError('access_denied');
      return state();
    },
    save: async () => {
      throw new ApiError('conflict');
    },
  });
  await f.controller.load();
  f.controller.edit({ interval_hours: 48 });
  await f.controller.save();
  assert.equal(f.denied(), 1);
  assert.equal(f.controller.state.draft, null);
});
test('disposal aborts pending reads and ignores late results', async () => {
  let complete: (value: ResearchState) => void = () => {};
  let signal: AbortSignal | undefined;
  const f = fixture({
    load: async (_token, requestSignal) => {
      signal = requestSignal;
      return new Promise((resolve) => {
        complete = resolve;
      });
    },
  });
  const pending = f.controller.load();
  f.controller.dispose();
  complete(state());
  await pending;
  assert.equal(signal?.aborted, true);
  assert.equal(f.controller.state.saved, null);
  assert.equal(f.controller.state.status, 'disposed');
});
