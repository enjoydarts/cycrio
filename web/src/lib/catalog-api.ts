import { ApiError, type TokenProvider } from './api.ts';

export const modelProviders = [
  'liquid',
  'openrouter',
  'openai',
  'anthropic',
] as const;
export type ModelProvider = (typeof modelProviders)[number];
export type Price = {
  id: string;
  model_id: string;
  source: 'api' | 'manual';
  currency: string;
  rates: Record<
    string,
    { amount: string; unit: 'token' | 'request' | 'image' | 'search' }
  >;
  raw_pricing: Record<string, unknown> | null;
  effective_from: string | null;
  captured_at: string;
  source_url: string;
};
export type Model = {
  id: string;
  provider: ModelProvider;
  model_id: string;
  display_name: string;
  canonical_slug: string | null;
  text_input: boolean | null;
  structured_output: boolean | null;
  context_length: number | null;
  max_input_tokens: number | null;
  max_output_tokens: number | null;
  raw_metadata: Record<string, unknown> | null;
  source: 'api' | 'manual';
  available: boolean;
  enabled: boolean;
  custom_display_name: string | null;
  text_input_override: boolean | null;
  structured_output_override: boolean | null;
  version: number;
  api_price: Price | null;
  manual_price: Price | null;
  current_price: Price | null;
  created_at: string;
  updated_at: string;
};
export type SyncState = {
  provider: ModelProvider;
  automatic_enabled: boolean;
  interval_hours: number;
  workspace_id: string;
  version: number;
  next_sync_at: string | null;
  last_success_at: string | null;
  last_attempt_at: string | null;
  status:
    | 'never'
    | 'queued'
    | 'running'
    | 'succeeded'
    | 'failed'
    | 'credentials_required'
    | 'manual_only';
  error_code: 'provider_unavailable' | 'credentials_required' | null;
};
export type SettingsState = {
  version: number;
  id: string | null;
  primary_model_id: string | null;
  secondary_model_id: string | null;
  primary_price_id: string | null;
  secondary_price_id: string | null;
  updated_at: string | null;
};
export type CatalogFilter = {
  provider?: ModelProvider | '';
  search?: string;
  offset?: number;
};
export type ModelInput = Pick<
  Model,
  'provider' | 'model_id' | 'display_name' | 'text_input' | 'structured_output'
>;
export type ModelUpdate = {
  expected_version: number;
  display_name: string;
  enabled: boolean;
  text_input: boolean | null;
  structured_output: boolean | null;
};
export const priceFields = [
  'input_per_million',
  'output_per_million',
  'cache_read_per_million',
  'cache_write_per_million',
  'cache_write_30m_per_million',
  'cache_write_1h_per_million',
  'cache_write_12h_per_million',
  'per_request',
] as const;
export type PriceInput = {
  expected_version: number;
  currency: string;
  effective_from: string;
  source_url: string;
} & Record<(typeof priceFields)[number], string | null>;
export type SyncInput = {
  expected_version: number;
  automatic_enabled: boolean;
  interval_hours: number;
  workspace_id: string;
};
const invalid = (): never => {
  throw new ApiError('dependency_unavailable');
};
const record = (value: unknown): Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : invalid();
const str = (v: unknown): string => (typeof v === 'string' ? v : invalid());
const bool = (v: unknown): boolean => (typeof v === 'boolean' ? v : invalid());
const integer = (v: unknown, min = 0): number =>
  typeof v === 'number' && Number.isSafeInteger(v) && v >= min ? v : invalid();
const choice = <T extends string>(v: unknown, values: readonly T[]): T =>
  typeof v === 'string' && values.includes(v as T) ? (v as T) : invalid();
const nullable = <T>(v: unknown, parse: (v: unknown) => T): T | null =>
  v === null ? null : parse(v);
const uuid = (v: unknown): string =>
  typeof v === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v)
    ? v
    : invalid();
function timestamp(v: unknown): string {
  if (
    typeof v !== 'string' ||
    !/^\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(
      v,
    ) ||
    !Number.isFinite(Date.parse(v))
  )
    return invalid();
  if (
    new Date(`${v.slice(0, 10)}T00:00:00Z`).toISOString().slice(0, 10) !==
    v.slice(0, 10)
  )
    return invalid();
  return v;
}
export const decimal = (v: unknown): string =>
  typeof v === 'string' && /^(?:0|[1-9]\d*)(?:\.\d+)?$/.test(v) ? v : invalid();
