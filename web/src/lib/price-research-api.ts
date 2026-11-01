import { ApiError, type TokenProvider } from './api.ts';

export type ResearchProvider = 'openai' | 'anthropic';
export type ResearchTarget = ResearchProvider | 'liquid';
export type ResearchRun = {
  id: string;
  kind: 'test' | 'update';
  target_provider: ResearchTarget;
  state: 'queued' | 'running' | 'succeeded' | 'failed' | 'unknown';
  error_code: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  updated_count: number;
  unresolved_count: number;
  call_id: string | null;
  sources: string[];
};
export type ResearchState = {
  version: number;
  provider: ResearchProvider | '';
  model_id: string | null;
  automatic_enabled: boolean;
  interval_hours: number;
  next_run_at: string | null;
  ready: boolean;
  latest_run: ResearchRun | null;
};
export type ResearchInput = Pick<
  ResearchState,
  'model_id' | 'automatic_enabled' | 'interval_hours'
> & { expected_version: number };
const invalid = (): never => {
  throw new ApiError('dependency_unavailable');
};
const record = (v: unknown): Record<string, unknown> =>
  typeof v === 'object' && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : invalid();
const string = (v: unknown): string => (typeof v === 'string' ? v : invalid());
const integer = (v: unknown): number =>
  typeof v === 'number' && Number.isSafeInteger(v) && v >= 0 ? v : invalid();
const boolean = (v: unknown): boolean =>
  typeof v === 'boolean' ? v : invalid();
const choice = <T extends string>(v: unknown, options: readonly T[]): T =>
  typeof v === 'string' && options.includes(v as T) ? (v as T) : invalid();
const uuid = (v: unknown): string =>
  typeof v === 'string' &&
  /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(v)
    ? v
    : invalid();
const nullable = <T>(v: unknown, parse: (v: unknown) => T): T | null =>
  v === null ? null : parse(v);
const timestamp = (v: unknown): string =>
  typeof v === 'string' &&
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(
    v,
  ) &&
  Number.isFinite(Date.parse(v)) &&
  new Date(`${v.slice(0, 10)}T00:00:00Z`).toISOString().slice(0, 10) ===
    v.slice(0, 10)
    ? v
    : invalid();
const sourceURL = (value: unknown): string => {
  const valueString = string(value);
  try {
    const url = new URL(valueString);
    const host = url.hostname.replace(/^www\./, '');
    if (
      url.protocol !== 'https:' ||
      url.port ||
      url.username ||
      url.password ||
      url.hash ||
      valueString.length > 2048 ||
      ![
        'openai.com',
        'developers.openai.com',
        'platform.openai.com',
        'anthropic.com',
        'claude.com',
        'platform.claude.com',
        'liquid.ai',
        'docs.liquid.ai',
      ].includes(host)
    )
      return invalid();
    return valueString;
  } catch {
    return invalid();
  }
};
export function parseResearchRun(value: unknown): ResearchRun {
  const v = record(value);
  if (!Array.isArray(v.sources) || v.sources.length > 100) return invalid();
  return {
    id: uuid(v.id),
    kind: choice(v.kind, ['test', 'update']),
    target_provider: choice(v.target_provider, [
      'liquid',
      'openai',
      'anthropic',
    ]),
    state: choice(v.state, [
      'queued',
      'running',
      'succeeded',
      'failed',
      'unknown',
    ]),
    error_code: string(v.error_code),
    created_at: timestamp(v.created_at),
    started_at: nullable(v.started_at, timestamp),
    finished_at: nullable(v.finished_at, timestamp),
    updated_count: integer(v.updated_count),
    unresolved_count: integer(v.unresolved_count),
    call_id: nullable(v.call_id, uuid),
    sources: v.sources.map(sourceURL),
  };
}
export function parseResearchState(value: unknown): ResearchState {
  const v = record(value),
    interval = integer(v.interval_hours);
  if (interval < 1 || interval > 168) return invalid();
  return {
    version: integer(v.version),
    provider: choice(v.provider, ['', 'openai', 'anthropic']),
    model_id: nullable(v.model_id, uuid),
    automatic_enabled: boolean(v.automatic_enabled),
    interval_hours: interval,
    next_run_at: nullable(v.next_run_at, timestamp),
    ready: boolean(v.ready),
    latest_run: nullable(v.latest_run, parseResearchRun),
  };
}
export function createPriceResearchAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  async function request(
    path: string,
    token: TokenProvider,
    signal?: AbortSignal,
    method = 'GET',
    body?: object,
  ): Promise<unknown> {
    try {
      const bearer = await token();
      if (!bearer) throw new ApiError('authentication_required');
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      const response = await fetcher(
        `${base.replace(/\/$/, '')}/v1/admin/ai/price-research${path}`,
        {
          method,
          signal,
          credentials: 'omit',
          cache: 'no-store',
          headers: {
            Accept: 'application/json',
            Authorization: `Bearer ${bearer}`,
            ...(body ? { 'Content-Type': 'application/json' } : {}),
          },
          ...(body ? { body: JSON.stringify(body) } : {}),
        },
      );
      if (!response.ok)
        throw new ApiError(
          response.status === 401
            ? 'authentication_required'
            : response.status === 403
              ? 'access_denied'
              : response.status === 409
                ? 'conflict'
                : response.status === 400
                  ? 'invalid_request'
                  : response.status === 404
                    ? 'not_found'
                    : 'dependency_unavailable',
        );
      return await response.json();
    } catch (error) {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (error instanceof ApiError) throw error;
      return invalid();
    }
  }
  return {
    load: async (token: TokenProvider, signal?: AbortSignal) =>
      parseResearchState(await request('', token, signal)),
    save: async (
      input: ResearchInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) =>
      parseResearchState(
        await request('', token, signal, 'PUT', {
          expected_version: input.expected_version,
          model_id: input.model_id,
          automatic_enabled: input.automatic_enabled,
          interval_hours: input.interval_hours,
        }),
      ),
    test: async (
      expected_version: number,
      token: TokenProvider,
      signal?: AbortSignal,
    ) =>
      parseResearchRun(
        await request('/tests', token, signal, 'POST', { expected_version }),
      ),
    update: async (
      expected_version: number,
      target_provider: ResearchTarget,
      token: TokenProvider,
      signal?: AbortSignal,
    ) =>
      parseResearchRun(
        await request('/runs', token, signal, 'POST', {
          expected_version,
          target_provider,
        }),
      ),
    run: async (id: string, token: TokenProvider, signal?: AbortSignal) =>
      parseResearchRun(
        await request(`/runs/${encodeURIComponent(id)}`, token, signal),
      ),
  };
}
export type PriceResearchAPI = ReturnType<typeof createPriceResearchAPI>;
