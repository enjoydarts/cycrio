import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import { importHistoryID } from './import-executions-api.ts';
import { input } from './daily-api.ts';
import {
  parseRouteProof,
  type RouteProof,
  safeSource,
} from './ai-routes-api.ts';
import {
  modelProviders,
  parsePrice,
  type ModelProvider,
  type Price,
} from './catalog-api.ts';

export type PublicProcessingModel = {
  retention_source_url?: string;
  provider: ModelProvider;
  model_id: string;
  display_name: string;
  price: Price | null;
  retention: string;
  routing_provider: string;
};
export type PublicProcessingProfile = {
  id: string;
  primary: PublicProcessingModel;
  secondary: PublicProcessingModel;
  max_input_bytes: number;
  max_output_tokens: number;
  timeout_seconds: number;
};
export const blockedReasons = [
  '',
  'account_not_ready',
  'credentials_required',
  'folders_required',
  'runtime_inactive',
  'policy_disabled',
  'full_email_not_allowed',
  'policy_reconfirmation_required',
  'processing_active',
  'configuration_unavailable',
] as const;
export type ProcessingPreview = {
  full_email_allowed?: boolean;
  account_id: string;
  expected_version: number;
  runtime_version: number;
  profile_id: string | null;
  policy_version: number;
  ready: boolean;
  blocked_reason: (typeof blockedReasons)[number];
  pending_count: number;
  failed_count: number;
  unknown_count: number;
  primary: PublicProcessingModel | null;
  secondary: PublicProcessingModel | null;
  max_input_bytes: number;
  max_output_tokens: number;
  timeout_seconds: number;
};
export type ProcessingInput = {
  history_id?: string;
  full_email?: boolean;
  expected_version: number;
  runtime_version: number;
  profile_id: string;
  policy_version: number;
  request_id: string;
  max_messages: number;
  confirmed: boolean;
};
export type ProcessingRetryInput = ProcessingInput & {
  expected_result_id: string;
  confirm_unknown: boolean;
};
export type ProcessingRun = {
  id: string;
  account_id: string;
  state: 'queued' | 'leased' | 'running' | 'completed' | 'failed' | 'cancelled';
  max_messages: number;
  total_count: number;
  completed_count: number;
  related_count: number;
  unrelated_count: number;
  failed_count: number;
  unknown_count: number;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
};
export const processingErrorCodes = [
  '',
  'authentication_failed',
  'tls_failed',
  'connection_failed',
  'timeout',
  'invalid_response',
  'cancelled',
  'credentials_unavailable',
  'source_missing',
  'input_too_large',
  'unsupported_content',
  'configuration_unavailable',
  'execution_panic',
  'recording_failed',
  'lease_expired',
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
] as const;
export type ProcessingEvent = {
  service: string | null;
  recurring_amount?: string | null;
  recurring_currency?: string | null;
  contract_kind?: 'recurring' | 'fixed_term' | null;
  effective_date?: string | null;
  access_end_date?: string | null;
  plan: string | null;
  event_type:
    | 'invoice'
    | 'payment'
    | 'renewal'
    | 'refund'
    | 'change'
    | 'cancellation'
    | 'trial'
    | 'payment_failed'
    | 'payment_reminder'
    | 'suspension';
  amount: string | null;
  currency: string | null;
  billed_at: string | null;
  period_start: string | null;
  period_end: string | null;
  next_billing_at: string | null;
  interval_unit: 'day' | 'week' | 'month' | 'year' | null;
  interval_count: number | null;
};
export type ProcessingResult = {
  id: string;
  message_id: string;
  state: 'succeeded' | 'not_related' | 'failed' | 'unknown';
  classification: 'subscription_related' | 'not_related' | 'uncertain' | null;
  error_code: (typeof processingErrorCodes)[number];
  events: ProcessingEvent[];
  finished_at: string;
};
export type ProcessingPolicy = {
  full_email_allowed?: boolean;
  standard_proof?: StandardProof | null;
  profile_id: string;
  version: number;
  enabled: boolean;
  primary_retention: string;
  secondary_retention: string;
  primary_confirmed: boolean;
  secondary_confirmed: boolean;
};
export type PolicyState = {
  standard_terms?: {
    primary: StandardTerms;
    secondary_route: RouteProof | null;
    ready: boolean;
    blocked_reason:
      | ''
      | 'runtime_inactive'
      | 'custom_secondary_required'
      | 'route_confirmation_required';
  };
  runtime_version: number;
  profile: PublicProcessingProfile | null;
  policy: ProcessingPolicy | null;
};
export type StandardTerms = {
  id: string;
  version: string;
  source_url: string;
  summary: string;
  zdr: boolean;
  training_permitted: boolean;
  fixed_retention_days: number | null;
};
export type StandardProof = {
  full_email_allowed?: boolean;
  profile_id: string;
  policy_version: number;
  primary_mode: 'standard' | 'custom';
  primary_terms: StandardTerms | null;
  primary_retention: string;
  secondary_route: RouteProof;
  secondary_retention: string;
  actor_id: string;
  confirmed_at: string;
};
export type StandardPolicyInput = {
  full_email_allowed?: boolean;
  expected_version: number;
  runtime_version: number;
  profile_id: string;
  enabled: boolean;
  primary_mode: 'standard' | 'custom';
  primary_terms_id: string;
  primary_retention: string;
  primary_confirmed: boolean;
  secondary_route_id: string;
  secondary_confirmed: boolean;
};
export type PolicyInput = Omit<ProcessingPolicy, 'version'> & {
  expected_version: number;
};
const invalid = (): never => {
  throw new ApiError('dependency_unavailable');
};
const record = (v: unknown): Record<string, unknown> =>
  v !== null && typeof v === 'object' && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : invalid();
