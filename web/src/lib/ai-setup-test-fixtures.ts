import {
  id,
  now,
  run,
  runtime as runtimeFixture,
} from './airuntime-test-fixtures.ts';
import { modelFixture } from './catalog-test-fixtures.ts';
import { processingPolicy } from './mail-processing-fixtures.ts';
import { createCatalogAPI, parseModel } from './catalog-api.ts';
import { createAIRuntimeAPI, parseRuntimeState } from './airuntime-api.ts';
import { createMailProcessingAPI } from './mail-processing-api.ts';
import { createAIRoutesAPI } from './ai-routes-api.ts';
export const secondaryID = '22222222-2222-4222-8222-222222222222';
export const routeFixture = () => ({
  ambiguous: false,
  id,
  snapshot_id: id,
  captured_at: now,
  source_url: 'https://openrouter.ai/api/v1/endpoints/zdr',
  upstream_model_id: 'a/b',
  model_name: 'Example',
  provider_name: 'DeepInfra',
  tag: 'deepinfra/fp4',
  service_tier: '',
  quantization: 'fp4',
  context_length: 65536,
  max_prompt_tokens: null,
  max_completion_tokens: 8192,
  supported_parameters: ['response_format'],
  status: 0,
  zdr: true,
  price: null,
  pricing_hash: 'a'.repeat(64),
  model_id: id,
  eligible: true,
  reasons: [],
});
export const routesFixture = () => ({
  snapshot: {
    id,
    captured_at: now,
    source_url: 'https://openrouter.ai/api/v1/endpoints/zdr',
  },
  routes: [routeFixture()],
  total: 1,
  key_available: true,
  fresh: true,
  refresh: {
    status: 'succeeded' as const,
    error_code: null,
    last_attempt_at: now,
    last_success_at: now,
  },
});
export function setupFixture() {
  const runtime = parseRuntimeState(runtimeFixture());
  runtime.draft!.secondary = {
    ...runtime.draft!.secondary,
    id: secondaryID,
    provider: 'openrouter',
  };
  runtime.draft!.openrouter_provider = 'deepinfra/fp4';
  const state = {
    runtime,
    settings: {
      version: 1,
      id,
      primary_model_id: id,
      secondary_model_id: secondaryID,
      primary_price_id: null,
      secondary_price_id: null,
      updated_at: now,
    },
    models: [
      parseModel({ ...modelFixture(), id }),
      parseModel({
        ...modelFixture(),
        id: secondaryID,
        provider: 'openrouter',
        model_id: 'a/b',
        display_name: 'Example',
      }),
    ],
    routes: {
      ...routesFixture(),
      routes: [{ ...routeFixture(), model_id: secondaryID }],
    },
    policy: processingPolicy(),
    failure: new Map<string, number>(),
    requests: [] as {
      path: string;
      method: string;
      body: Record<string, unknown>;
    }[],
    refreshes: 0,
  };
  const fetcher: typeof fetch = async (url, init) => {
    const u = new URL(String(url), 'https://example.test'),
      method = init?.method ?? 'GET',
      body = typeof init?.body === 'string' ? JSON.parse(init.body) : {};
    state.requests.push({ path: u.pathname + u.search, method, body });
    const status = state.failure.get(`${method} ${u.pathname}`);
    if (status)
      return Response.json({ code: 'dependency_unavailable' }, { status });
    let result: unknown;
    const path = u.pathname;
    if (path.endsWith('/routes/refresh')) {
      state.refreshes++;
      state.routes.fresh = true;
      result = state.routes;
    } else if (path.endsWith('/routes')) {
      const offset = Number(u.searchParams.get('offset') ?? 0);
      result = {
        ...state.routes,
        routes: state.routes.routes.slice(offset, offset + 100),
        total: state.routes.routes.length,
      };
    } else if (path.endsWith('/catalog')) {
      const offset = Number(u.searchParams.get('offset') ?? 0);
      result = {
        models: state.models.slice(offset, offset + 20),
        total: state.models.length,
      };
    } else if (path.endsWith('/settings')) {
      if (method === 'PUT')
        state.settings = {
          ...state.settings,
          version: state.settings.version + 1,
        };
      result = state.settings;
    } else if (path.endsWith('/policy')) result = state.policy;
    else if (path.endsWith('/runtime/tests'))
      result = { ...run(), state: 'succeeded' };
    else if (path.endsWith('/runtime')) {
      if (method === 'PUT')
        state.runtime = {
          ...state.runtime,
          version: state.runtime.version + 1,
        };
      result = state.runtime;
    } else result = { ...run(), state: 'succeeded' };
    return Response.json(result);
  };
  return {
    state,
    api: {
      catalog: createCatalogAPI('', fetcher),
      runtime: createAIRuntimeAPI('', fetcher),
      processing: createMailProcessingAPI('', fetcher),
      routes: createAIRoutesAPI('', fetcher),
    },
  };
}
