import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import { parseRouteProof, type RouteProof } from './ai-routes-api.ts';
import {
  decimal,
  modelProviders,
  parsePrice,
  type ModelProvider,
  type Price,
} from './catalog-api.ts';

export type RuntimeErrorCode = ErrorCode | 'credentials_required';
export const stages = [
  'classification',
  'extraction',
  'match',
  'repair',
] as const;
export const scopes = ['user', 'admin_test', 'system'] as const;
export const outcomes = [
  'success',
  'invalid_request',
  'transport_error',
  'request_cancelled',
  'response_read',
  'response_too_large',
  'invalid_json',
  'provider_http',
  'provider_error',
  'model_mismatch',
  'invalid_output',
  'refusal',
  'incomplete',
  'generation_mismatch',
  'provider_unavailable',
  'timeout',
  'configuration_unavailable',
  'execution_unavailable',
  'input_too_large',
  'credentials_unavailable',
  'recording_failed',
  'lease_expired',
] as const;
export type ModelRef = {
  id: string;
  provider: ModelProvider;
  model_id: string;
  display_name: string;
  credential_id: string;
  credential_version: number;
  price: Price | null;
};
export type Profile = {
  route_proof?: RouteProof | null;
  id: string;
  catalog_settings_id: string;
  primary: ModelRef;
  secondary: ModelRef;
  max_input_bytes: number;
  max_output_tokens: number;
  timeout_seconds: number;
  openrouter_provider: string;
  anthropic_workspace_id: string;
  prompt_version: string;
  created_at: string;
};
export type ConfigValues = Pick<
  Profile,
  | 'catalog_settings_id'
  | 'max_input_bytes'
  | 'max_output_tokens'
  | 'timeout_seconds'
  | 'openrouter_provider'
>;
export type ConfigInput = ConfigValues & {
  expected_version: number;
  route_id?: string;
};
export type TestFailure = {
  http_status: number;
  reason:
    | 'credentials_rejected'
    | 'payment_required'
    | 'model_route_unavailable'
    | 'rate_limited';
  hint: string;
};
export type TestRun = {
  primary_failure?: TestFailure | null;
  secondary_failure?: TestFailure | null;
  id: string;
  profile_id: string;
  state: 'queued' | 'leased' | 'running' | 'succeeded' | 'failed' | 'unknown';
  primary_outcome: (typeof outcomes)[number] | null;
  secondary_outcome: (typeof outcomes)[number] | null;
  classification: 'subscription_related' | 'not_related' | 'uncertain' | null;
  extracted_events: number | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
};
export type RuntimeState = {
  version: number;
  draft: Profile | null;
  active: Profile | null;
  active_ready: boolean;
  active_blocked_reason: string | null;
  latest_test: TestRun | null;
};
export type Usage = {
  present: boolean;
  valid: boolean;
  input_tokens: number;
  output_tokens: number;
  ordinary_input_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  cache_write_5m_tokens: number;
  cache_write_30m_tokens: number;
  cache_write_1h_tokens: number;
  cache_write_12h_tokens: number;
  reasoning_tokens: number;
  cache_write_ttl_known: boolean;
};
export type Cost = {
  status: 'unknown' | 'estimated' | 'provider_reported';
  amount: string | null;
  currency: string | null;
  reason: string;
};
export type Call = {
  id: string;
  operation_id: string;
  run_id: string | null;
  profile_id: string;
  scope: (typeof scopes)[number];
  consuming_user_id: string | null;
  initiated_by_user_id: string | null;
  provider: ModelProvider;
  model_id: string;
  model_name: string;
  requested_model_id: string;
  price_id: string | null;
  stage: (typeof stages)[number];
  attempt_no: number;
  status: 'calling' | 'succeeded' | 'failed' | 'unknown';
  outcome: (typeof outcomes)[number] | null;
  http_status: number | null;
  generation_id: string | null;
  actual_model_id: string | null;
  actual_provider: string | null;
  usage: Usage;
  cost: Cost;
  revision_id: string | null;
  started_at: string;
  finished_at: string | null;
  updated_at: string;
};
export type Totals = {
  currency: string | null;
  amount: string;
  estimated_amount: string;
  reported_amount: string;
  calls: number;
  unknown_calls: number;
  failures: number;
};
export type ConsumerCost = {
  user_id: string;
  calls: number;
  unknown_calls: number;
  failures: number;
  totals: Totals[];
};
export type Breakdown = {
  provider: ModelProvider;
  model_id: string;
  model_name: string;
  stage: (typeof stages)[number];
  totals: Totals[];
};
export type CostReport = {
  month: string;
  timezone: 'Asia/Tokyo';
  from: string;
  to: string;
  provisional: boolean;
  totals: Totals[];
  users: ConsumerCost[];
  users_total: number;
  administration: { scope: 'admin_test' | 'system'; totals: Totals[] }[];
  breakdown: Breakdown[];
  updated_at: string | null;
};
export type CostFilter = {
  month: string;
  user_id?: string;
  scope?: (typeof scopes)[number] | '';
  provider?: ModelProvider | '';
  model_id?: string;
  stage?: (typeof stages)[number] | '';
  limit?: number;
  offset?: number;
};
export type UsageRevision = {
  id: string;
  call_id: string;
  usage: Usage;
  cost: Cost;
  captured_at: string;
};
const invalid = (): never => {
  throw new ApiError('dependency_unavailable');
};
const object = (v: unknown): Record<string, unknown> =>
  typeof v === 'object' && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : invalid();