export function parsePrice(value: unknown): Price {
  const v = record(value);
  const rates = record(v.rates);
  const parseUnit = (unit: unknown) => {
    const text = str(unit);
    if (!text.startsWith(`${v.currency}/`)) return invalid();
    return choice(text.slice(String(v.currency).length + 1), [
      'token',
      'request',
      'image',
      'search',
    ]);
  };
  return {
    id: uuid(v.id),
    model_id: uuid(v.model_id),
    source: choice(v.source, ['api', 'manual']),
    currency:
      typeof v.currency === 'string' && /^[A-Z]{3}$/.test(v.currency)
        ? v.currency
        : invalid(),
    rates: Object.fromEntries(
      Object.entries(rates).map(([key, value]) => {
        const rate = record(value);
        return [
          choice(key, [
            'input',
            'output',
            'cache_read',
            'cache_write',
            'cache_write_1h',
            'cache_write_30m',
            'cache_write_12h',
            'request',
            'image',
            'image_output',
            'image_token',
            'audio',
            'audio_output',
            'audio_cache',
            'web_search',
            'reasoning',
          ]),
          { amount: decimal(rate.amount), unit: parseUnit(rate.unit) },
        ];
      }),
    ),
    raw_pricing: nullable(v.raw_pricing, record),
    effective_from: nullable(v.effective_from, timestamp),
    captured_at: timestamp(v.captured_at),
    source_url: str(v.source_url),
  };
}
export function parseModel(value: unknown): Model {
  const v = record(value);
  return {
    id: uuid(v.id),
    provider: choice(v.provider, modelProviders),
    model_id: str(v.model_id),
    display_name: str(v.display_name),
    canonical_slug: nullable(v.canonical_slug, str),
    text_input: nullable(v.text_input, bool),
    structured_output: nullable(v.structured_output, bool),
    context_length: nullable(v.context_length, integer),
    max_input_tokens: nullable(v.max_input_tokens, integer),
    max_output_tokens: nullable(v.max_output_tokens, integer),
    raw_metadata: nullable(v.raw_metadata, record),
    source: choice(v.source, ['api', 'manual']),
    available: bool(v.available),
    enabled: bool(v.enabled),
    custom_display_name: nullable(v.custom_display_name, str),
    text_input_override: nullable(v.text_input_override, bool),
    structured_output_override: nullable(v.structured_output_override, bool),
    version: integer(v.version, 1),
    api_price: nullable(v.api_price, parsePrice),
    manual_price: nullable(v.manual_price, parsePrice),
    current_price: nullable(v.current_price, parsePrice),
    created_at: timestamp(v.created_at),
    updated_at: timestamp(v.updated_at),
  };
}
export function parseSync(value: unknown): SyncState {
  const v = record(value);
  const interval = integer(v.interval_hours, 1);
  if (interval > 168) return invalid();
  return {
    provider: choice(v.provider, modelProviders),
    automatic_enabled: bool(v.automatic_enabled),
    interval_hours: interval,
    workspace_id: str(v.workspace_id),
    version: integer(v.version),
    next_sync_at: nullable(v.next_sync_at, timestamp),
    last_success_at: nullable(v.last_success_at, timestamp),
    last_attempt_at: nullable(v.last_attempt_at, timestamp),
    status: choice(v.status, [
      'never',
      'queued',
      'running',
      'succeeded',
      'failed',
      'credentials_required',
      'manual_only',
    ]),
    error_code: nullable(v.error_code, (x) =>
      choice(x, ['provider_unavailable', 'credentials_required']),
    ),
  };
}
export function parseSettings(value: unknown): SettingsState {
  const v = record(value);
  return {
    version: integer(v.version),
    id: nullable(v.id, uuid),
    primary_model_id: nullable(v.primary_model_id, uuid),
    secondary_model_id: nullable(v.secondary_model_id, uuid),
    primary_price_id: nullable(v.primary_price_id, uuid),
    secondary_price_id: nullable(v.secondary_price_id, uuid),
    updated_at: nullable(v.updated_at, timestamp),
  };
}
const array = <T>(
  value: unknown,
  parse: (v: unknown) => T,
  max: number,
): T[] =>
  Array.isArray(value) && value.length <= max ? value.map(parse) : invalid();

