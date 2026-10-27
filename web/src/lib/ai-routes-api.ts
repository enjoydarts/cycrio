import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import { parsePrice, type Price } from './catalog-api.ts';

export const invalidRoute = (): never => {
  throw new ApiError('dependency_unavailable');
};
export const routeRecord = (v: unknown): Record<string, unknown> =>
  v !== null && typeof v === 'object' && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : invalidRoute();
export const routeText = (v: unknown): string =>
  typeof v === 'string' && v.length <= 4096 ? v : invalidRoute();
export const routeBool = (v: unknown): boolean =>
  typeof v === 'boolean' ? v : invalidRoute();
export const routeInt = (v: unknown, min = 0): number =>
  typeof v === 'number' && Number.isSafeInteger(v) && v >= min
    ? v
    : invalidRoute();
export const routeID = (v: unknown): string =>
  typeof v === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v) &&
  v !== '00000000-0000-0000-0000-000000000000'
    ? v
    : invalidRoute();
export const routeTime = (v: unknown): string => {
  const s = routeText(v);
  return /^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])T([01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d{1,9})?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(
    s,
  ) &&
    Number.isFinite(Date.parse(s)) &&
    new Date(`${s.slice(0, 10)}T00:00:00Z`).toISOString().slice(0, 10) ===
      s.slice(0, 10)
    ? s
    : invalidRoute();
};
export const safeSource = (v: unknown): string => {
  const s = routeText(v);
  try {
    const u = new URL(s);
    return u.protocol === 'https:' && !u.username && !u.password
      ? s
      : invalidRoute();
  } catch {
    return invalidRoute();
  }
};
export const routeNullable = <T>(
  v: unknown,
  parse: (v: unknown) => T,
): T | null => (v === null ? null : parse(v));
export const routeArray = <T>(
  v: unknown,
  parse: (v: unknown) => T,
  max = 20000,
): T[] => (Array.isArray(v) && v.length <= max ? v.map(parse) : invalidRoute());
export type RouteProof = {
  ambiguous: boolean;
  id: string;
  snapshot_id: string;
  captured_at: string;
  source_url: string;
  upstream_model_id: string;
  model_name: string;
  provider_name: string;
  tag: string;
  service_tier: string;
  quantization: string;
  context_length: number | null;
  max_prompt_tokens: number | null;
  max_completion_tokens: number | null;
  supported_parameters: string[] | null;
  status: number | null;
  zdr: boolean;
  price: Price | null;
  pricing_hash: string;
};
// Endpoint quotes may describe an upstream model which has no catalog UUID yet.
export function parseEndpointPrice(value: unknown): Price {
  const v = routeRecord(value);
  if (v.model_id === '') {
    const p = parsePrice({
      ...v,
      model_id: '11111111-1111-4111-8111-111111111111',
    });
    return { ...p, model_id: '' };
  }
  return parsePrice(v);
}
export function parseRouteProof(value: unknown): RouteProof {
  const v = routeRecord(value);
  return {
    ambiguous: routeBool(v.ambiguous),
    id: routeID(v.id),
    snapshot_id: routeID(v.snapshot_id),
    captured_at: routeTime(v.captured_at),
    source_url: safeSource(v.source_url),
    upstream_model_id: routeText(v.upstream_model_id),
    model_name: routeText(v.model_name),
    provider_name: routeText(v.provider_name),
    tag: routeText(v.tag),
    service_tier: routeText(v.service_tier),
    quantization: routeText(v.quantization),
    context_length: routeNullable(v.context_length, routeInt),
    max_prompt_tokens: routeNullable(v.max_prompt_tokens, routeInt),
    max_completion_tokens: routeNullable(v.max_completion_tokens, routeInt),
    supported_parameters: routeNullable(v.supported_parameters, (x) =>
      routeArray(x, routeText, 200),
    ),
    status: routeNullable(v.status, (x) =>
      routeInt(x, Number.MIN_SAFE_INTEGER),
    ),
    zdr: routeBool(v.zdr),
    price: routeNullable(v.price, parseEndpointPrice),
    pricing_hash: routeText(v.pricing_hash),
  };
}
export type AIRoute = RouteProof & {
  model_id: string | null;
  eligible: boolean;
  reasons: string[];
};
export type RoutesResult = {
  snapshot: { id: string; captured_at: string; source_url: string } | null;
  routes: AIRoute[];
  total: number;
  key_available: boolean;
  fresh: boolean;
  refresh: {
    status: 'never' | 'running' | 'succeeded' | 'failed';
    error_code:
      | null
      | 'provider_unavailable'
      | 'credentials_required'
      | 'conflict'
      | 'cancelled';
    last_attempt_at: string | null;
    last_success_at: string | null;
  };
};
export function parseRoutes(value: unknown): RoutesResult {
  const v = routeRecord(value),
    r = routeRecord(v.refresh);
  const status = routeText(r.status),
    error = r.error_code === null ? null : routeText(r.error_code);
  if (
    !['never', 'running', 'succeeded', 'failed'].includes(status) ||
    (error !== null &&
      ![
        'provider_unavailable',
        'credentials_required',
        'conflict',
        'cancelled',
      ].includes(error))
  )
    return invalidRoute();
  return {
    snapshot: routeNullable(v.snapshot, (x) => {
      const s = routeRecord(x);
      return {
        id: routeID(s.id),
        captured_at: routeTime(s.captured_at),
        source_url: safeSource(s.source_url),
      };
    }),
    routes: routeArray(v.routes, (x) => {
      const a = routeRecord(x);
      return {
        ...parseRouteProof(a),
        model_id: routeNullable(a.model_id, routeID),
        eligible: routeBool(a.eligible),
        reasons: routeArray(a.reasons, routeText, 100),
      };
    }),
    total: routeInt(v.total),
    key_available: routeBool(v.key_available),
    fresh: routeBool(v.fresh),
    refresh: {
      status: status as RoutesResult['refresh']['status'],
      error_code: error as RoutesResult['refresh']['error_code'],
      last_attempt_at: routeNullable(r.last_attempt_at, routeTime),
      last_success_at: routeNullable(r.last_success_at, routeTime),
    },
  };
}
export type RoutesFilter = {
  model_id?: string;
  search?: string;
  provider?: string;
  max_input_bytes?: number;
  max_output_tokens?: number;
  offset?: number;
  include_unavailable?: boolean;
};
export function createAIRoutesAPI(base: string, fetcher: typeof fetch = fetch) {
  async function request(
    path: string,
    token: TokenProvider,
    signal?: AbortSignal,
    method = 'GET',
  ) {
    try {
      const bearer = await token();
      if (!bearer) throw new ApiError('authentication_required');
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      const response = await fetcher(
        `${base.replace(/\/$/, '')}/v1/admin/ai/routes${path}`,
        {
          method,
          signal,
          credentials: 'omit',
          cache: 'no-store',
          headers: {
            Accept: 'application/json',
            Authorization: `Bearer ${bearer}`,
            ...(method === 'POST'
              ? { 'Content-Type': 'application/json' }
              : {}),
          },
          ...(method === 'POST' ? { body: '{}' } : {}),
        },
      );
      if (!response.ok) {
        const errors: Record<number, ErrorCode> = {
          400: 'invalid_request',
          401: 'authentication_required',
          403: 'access_denied',
          404: 'not_found',
          409: 'conflict',
        };
        throw new ApiError(errors[response.status] ?? 'dependency_unavailable');
      }
      return parseRoutes(await response.json());
    } catch (error) {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (error instanceof ApiError) throw error;
      return invalidRoute();
    }
  }
  return {
    list(filter: RoutesFilter, token: TokenProvider, signal?: AbortSignal) {
      const query = new URLSearchParams({ limit: '100' });
      for (const [key, value] of Object.entries(filter))
        if (value !== undefined) query.set(key, String(value));
      return request(`?${query}`, token, signal);
    },
    refresh: (token: TokenProvider, signal?: AbortSignal) =>
      request('/refresh', token, signal, 'POST'),
  };
}
export type AIRoutesAPI = ReturnType<typeof createAIRoutesAPI>;
