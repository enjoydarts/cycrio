import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createCatalogController,
  perMillion,
  updateInput,
  priceInput,
  isPrimaryModel,
  createRevisionDraft,
  reconcileRevisionDraft,
  revisionInput,
} from './model-catalog.ts';
import * as catalog from './model-catalog.ts';
import {
  createCatalogAPI,
  parseModel,
  type CatalogAPI,
} from './catalog-api.ts';
import { ApiError } from './api.ts';
import {
  modelFixture,
  settingsFixture,
  syncFixture,
} from './catalog-test-fixtures.ts';

test('decimal display shifts exactly without floating point and unknown is distinct from zero', () => {
  assert.equal(perMillion('0.000000000000123456789'), '0.000000123456789');
  assert.equal(perMillion('12.340000'), '12340000');
  assert.equal(perMillion('0'), '0');
  assert.equal(perMillion(undefined), '不明・変動');
});
test('list prices preserve currency, exact decimals, zero and unknown token prices', () => {
  assert.equal(typeof catalog.modelRate, 'function');
  const { modelRate } = catalog;
  const model = parseModel(modelFixture());
  assert.equal(modelRate(model, 'input'), '不明');
  const priced = parseModel({
    ...modelFixture(),
    current_price: {
      id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
      model_id: model.id,
      source: 'manual',
      currency: 'JPY',
      rates: {
        input: { amount: '0.000000000000123456789', unit: 'JPY/token' },
        output: { amount: '0', unit: 'JPY/token' },
      },
      raw_pricing: null,
      effective_from: null,
      captured_at: '2026-10-25T00:00:00Z',
      source_url: 'https://example.com',
    },
  });
  assert.equal(modelRate(priced, 'input'), '0.000000123456789 JPY');
  assert.equal(modelRate(priced, 'output'), '0 JPY');
});
test('primary selection accepts explicitly maintained D1 variants including d1.1', () => {
  const model = parseModel(modelFixture());
  for (const id of ['d1', 'd1.1', 'd1-8b', 'd1/8b'])
    assert.equal(isPrimaryModel({ ...model, model_id: id }), true);
  for (const id of ['lfm2', 'd10', 'd1.'])
    assert.equal(isPrimaryModel({ ...model, model_id: id }), false);
  assert.equal(
    isPrimaryModel({ ...model, model_id: 'd1', enabled: false }),
    false,
  );
});
test('enabled changes preserve nullable overrides rather than freezing API metadata', () => {
  assert.deepEqual(updateInput(parseModel(modelFixture()), false), {
    expected_version: 1,
    display_name: '',
    enabled: false,
    text_input: null,
    structured_output: null,
  });
});
test('unedited sync drafts follow server updates and dirty drafts retain their original revision', () => {
  const first = createRevisionDraft(1, {
    automatic_enabled: false,
    interval_hours: 24,
    workspace_id: 'old',
  });
  const fresh = reconcileRevisionDraft(first, 2, {
    automatic_enabled: true,
    interval_hours: 48,
    workspace_id: 'new',
  });
  assert.equal(fresh.baseline_version, 2);
  assert.equal(fresh.values.workspace_id, 'new');
  fresh.dirty = true;
  fresh.values.interval_hours = 12;
  const changed = reconcileRevisionDraft(fresh, 3, {
    automatic_enabled: false,
    interval_hours: 96,
    workspace_id: 'other',
  });
  assert.equal(changed, fresh);
  assert.deepEqual(revisionInput(changed), {
    expected_version: 2,
    automatic_enabled: true,
    interval_hours: 12,
    workspace_id: 'new',
  });
  const reloaded = createRevisionDraft(3, {
    automatic_enabled: false,
    interval_hours: 96,
    workspace_id: 'other',
  });
  assert.equal(reloaded.dirty, false);
  assert.equal(reloaded.baseline_version, 3);
});
test('external model revisions preserve dirty name and capability inputs with a stale baseline', () => {
  const { expected_version, ...values } = updateInput(
    parseModel(modelFixture()),
  );
  const draft = createRevisionDraft(expected_version, values);
  draft.dirty = true;
  draft.values.display_name = 'unsaved name';
  draft.values.text_input = false;
  const updated = reconcileRevisionDraft(draft, 2, {
    ...values,
    display_name: 'other admin',
  });
  assert.equal(revisionInput(updated).expected_version, 1);
  assert.equal(updated.values.display_name, 'unsaved name');
  assert.equal(updated.values.text_input, false);
});
test('dirty manual price fields keep revision from first edit after a newer API price arrives', () => {
  const draft = createRevisionDraft(1, {
    input_per_million: '0',
    output_per_million: '1.234567890123456789',
  });
  draft.dirty = true;
  const newer = reconcileRevisionDraft(draft, 2, {
    input_per_million: '',
    output_per_million: '',
  });
  assert.equal(newer.baseline_version, 1);
  assert.equal(newer.values.output_per_million, '1.234567890123456789');
});
test('price reset uses the confirmation revision and cannot remove a newer override', async () => {
  const versions: number[] = [];
  const api = fakeAPI({
    resetPrice: async (_id, version) => {
      versions.push(version);
      throw new ApiError('conflict');
    },
  });
  const controller = createCatalogController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  await controller.loadCatalog();
  await controller.resetPrice(parseModel({ ...modelFixture(), version: 2 }), 1);
  assert.deepEqual(versions, [1]);
  assert.equal(controller.state.error, 'conflict');
});
test('manual price input distinguishes blank and zero and converts explicitly labelled JST', () => {
  const form = new FormData();
  form.set('currency', 'USD');
  form.set('input_per_million', '0');
  form.set('output_per_million', '');
  form.set('effective_from', '2026-10-25T12:30');
  form.set('source_url', 'https://example.com/prices');
  const value = priceInput(form, 4);
  assert.equal(value.input_per_million, '0');
  assert.equal(value.output_per_million, null);
  assert.equal(value.effective_from, '2026-10-25T03:30:00.000Z');
  form.set('source_url', 'http://example.com');
  assert.throws(() => priceInput(form, 4), ApiError);
  form.set('source_url', 'https://user:password@example.com/prices');
  assert.throws(() => priceInput(form, 4), ApiError);
});
const fakeAPI = (changes: Partial<CatalogAPI> = {}): CatalogAPI => ({
  ...createCatalogAPI('', async () => {
    throw Error('unexpected fetch');
  }),
  list: async () => ({ models: [parseModel(modelFixture())], total: 1 }),
  syncStates: async () =>
    ['liquid', 'openrouter', 'openai', 'anthropic'].map(
      (p) =>
        ({ ...syncFixture(p), provider: p }) as Awaited<
          ReturnType<CatalogAPI['syncStates']>
        >[number],
    ),
  settings: async () => settingsFixture(),
  ...changes,
});
test('new catalog searches supersede in-flight reads and stale results cannot replace the latest page', async () => {
  const pending: {
    filter: unknown;
    signal?: AbortSignal;
    resolve: (v: {
      models: ReturnType<typeof parseModel>[];
      total: number;
    }) => void;
  }[] = [];
  const api = fakeAPI({
    list: async (filter, _token, signal) =>
      new Promise((resolve) => pending.push({ filter, signal, resolve })),
  });
  const controller = createCatalogController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  const older = controller.loadCatalog({ search: 'old', offset: 20 });
  const newer = controller.loadCatalog({
    provider: 'openrouter',
    search: 'latest',
    offset: 0,
  });
  assert.equal(pending.length, 2);
  assert.equal(pending[0].signal?.aborted, true);
  pending[1].resolve({ models: [], total: 3 });
  await newer;
  pending[0].resolve({ models: [parseModel(modelFixture())], total: 99 });
  await older;
  assert.equal(controller.state.total, 3);
  assert.equal(controller.state.filter.search, 'latest');
});
test('a delayed live search preserves a clicked model while its detail read is pending', async () => {
  const target = parseModel({
    ...modelFixture(),
    id: '22222222-2222-4222-8222-222222222222',
    display_name: 'Clicked model',
  });
  for (const hasPrevious of [false, true]) {
    let finish!: (model: ReturnType<typeof parseModel>) => void;
    let delayed = false;
    const api = fakeAPI({
      get: async (id) => {
        if (id === target.id && !delayed) {
          delayed = true;
          return new Promise((resolve) => {
            finish = resolve;
          });
        }
        return id === target.id ? target : parseModel(modelFixture());
      },
      prices: async () => [],
    });
    const controller = createCatalogController(
      api,
      async () => 'token',
      () => {},
      () => {},
    );
    await controller.loadCatalog();
    if (hasPrevious) await controller.select(parseModel(modelFixture()));
    const selection = controller.select(target);
    await controller.loadCatalog({ search: 'clicked', offset: 0 });
    assert.equal(controller.state.selected?.id, target.id);
    finish(target);
    await selection;
    assert.equal(controller.state.selected?.id, target.id);
  }
});
test('model selection searches supersede a previous read while writes still block new searches', async () => {
  const pending: ((v: {
    models: ReturnType<typeof parseModel>[];
    total: number;
  }) => void)[] = [];
  const controller = createCatalogController(
    fakeAPI({
      list: async () => new Promise((resolve) => pending.push(resolve)),
    }),
    async () => 'token',
    () => {},
    () => {},
  );
  await controller.loadSettings();
  const first = controller.search({ search: 'old' });
  const latest = controller.search({ provider: 'openai', search: 'new' });
  assert.equal(pending.length, 2);
  pending[1]({ models: [], total: 2 });
  await latest;
  pending[0]({ models: [], total: 99 });
  await first;
  assert.equal(controller.state.total, 2);
  assert.equal(controller.state.filter.search, 'new');
  let finish!: () => void;
  const writing = createCatalogController(
    fakeAPI({
      add: async () => {
        await new Promise<void>((r) => (finish = r));
        return parseModel(modelFixture());
      },
      list: async () => {
        throw Error('must not search during save');
      },
    }),
    async () => 'token',
    () => {},
    () => {},
  );
  const save = writing.add({
    provider: 'liquid',
    model_id: 'd1',
    display_name: 'D1',
    text_input: null,
    structured_output: null,
  });
  await writing.search({ search: 'ignored' });
  assert.equal(writing.state.status, 'saving');
  finish();
  await save;
});
test('saved selections load by UUID even when absent from first catalog page', async () => {
  const ids: string[] = [];
  const id = modelFixture().id;
  const api = fakeAPI({
    settings: async () => ({
      ...settingsFixture(),
      primary_model_id: id,
      secondary_model_id: id,
    }),
    get: async (value) => {
      ids.push(value);
      return parseModel(modelFixture());
    },
  });
  const controller = createCatalogController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  await controller.loadSettings();
  assert.deepEqual(ids, [id, id]);
  assert.equal(controller.state.primary?.display_name, 'D1');
});
test('catalog paging remains server filtered and double clicks cannot duplicate writes', async () => {
  let writes = 0;
  let release!: () => void;
  const seen: unknown[] = [];
  const api = fakeAPI({
    list: async (filter) => {
      seen.push(filter);
      return { models: [], total: 30 };
    },
    add: async () => {
      writes++;
      await new Promise<void>((resolve) => {
        release = resolve;
      });
      return parseModel(modelFixture());
    },
  });
  const controller = createCatalogController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  await controller.loadCatalog({
    provider: 'openai',
    search: 'model',
    offset: 20,
  });
  assert.deepEqual(seen[0], {
    provider: 'openai',
    search: 'model',
    offset: 20,
  });
  const value = {
    provider: 'liquid' as const,
    model_id: 'd1',
    display_name: 'D1',
    text_input: null,
    structured_output: null,
  };
  const first = controller.add(value);
  await controller.add(value);
  assert.equal(writes, 1);
  release();
  await first;
});
test('conflicts reload server state and require an explicit retry', async () => {
  let writes = 0;
  let reads = 0;
  const api = fakeAPI({
    update: async () => {
      writes++;
      throw new ApiError('conflict');
    },
    list: async () => {
      reads++;
      return { models: [], total: 0 };
    },
  });
  const controller = createCatalogController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  await controller.update(
    parseModel(modelFixture()),
    updateInput(parseModel(modelFixture()), false),
  );
  assert.equal(writes, 1);
  assert.equal(reads, 1);
  assert.equal(controller.state.error, 'conflict');
});
test('sync polling refreshes selected model price revision as well as the page', async () => {
  let version = 1;
  const api = fakeAPI({
    get: async () => parseModel({ ...modelFixture(), version }),
    prices: async () => [],
  });
  const controller = createCatalogController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  await controller.loadCatalog();
  await controller.select(parseModel(modelFixture()));
  version = 2;
  await controller.pollSync();
  assert.equal(controller.state.selected?.version, 2);
});
test('denied clears all metadata and disposal rejects stale responses', async () => {
  let denied = 0;
  let release!: (value: {
    models: ReturnType<typeof parseModel>[];
    total: number;
  }) => void;
  const api = fakeAPI({
    list: async () => {
      throw new ApiError('access_denied');
    },
  });
  const controller = createCatalogController(
    api,
    async () => 'token',
    () => {},
    () => {
      denied++;
    },
  );
  await controller.loadSettings();
  await controller.loadCatalog();
  assert.equal(denied, 1);
  assert.equal(controller.state.settings, null);
  assert.deepEqual(controller.state.models, []);
  const other = createCatalogController(
    fakeAPI({
      list: async () =>
        new Promise((resolve) => {
          release = resolve;
        }),
    }),
    async () => 'token',
    () => {},
    () => {},
  );
  const pending = other.loadCatalog();
  other.dispose();
  release({ models: [parseModel(modelFixture())], total: 1 });
  await pending;
  assert.equal(other.state.status, 'disposed');
  assert.deepEqual(other.state.models, []);
});