const str = (v: unknown): string => (typeof v === 'string' ? v : invalid());
const bool = (v: unknown): boolean => (typeof v === 'boolean' ? v : invalid());
const integer = (v: unknown, min = 0, max = Number.MAX_SAFE_INTEGER): number =>
  typeof v === 'number' && Number.isSafeInteger(v) && v >= min && v <= max
    ? v
    : invalid();
const choice = <T extends string>(v: unknown, values: readonly T[]): T =>
  typeof v === 'string' && values.includes(v as T) ? (v as T) : invalid();
const nullable = <T>(v: unknown, parse: (v: unknown) => T): T | null =>
  v === null ? null : parse(v);
const uuid = (v: unknown): string =>
  typeof v === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(v) &&
  v !== '00000000-0000-0000-0000-000000000000'
    ? v
    : invalid();
function date(v: unknown): string {
  if (
    typeof v !== 'string' ||
    !/^(?!0000)\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/.test(v) ||
    !Number.isFinite(Date.parse(`${v}T00:00:00Z`)) ||
    new Date(`${v}T00:00:00Z`).toISOString().slice(0, 10) !== v
  )
    return invalid();
  return v;
}
function timestamp(v: unknown): string {
  if (
    typeof v !== 'string' ||
    !/^\d{4}-\d{2}-\d{2}T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d{1,9})?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(
      v,
    ) ||
    !Number.isFinite(Date.parse(v))
  )
    return invalid();
  date(v.slice(0, 10));
  return v;
}
const text = (v: unknown, max: number, empty = false): string => {
  const s = str(v);
  return [...s].length <= max &&
    (empty || s.trim().length > 0) &&
    !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(
      s,
    )
    ? s
    : invalid();
};
export const validRetention = (v: string, required = true) =>
  [...v].length <= 500 &&
  (!required || !!v.trim()) &&
  !/[\p{Cc}\p{Cs}]/u.test(v);