const str = (v: unknown): string =>
  typeof v === 'string' && v.length <= 1024 ? v : invalid();
const bool = (v: unknown): boolean => (typeof v === 'boolean' ? v : invalid());
const integer = (v: unknown, min = 0, max = Number.MAX_SAFE_INTEGER): number =>
  typeof v === 'number' && Number.isSafeInteger(v) && v >= min && v <= max
    ? v
    : invalid();
const choice = <T extends string>(v: unknown, values: readonly T[]): T =>
  typeof v === 'string' && values.includes(v as T) ? (v as T) : invalid();
const nullable = <T>(v: unknown, parse: (v: unknown) => T): T | null =>
  v === null ? null : parse(v);
const uuid = (v: unknown) =>
  typeof v === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v)
    ? v
    : invalid();
const currency = (v: unknown) =>
  typeof v === 'string' && /^[A-Z]{3}$/.test(v) ? v : invalid();
const array = <T>(v: unknown, parse: (v: unknown) => T, max = 10000): T[] =>
  Array.isArray(v) && v.length <= max ? v.map(parse) : invalid();
function timestamp(v: unknown): string {
  const s = str(v);
  if (
    !/^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])T([01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(
      s,
    ) ||
    !Number.isFinite(Date.parse(s)) ||
    new Date(`${s.slice(0, 10)}T00:00:00Z`).toISOString().slice(0, 10) !==
      s.slice(0, 10)
  )
    return invalid();
  return s;
}
function parseModelRef(value: unknown): ModelRef {
  const v = object(value);
  return {
    id: uuid(v.id),
    provider: choice(v.provider, modelProviders),
    model_id: str(v.model_id),
    display_name: str(v.display_name),
    credential_id: uuid(v.credential_id),
    credential_version: integer(v.credential_version, 1),
    price: nullable(v.price, parsePrice),
  };
}
export function parseProfile(value: unknown): Profile {
  const v = object(value);
  return {
    ...(v.route_proof === undefined
      ? {}
      : { route_proof: nullable(v.route_proof, parseRouteProof) }),
    id: uuid(v.id),
    catalog_settings_id: uuid(v.catalog_settings_id),
    primary: parseModelRef(v.primary),
    secondary: parseModelRef(v.secondary),
    max_input_bytes: integer(v.max_input_bytes, 1024, 65536),
    max_output_tokens: integer(v.max_output_tokens, 64, 16384),
    timeout_seconds: integer(v.timeout_seconds, 1, 120),
    openrouter_provider: str(v.openrouter_provider),
    anthropic_workspace_id: str(v.anthropic_workspace_id),
    prompt_version: str(v.prompt_version),
    created_at: timestamp(v.created_at),
  };
}
export function parseTestRun(value: unknown): TestRun {
  const v = object(value);
  return {
    ...(v.primary_failure === undefined
      ? {}
      : { primary_failure: nullable(v.primary_failure, parseTestFailure) }),
    ...(v.secondary_failure === undefined
      ? {}
      : { secondary_failure: nullable(v.secondary_failure, parseTestFailure) }),
    id: uuid(v.id),
    profile_id: uuid(v.profile_id),
    state: choice(v.state, [
      'queued',
      'leased',
      'running',
      'succeeded',
      'failed',
      'unknown',
    ]),
    primary_outcome: nullable(v.primary_outcome, (x) => choice(x, outcomes)),
    secondary_outcome: nullable(v.secondary_outcome, (x) =>
      choice(x, outcomes),
    ),
    classification: nullable(v.classification, (x) =>
      choice(x, ['subscription_related', 'not_related', 'uncertain']),
    ),
    extracted_events: nullable(v.extracted_events, (x) => integer(x, 0, 32)),
    created_at: timestamp(v.created_at),
    started_at: nullable(v.started_at, timestamp),
    finished_at: nullable(v.finished_at, timestamp),
  };
}
function parseTestFailure(value: unknown): TestFailure {
  const v = object(value);
  const reason = choice(v.reason, [
    'credentials_rejected',
    'payment_required',
    'model_route_unavailable',
    'rate_limited',
  ]);
  const status = integer(v.http_status, 100, 599);
  if (
    {
      credentials_rejected: 401,
      payment_required: 402,
      model_route_unavailable: 404,
      rate_limited: 429,
    }[reason] !== status
  )
    return invalid();
  return { http_status: status, reason, hint: str(v.hint) };
}
export function parseRuntimeState(value: unknown): RuntimeState {
  const v = object(value);
  return {
    version: integer(v.version),
    draft: nullable(v.draft, parseProfile),
    active: nullable(v.active, parseProfile),
    active_ready: bool(v.active_ready),
    active_blocked_reason: nullable(v.active_blocked_reason, str),
    latest_test: nullable(v.latest_test, parseTestRun),
  };
}
export function parseUsage(value: unknown): Usage {
  const v = object(value);
  return {
    present: bool(v.present),
    valid: bool(v.valid),
    input_tokens: integer(v.input_tokens),
    output_tokens: integer(v.output_tokens),
    ordinary_input_tokens: integer(v.ordinary_input_tokens),
    cache_read_tokens: integer(v.cache_read_tokens),
    cache_write_tokens: integer(v.cache_write_tokens),
    cache_write_5m_tokens: integer(v.cache_write_5m_tokens),
    cache_write_30m_tokens: integer(v.cache_write_30m_tokens),
    cache_write_1h_tokens: integer(v.cache_write_1h_tokens),
    cache_write_12h_tokens: integer(v.cache_write_12h_tokens),
    reasoning_tokens: integer(v.reasoning_tokens),
    cache_write_ttl_known: bool(v.cache_write_ttl_known),
  };
}
export function parseCost(value: unknown): Cost {
  const v = object(value);
  const cost = {
    status: choice(v.status, ['unknown', 'estimated', 'provider_reported']),
    amount: nullable(v.amount, decimal),
    currency: nullable(v.currency, currency),
    reason: str(v.reason),
  };
  if (
    cost.status === 'unknown'
      ? cost.amount !== null
      : cost.amount === null || cost.currency === null
  )
    return invalid();
  return cost;
}
export function parseCall(value: unknown): Call {
  const v = object(value);
  return {
    id: uuid(v.id),
    operation_id: uuid(v.operation_id),
    run_id: nullable(v.run_id, uuid),
    profile_id: uuid(v.profile_id),
    scope: choice(v.scope, scopes),
    consuming_user_id: nullable(v.consuming_user_id, uuid),
    initiated_by_user_id: nullable(v.initiated_by_user_id, uuid),
    provider: choice(v.provider, modelProviders),
    model_id: uuid(v.model_id),
    model_name: str(v.model_name),
    requested_model_id: str(v.requested_model_id),
    price_id: nullable(v.price_id, uuid),
    stage: choice(v.stage, stages),
    attempt_no: integer(v.attempt_no, 1),
    status: choice(v.status, ['calling', 'succeeded', 'failed', 'unknown']),
    outcome: nullable(v.outcome, (x) => choice(x, outcomes)),
    http_status: nullable(v.http_status, (x) => integer(x, 100, 599)),
    generation_id: nullable(v.generation_id, str),
    actual_model_id: nullable(v.actual_model_id, str),
    actual_provider: nullable(v.actual_provider, str),
    usage: parseUsage(v.usage),
    cost: parseCost(v.cost),
    revision_id: nullable(v.revision_id, uuid),
    started_at: timestamp(v.started_at),
    finished_at: nullable(v.finished_at, timestamp),
    updated_at: timestamp(v.updated_at),
  };
}
function parseTotals(value: unknown): Totals {
  const v = object(value);
  return {
    currency: nullable(v.currency, currency),
    amount: decimal(v.amount),
    estimated_amount: decimal(v.estimated_amount),
    reported_amount: decimal(v.reported_amount),
    calls: integer(v.calls),
    unknown_calls: integer(v.unknown_calls),
    failures: integer(v.failures),
  };
}
export function parseCostReport(value: unknown): CostReport {
  const v = object(value);
  const month = str(v.month);
  if (!/^(20\d{2}|21\d{2})-(0[1-9]|1[0-2])$/.test(month)) return invalid();
  return {
    month,
    timezone: choice(v.timezone, ['Asia/Tokyo']),
    from: timestamp(v.from),
    to: timestamp(v.to),
    provisional: bool(v.provisional),
    totals: array(v.totals, parseTotals),
    users: array(
      v.users,
      (x) => {
        const u = object(x);
        return {
          user_id: uuid(u.user_id),
          calls: integer(u.calls),
          unknown_calls: integer(u.unknown_calls),
          failures: integer(u.failures),
          totals: array(u.totals, parseTotals),
        };
      },
      100,
    ),
    users_total: integer(v.users_total),
    administration: array(v.administration, (x) => {
      const s = object(x);
      return {
        scope: choice(s.scope, ['admin_test', 'system']),
        totals: array(s.totals, parseTotals),
      };
    }),
    breakdown: array(v.breakdown, (x) => {
      const b = object(x);
      return {
        provider: choice(b.provider, modelProviders),
        model_id: uuid(b.model_id),
        model_name: str(b.model_name),
        stage: choice(b.stage, stages),
        totals: array(b.totals, parseTotals),
      };
    }),
    updated_at: nullable(v.updated_at, timestamp),
  };
}
function parseRevision(value: unknown): UsageRevision {
  const v = object(value);
  return {
    id: uuid(v.id),
    call_id: uuid(v.call_id),
    usage: parseUsage(v.usage),
    cost: parseCost(v.cost),
    captured_at: timestamp(v.captured_at),
  };
}
class CredentialsRequired extends Error {
  readonly code = 'credentials_required';
}
export function createAIRuntimeAPI(
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
      if (!response.ok) {
        if (response.status === 400) {
          const v = await response.json().catch(() => null);
          if (v?.code === 'credentials_required')
            throw new CredentialsRequired();
        }
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
      }
      return await response.json();
    } catch (error) {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (error instanceof ApiError || error instanceof CredentialsRequired)
        throw error;
      return invalid();
    }
  }
  function query(filter: CostFilter) {
    const p = new URLSearchParams({
      month: filter.month,
      limit: String(filter.limit ?? 50),
      offset: String(filter.offset ?? 0),
    });
    for (const key of [
      'user_id',
      'scope',
      'provider',
      'model_id',
      'stage',
    ] as const)
      if (filter[key]) p.set(key, filter[key]);
    return p.toString();
  }
  return {
    async state(token: TokenProvider, signal?: AbortSignal) {
      return parseRuntimeState(await request('runtime', token, signal));
    },
    async save(input: ConfigInput, token: TokenProvider, signal?: AbortSignal) {
      const {
        expected_version,
        catalog_settings_id,
        max_input_bytes,
        max_output_tokens,
        timeout_seconds,
        openrouter_provider,
      } = input;
      return parseRuntimeState(
        await request('runtime', token, signal, 'PUT', {
          expected_version,
          catalog_settings_id,
          max_input_bytes,
          max_output_tokens,
          timeout_seconds,
          openrouter_provider,
          ...(input.route_id ? { route_id: input.route_id } : {}),
        }),
      );
    },
    async requestTest(
      input: {
        expected_version: number;
        profile_id: string;
        request_id: string;
      },
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const { expected_version, profile_id, request_id } = input;
      return parseTestRun(
        await request('runtime/tests', token, signal, 'POST', {
          expected_version,
          profile_id,
          request_id,
        }),
      );
    },
    async test(id: string, token: TokenProvider, signal?: AbortSignal) {
      return parseTestRun(
        await request(`runtime/tests/${encodeURIComponent(id)}`, token, signal),
      );
    },
    async activate(
      input: { expected_version: number; profile_id: string; test_id: string },
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const { expected_version, profile_id, test_id } = input;
      return parseRuntimeState(
        await request('runtime/activate', token, signal, 'POST', {
          expected_version,
          profile_id,
          test_id,
        }),
      );
    },
    async deactivate(
      expected_version: number,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseRuntimeState(
        await request('runtime/deactivate', token, signal, 'POST', {
          expected_version,
        }),
      );
    },
    async costs(
      filter: CostFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseCostReport(
        await request(`costs?${query(filter)}`, token, signal),
      );
    },
    async calls(
      filter: CostFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const v = object(await request(`calls?${query(filter)}`, token, signal));
      return { calls: array(v.calls, parseCall, 100), total: integer(v.total) };
    },
    async revisions(id: string, token: TokenProvider, signal?: AbortSignal) {
      const v = object(
        await request(
          `calls/${encodeURIComponent(id)}/revisions`,
          token,
          signal,
        ),
      );
      return array(v.revisions, parseRevision, 50);
    },
    async reconcile(id: string, token: TokenProvider, signal?: AbortSignal) {
      return parseCall(
        await request(
          `calls/${encodeURIComponent(id)}/reconcile`,
          token,
          signal,
          'POST',
          {},
        ),
      );
    },
  };
}
export type AIRuntimeAPI = ReturnType<typeof createAIRuntimeAPI>;