export function createCatalogAPI(base: string, fetcher: typeof fetch = fetch) {
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
        `${base.replace(/\/$/, '')}/v1/admin/ai/${path}`,
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
              : response.status === 400
                ? 'invalid_request'
                : response.status === 404
                  ? 'not_found'
                  : response.status === 409
                    ? 'conflict'
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
    async list(
      filter: CatalogFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const params = new URLSearchParams({
        provider: filter.provider ?? '',
        search: filter.search ?? '',
        include_disabled: 'true',
        limit: '20',
        offset: String(filter.offset ?? 0),
      });
      const v = record(await request(`catalog?${params}`, token, signal));
      return {
        models: array(v.models, parseModel, 20),
        total: integer(v.total),
      };
    },
    async get(id: string, token: TokenProvider, signal?: AbortSignal) {
      return parseModel(
        await request(`catalog/${encodeURIComponent(id)}`, token, signal),
      );
    },
    async add(input: ModelInput, token: TokenProvider, signal?: AbortSignal) {
      return parseModel(
        await request('catalog', token, signal, 'POST', {
          provider: input.provider,
          model_id: input.model_id,
          display_name: input.display_name,
          text_input: input.text_input,
          structured_output: input.structured_output,
        }),
      );
    },
    async update(
      id: string,
      input: ModelUpdate,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseModel(
        await request(
          `catalog/${encodeURIComponent(id)}`,
          token,
          signal,
          'PUT',
          {
            expected_version: input.expected_version,
            display_name: input.display_name,
            enabled: input.enabled,
            text_input: input.text_input,
            structured_output: input.structured_output,
          },
        ),
      );
    },
    async prices(id: string, token: TokenProvider, signal?: AbortSignal) {
      const v = record(
        await request(
          `catalog/${encodeURIComponent(id)}/prices`,
          token,
          signal,
        ),
      );
      return array(v.prices, parsePrice, 50);
    },
    async savePrice(
      id: string,
      input: PriceInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const payload = Object.fromEntries(
        priceFields.map((key) => [key, input[key]]),
      );
      return parseModel(
        await request(
          `catalog/${encodeURIComponent(id)}/prices`,
          token,
          signal,
          'POST',
          {
            ...payload,
            expected_version: input.expected_version,
            currency: input.currency,
            effective_from: input.effective_from,
            source_url: input.source_url,
          },
        ),
      );
    },
    async resetPrice(
      id: string,
      expected_version: number,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseModel(
        await request(
          `catalog/${encodeURIComponent(id)}/prices/override`,
          token,
          signal,
          'DELETE',
          { expected_version },
        ),
      );
    },
    async syncStates(token: TokenProvider, signal?: AbortSignal) {
      const v = record(await request('catalog-sync', token, signal));
      const states = array(v.providers, parseSync, 4);
      if (
        states.length !== 4 ||
        new Set(states.map((s) => s.provider)).size !== 4
      )
        return invalid();
      return states;
    },
    async requestSync(
      provider: ModelProvider,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseSync(
        await request(`catalog-sync/${provider}`, token, signal, 'POST', {}),
      );
    },
    async configureSync(
      provider: ModelProvider,
      input: SyncInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseSync(
        await request(`catalog-sync/${provider}`, token, signal, 'PUT', {
          expected_version: input.expected_version,
          automatic_enabled: input.automatic_enabled,
          interval_hours: input.interval_hours,
          workspace_id: input.workspace_id,
        }),
      );
    },
    async settings(token: TokenProvider, signal?: AbortSignal) {
      return parseSettings(await request('settings', token, signal));
    },
    async saveSettings(
      expected_version: number,
      primary_model_id: string,
      secondary_model_id: string,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseSettings(
        await request('settings', token, signal, 'PUT', {
          expected_version,
          primary_model_id,
          secondary_model_id,
        }),
      );
    },
  };
}
export type CatalogAPI = ReturnType<typeof createCatalogAPI>;