function retention(v: unknown): string {
  const s = text(v, 500, true);
  return validRetention(s, false) ? s : invalid();
}
function model(v: unknown, primary: boolean): PublicProcessingModel {
  const m = record(v),
    provider = choice(
      m.provider,
      primary
        ? (['liquid'] as const)
        : modelProviders.filter((p) => p !== 'liquid'),
    );
  const routing = str(m.routing_provider);
  if (
    provider === 'openrouter'
      ? routing.length > 128 ||
        !/^[a-z0-9][a-z0-9._-]*(?:\/[a-z0-9][a-z0-9._-]*){0,3}$/.test(routing)
      : routing !== ''
  )
    return invalid();
  return {
    ...(m.retention_source_url === undefined
      ? {}
      : { retention_source_url: safeSource(m.retention_source_url) }),
    provider,
    model_id: text(m.model_id, 1024),
    display_name: text(m.display_name, 1024),
    price: nullable(m.price, parsePrice),
    retention: retention(m.retention),
    routing_provider: routing,
  };
}
function limits(v: Record<string, unknown>, inactive = false) {
  return {
    max_input_bytes: integer(
      v.max_input_bytes,
      inactive ? 0 : 1024,
      inactive ? 0 : 65536,
    ),
    max_output_tokens: integer(
      v.max_output_tokens,
      inactive ? 0 : 64,
      inactive ? 0 : 16384,
    ),
    timeout_seconds: integer(
      v.timeout_seconds,
      inactive ? 0 : 1,
      inactive ? 0 : 120,
    ),
  };
}
export function parseProcessingPreview(
  value: unknown,
  accountID: string,
): ProcessingPreview {
  const v = record(value);
  if (uuid(v.account_id) !== uuid(accountID)) return invalid();
  const profile_id = nullable(v.profile_id, uuid),
    ready = bool(v.ready),
    blocked_reason = choice(v.blocked_reason, blockedReasons);
  const primary = nullable(v.primary, (x) => model(x, true)),
    secondary = nullable(v.secondary, (x) => model(x, false));
  if (
    (profile_id === null) !== (primary === null) ||
    (profile_id === null) !== (secondary === null) ||
    ready !== (blocked_reason === '') ||
    (ready && (!profile_id || !v.policy_version || !v.runtime_version))
  )
    return invalid();
  return {
    account_id: accountID,
    full_email_allowed:
      v.full_email_allowed === undefined ? false : bool(v.full_email_allowed),
    expected_version: integer(v.expected_version, 1),
    runtime_version: integer(v.runtime_version),
    profile_id,
    policy_version: integer(v.policy_version),
    ready,
    blocked_reason,
    pending_count: integer(v.pending_count),
    failed_count: integer(v.failed_count),
    unknown_count: integer(v.unknown_count),
    primary,
    secondary,
    ...limits(v, profile_id === null),
  };
}
export function parseProcessingRun(
  value: unknown,
  accountID: string,
): ProcessingRun {
  const v = record(value);
  if (uuid(v.account_id) !== uuid(accountID)) return invalid();
  const max_messages = integer(v.max_messages, 1),
    total_count = integer(v.total_count, 0, max_messages),
    completed_count = integer(v.completed_count, 0, total_count);
  const related_count = integer(v.related_count, 0, completed_count),
    unrelated_count = integer(v.unrelated_count, 0, completed_count),
    failed_count = integer(v.failed_count, 0, completed_count),
    unknown_count = integer(v.unknown_count, 0, completed_count);
  if (
    related_count + unrelated_count + failed_count + unknown_count !==
    completed_count
  )
    return invalid();
  return {
    id: uuid(v.id),
    account_id: accountID,
    state: choice(v.state, [
      'queued',
      'leased',
      'running',
      'completed',
      'failed',
      'cancelled',
    ]),
    max_messages,
    total_count,
    completed_count,
    related_count,
    unrelated_count,
    failed_count,
    unknown_count,
    created_at: timestamp(v.created_at),
    started_at: nullable(v.started_at, timestamp),
    finished_at: nullable(v.finished_at, timestamp),
  };
}
const currencies = new Set(
  'AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BHD BIF BMD BND BOB BOV BRL BSD BTN BWP BYN BZD CAD CDF CHE CHF CHW CLF CLP CNY COP COU CRC CUP CVE CZK DJF DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GNF GTQ GYD HKD HNL HTG HUF IDR ILS INR IQD IRR ISK JMD JOD JPY KES KGS KHR KMF KPW KRW KWD KYD KZT LAK LBP LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MXV MYR MZN NAD NGN NIO NOK NPR NZD OMR PAB PEN PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP SLE SOS SRD SSP STN SVC SYP SZL THB TJS TMT TND TOP TRY TTD TWD TZS UAH UGX USD USN UYI UYU UYW UZS VED VES VND VUV WST XAF XCD XCG XDR XOF XPF XSU XUA YER ZAR ZMW ZWG'.split(
    ' ',
  ),
);
export function parseProcessingEvent(value: unknown): ProcessingEvent {
  const v = record(value);
  return {
    service: nullable(v.service, (x) => text(x, 128)),
    recurring_amount: nullable(v.recurring_amount ?? null, (x) =>
      typeof x === 'string' && /^(0|[1-9][0-9]{0,17})(\.[0-9]{1,18})?$/.test(x)
        ? x
        : invalid(),
    ),
    recurring_currency: nullable(v.recurring_currency ?? null, (x) =>
      typeof x === 'string' && currencies.has(x) ? x : invalid(),
    ),
    contract_kind: nullable(v.contract_kind ?? null, (x) =>
      choice(x, ['recurring', 'fixed_term'] as const),
    ),
    effective_date: nullable(v.effective_date ?? null, date),
    access_end_date: nullable(v.access_end_date ?? null, date),
    plan: nullable(v.plan, (x) => text(x, 128)),
    event_type: choice(v.event_type, [
      'invoice',
      'payment',
      'renewal',
      'refund',
      'change',
      'cancellation',
      'trial',
      'payment_failed',
      'payment_reminder',
      'suspension',
    ]),
    amount: nullable(v.amount, (x) =>
      typeof x === 'string' && /^(0|[1-9][0-9]{0,17})(\.[0-9]{1,18})?$/.test(x)
        ? x
        : invalid(),
    ),
    currency: nullable(v.currency, (x) =>
      typeof x === 'string' && currencies.has(x) ? x : invalid(),
    ),
    billed_at: nullable(v.billed_at, date),
    period_start: nullable(v.period_start, date),
    period_end: nullable(v.period_end, date),
    next_billing_at: nullable(v.next_billing_at, date),
    interval_unit: nullable(v.interval_unit, (x) =>
      choice(x, ['day', 'week', 'month', 'year']),
    ),
    interval_count: nullable(v.interval_count, (x) => integer(x, 1, 10000)),
  };
}
function array<T>(v: unknown, parse: (v: unknown) => T, max: number): T[] {
  return Array.isArray(v) && v.length <= max ? v.map(parse) : invalid();
}
export function parseProcessingResult(value: unknown): ProcessingResult {
  const v = record(value);
  return {
    id: uuid(v.id),
    message_id: uuid(v.message_id),
    state: choice(v.state, ['succeeded', 'not_related', 'failed', 'unknown']),
    classification: nullable(v.classification, (x) =>
      choice(x, ['subscription_related', 'not_related', 'uncertain']),
    ),
    error_code: choice(v.error_code, processingErrorCodes),
    events: array(v.events, parseProcessingEvent, 32),
    finished_at: timestamp(v.finished_at),
  };
}
export function parsePolicyState(value: unknown): PolicyState {
  const v = record(value);
  const profile = nullable(v.profile, (x) => {
    const p = record(x);
    return {
      id: uuid(p.id),
      primary: model(p.primary, true),
      secondary: model(p.secondary, false),
      ...limits(p),
    };
  });
  const policy = nullable(v.policy, (x) => {
    const p = record(x);
    return {
      ...(p.standard_proof === undefined
        ? {}
        : { standard_proof: nullable(p.standard_proof, parseStandardProof) }),
      profile_id: uuid(p.profile_id),
      full_email_allowed:
        p.full_email_allowed === undefined ? false : bool(p.full_email_allowed),
      version: integer(p.version),
      enabled: bool(p.enabled),
      primary_retention: retention(p.primary_retention),
      secondary_retention: retention(p.secondary_retention),
      primary_confirmed: bool(p.primary_confirmed),
      secondary_confirmed: bool(p.secondary_confirmed),
    };
  });
  if (policy && (!profile || policy.profile_id !== profile.id))
    return invalid();
  if (
    policy?.enabled &&
    (!policy.version ||
      !policy.primary_confirmed ||
      !policy.secondary_confirmed ||
      !validRetention(policy.primary_retention) ||
      !validRetention(policy.secondary_retention))
  )
    return invalid();
  return {
    runtime_version: integer(v.runtime_version),
    profile,
    policy,
    ...(v.standard_terms === undefined
      ? {}
      : { standard_terms: parseStandardTermsState(v.standard_terms) }),
  };
}
function parseTerms(value: unknown): StandardTerms {
  const v = record(value);
  return {
    id: text(v.id, 200),
    version: text(v.version, 100),
    source_url: safeSource(v.source_url),
    summary: text(v.summary, 2000),
    zdr: bool(v.zdr),
    training_permitted: bool(v.training_permitted),
    fixed_retention_days: nullable(v.fixed_retention_days, integer),
  };
}
function parseStandardProof(value: unknown): StandardProof {
  const v = record(value);
  return {
    full_email_allowed:
      v.full_email_allowed === undefined ? false : bool(v.full_email_allowed),
    profile_id: uuid(v.profile_id),
    policy_version: integer(v.policy_version),
    primary_mode: choice(v.primary_mode, ['standard', 'custom']),
    primary_terms: nullable(v.primary_terms, parseTerms),
    primary_retention: retention(v.primary_retention),
    secondary_route: parseRouteProof(v.secondary_route),
    secondary_retention: retention(v.secondary_retention),
    actor_id: uuid(v.actor_id),
    confirmed_at: timestamp(v.confirmed_at),
  };
}
function parseStandardTermsState(
  value: unknown,
): NonNullable<PolicyState['standard_terms']> {
  const v = record(value);
  return {
    primary: parseTerms(v.primary),
    secondary_route: nullable(v.secondary_route, parseRouteProof),
    ready: bool(v.ready),
    blocked_reason: choice(v.blocked_reason, [
      '',
      'runtime_inactive',
      'custom_secondary_required',
      'route_confirmation_required',
    ]),
  };
}
function processingBody(input: ProcessingInput): ProcessingInput {
  return {
    ...(input.full_email === undefined
      ? {}
      : { full_email: bool(input.full_email) }),
    expected_version: integer(input.expected_version, 1),
    runtime_version: integer(input.runtime_version, 1),
    profile_id: uuid(input.profile_id),
    policy_version: integer(input.policy_version, 1),
    request_id: uuid(input.request_id),
    max_messages: integer(input.max_messages, 1, 100),
    confirmed: input.confirmed === true ? true : invalid(),
  };
}
export function createMailProcessingAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  const pathID = (id: string) => {
    try {
      return uuid(id);
    } catch {
      throw new ApiError('invalid_request');
    }
  };
  const accountPath = (id: string, suffix: string) =>
    `/v1/mail/accounts/${pathID(id)}/processing-${suffix}`;
  async function request(
    path: string,
    token: TokenProvider,
    signal?: AbortSignal,
    method = 'GET',
    body?: object,
  ): Promise<unknown> {
    try {
      const bearer = await token();
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (!bearer) throw new ApiError('authentication_required');
      const response = await fetcher(`${base.replace(/\/$/, '')}${path}`, {
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
      });
      if (!response.ok) {
        const codes: Record<number, ErrorCode> = {
          400: 'invalid_request',
          401: 'authentication_required',
          403: 'access_denied',
          404: 'not_found',
          409: 'conflict',
        };
        throw new ApiError(codes[response.status] ?? 'dependency_unavailable');
      }
      return await response.json();
    } catch (error) {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (error instanceof ApiError) throw error;
      return invalid();
    }
  }
  const inputBody = (input: ProcessingInput) => {
    try {
      return processingBody(input);
    } catch {
      throw new ApiError('invalid_request');
    }
  };
  return {
    async retryFailed(
      id: string,
      body: ProcessingInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const v = record(
        await request(accountPath(id, 'retry-failed'), token, signal, 'POST', {
          ...inputBody(body),
          ...(body.history_id === undefined
            ? {}
            : { history_id: input(() => importHistoryID(body.history_id)) }),
        }),
      );
      return {
        run: parseProcessingRun(v.run, id),
        accepted_count: integer(v.accepted_count),
        skipped_count: integer(v.skipped_count),
      };
    },
    async saveStandardPolicy(
      input: StandardPolicyInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      let body: StandardPolicyInput;
      try {
        body = {
          expected_version: integer(input.expected_version),
          runtime_version: integer(input.runtime_version, 1),
          ...(input.full_email_allowed === undefined
            ? {}
            : { full_email_allowed: bool(input.full_email_allowed) }),
          profile_id: uuid(input.profile_id),
          enabled: bool(input.enabled),
          primary_mode: choice(input.primary_mode, ['standard', 'custom']),
          primary_terms_id: str(input.primary_terms_id),
          primary_retention: retention(input.primary_retention),
          primary_confirmed: bool(input.primary_confirmed),
          secondary_route_id: uuid(input.secondary_route_id),
          secondary_confirmed: bool(input.secondary_confirmed),
        };
        if (
          !body.primary_confirmed ||
          !body.secondary_confirmed ||
          (body.primary_mode === 'standard'
            ? !body.primary_terms_id.trim() || body.primary_retention !== ''
            : body.primary_terms_id !== '' ||
              !validRetention(body.primary_retention))
        )
          return invalid();
      } catch {
        throw new ApiError('invalid_request');
      }
      return parsePolicyState(
        await request(
          '/v1/admin/mail-processing/standard-policy',
          token,
          signal,
          'PUT',
          body,
        ),
      );
    },
    async preview(id: string, token: TokenProvider, signal?: AbortSignal) {
      return parseProcessingPreview(
        await request(accountPath(id, 'preview'), token, signal),
        id,
      );
    },
    async runs(id: string, token: TokenProvider, signal?: AbortSignal) {
      const v = record(await request(accountPath(id, 'runs'), token, signal));
      const values = array(v.runs, (x) => parseProcessingRun(x, id), 20);
      if (new Set(values.map((r) => r.id)).size !== values.length)
        return invalid();
      return values;
    },
    async results(id: string, token: TokenProvider, signal?: AbortSignal) {
      const v = record(
        await request(accountPath(id, 'results'), token, signal),
      );
      const values = array(v.results, parseProcessingResult, 100);
      if (new Set(values.map((r) => r.message_id)).size !== values.length)
        return invalid();
      return values;
    },
    async start(
      id: string,
      input: ProcessingInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseProcessingRun(
        await request(
          accountPath(id, 'runs'),
          token,
          signal,
          'POST',
          inputBody(input),
        ),
        id,
      );
    },
    async retry(
      id: string,
      messageID: string,
      input: ProcessingRetryInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = inputBody(input);
      if (body.max_messages !== 1 || typeof input.confirm_unknown !== 'boolean')
        throw new ApiError('invalid_request');
      return parseProcessingRun(
        await request(
          accountPath(id, `results/${pathID(messageID)}/retry`),
          token,
          signal,
          'POST',
          {
            ...body,
            expected_result_id: pathID(input.expected_result_id),
            confirm_unknown: input.confirm_unknown,
          },
        ),
        id,
      );
    },
    async cancel(
      id: string,
      runID: string,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const run = parseProcessingRun(
        await request(
          accountPath(id, `runs/${pathID(runID)}/cancel`),
          token,
          signal,
          'POST',
          {},
        ),
        id,
      );
      return run.id === runID ? run : invalid();
    },
    async policy(token: TokenProvider, signal?: AbortSignal) {
      return parsePolicyState(
        await request('/v1/admin/mail-processing/policy', token, signal),
      );
    },
    async savePolicy(
      input: PolicyInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      let body: PolicyInput;
      try {
        body = {
          expected_version: integer(input.expected_version),
          profile_id: uuid(input.profile_id),
          ...(input.full_email_allowed === undefined
            ? {}
            : { full_email_allowed: bool(input.full_email_allowed) }),
          enabled: bool(input.enabled),
          primary_retention: retention(input.primary_retention),
          secondary_retention: retention(input.secondary_retention),
          primary_confirmed: bool(input.primary_confirmed),
          secondary_confirmed: bool(input.secondary_confirmed),
        };
        if (
          body.enabled &&
          (!validRetention(body.primary_retention) ||
            !validRetention(body.secondary_retention) ||
            !body.primary_confirmed ||
            !body.secondary_confirmed)
        )
          return invalid();
      } catch {
        throw new ApiError('invalid_request');
      }
      return parsePolicyState(
        await request(
          '/v1/admin/mail-processing/policy',
          token,
          signal,
          'PUT',
          body,
        ),
      );
    },
  };
}
export type MailProcessingAPI = ReturnType<typeof createMailProcessingAPI>;
