import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import {
  choice,
  date,
  input,
  integer,
  invalid,
  nullable,
  record,
  timestamp,
  uuid,
} from './daily-api.ts';
import { parseSubscription, type Subscription } from './subscriptions-api.ts';
import {
  parseHistoryBasis,
  parseHistoryEvidence,
  type HistoryBasis,
  type HistoryEvidence,
} from './schedule-history-api.ts';
import {
  parseScheduleRule,
  sameScheduleRule,
  type ScheduleRule,
} from './schedule-rule.ts';
export {
  parseScheduleRule,
  sameScheduleRule,
  type ScheduleRule,
} from './schedule-rule.ts';
export type ScheduleSettings = {
  history_evidence?: HistoryEvidence;
  rule: ScheduleRule;
  stale: boolean;
  confirmed_as_of: string;
  confirmed_candidate: string;
  algorithm_version: 'calendar-v1';
  confirmed_version: number;
  confirmed_at: string;
  updated_at: string;
};
export type ScheduleView = {
  subscription: Subscription;
  settings: ScheduleSettings | null;
  as_of: string;
  timezone: 'Asia/Tokyo';
  next_date_source: 'unknown' | 'manual' | 'confirmed_calculation';
  overdue: boolean;
};
export type SchedulePreview = {
  subscription: Subscription;
  rule: ScheduleRule;
  as_of: string;
  timezone: 'Asia/Tokyo';
  candidate_date: string | null;
  reason: '' | 'ended' | 'out_of_range' | 'fixed_term';
  algorithm_version: 'calendar-v1';
  overdue: boolean;
};
export type ScheduleSave = ScheduleRule & {
  history_basis?: HistoryBasis;
  request_id: string;
  expected_version: number;
  expected_as_of: string;
  expected_candidate: string;
  confirmed: true;
};
export type ScheduleReceipt = {
  history_evidence?: HistoryEvidence;
  request_id: string;
  applied_version: number;
  rule: ScheduleRule;
  as_of: string;
  candidate_date: string;
  algorithm_version: 'calendar-v1';
  created_at: string;
};
export type ScheduleSaveResult = {
  receipt: ScheduleReceipt;
  current: ScheduleView;
};
export type ScheduleReason =
  | 'version_conflict'
  | 'preview_expired'
  | 'preview_changed'
  | 'request_conflict'
  | 'history_changed'
  | 'ended'
  | 'fixed_term';
const reasons: ScheduleReason[] = [
  'version_conflict',
  'preview_expired',
  'preview_changed',
  'request_conflict',
  'history_changed',
  'ended',
  'fixed_term',
];
export class ScheduleError extends ApiError {
  reason: ScheduleReason | null;
  constructor(code: ErrorCode, reason: ScheduleReason | null = null) {
    super(code);
    this.reason = reason;
  }
}
const boolean = (v: unknown): boolean =>
  typeof v === 'boolean' ? v : invalid();
const algorithm = (v: unknown) => choice(v, ['calendar-v1'] as const);
export function parseScheduleView(value: unknown): ScheduleView {
  const v = record(value),
    subscription = parseSubscription(v.subscription);
  const settings = nullable(v.settings, (x): ScheduleSettings => {
    const s = record(x),
      confirmed_version = integer(s.confirmed_version, 1, subscription.version);
    const confirmed_as_of = date(s.confirmed_as_of),
      confirmed_candidate = date(s.confirmed_candidate),
      rule = parseScheduleRule(s.rule);
    if (confirmed_candidate < confirmed_as_of) return invalid();
    return {
      rule,
      ...(s.history_evidence === undefined
        ? {}
        : {
            history_evidence: parseHistoryEvidence(
              s.history_evidence,
              rule,
              confirmed_as_of,
            ),
          }),
      stale: boolean(s.stale),
      confirmed_as_of,
      confirmed_candidate,
      algorithm_version: algorithm(s.algorithm_version),
      confirmed_version,
      confirmed_at: timestamp(s.confirmed_at),
      updated_at: timestamp(s.updated_at),
    };
  });
  const next_date_source = choice(v.next_date_source, [
    'unknown',
    'manual',
    'confirmed_calculation',
  ] as const);
  if (
    settings &&
    !settings.stale &&
    (settings.rule.interval_unit !== subscription.interval_unit ||
      settings.rule.interval_count !== subscription.interval_count)
  )
    return invalid();
  if (
    next_date_source === 'confirmed_calculation' &&
    (!settings ||
      settings.stale ||
      settings.confirmed_candidate !== subscription.next_billing_date)
  )
    return invalid();
  return {
    subscription,
    settings,
    as_of: date(v.as_of),
    timezone: choice(v.timezone, ['Asia/Tokyo'] as const),
    next_date_source,
    overdue: boolean(v.overdue),
  };
}
export function parseSchedulePreview(value: unknown): SchedulePreview {
  const v = record(value),
    as_of = date(v.as_of),
    candidate_date = nullable(v.candidate_date, date),
    reason = choice(v.reason, [
      '',
      'ended',
      'out_of_range',
      'fixed_term',
    ] as const);
  if (
    (candidate_date === null) !== (reason !== '') ||
    (candidate_date && candidate_date < as_of)
  )
    return invalid();
  return {
    subscription: parseSubscription(v.subscription),
    rule: parseScheduleRule(v.rule),
    as_of,
    timezone: choice(v.timezone, ['Asia/Tokyo'] as const),
    candidate_date,
    reason,
    algorithm_version: algorithm(v.algorithm_version),
    overdue: boolean(v.overdue),
  };
}
export function parseScheduleSaveResult(value: unknown): ScheduleSaveResult {
  const v = record(value),
    r = record(v.receipt),
    current = parseScheduleView(v.current);
  const receipt: ScheduleReceipt = {
    request_id: uuid(r.request_id),
    applied_version: integer(
      r.applied_version,
      2,
      current.subscription.version,
    ),
    rule: parseScheduleRule(r.rule),
    as_of: date(r.as_of),
    candidate_date: date(r.candidate_date),
    algorithm_version: algorithm(r.algorithm_version),
    created_at: timestamp(r.created_at),
  };
  if (receipt.candidate_date < receipt.as_of) return invalid();
  if (r.history_evidence !== undefined)
    receipt.history_evidence = parseHistoryEvidence(
      r.history_evidence,
      receipt.rule,
      receipt.as_of,
    );
  return { receipt, current };
}
export function createScheduleAPI(base: string, fetcher: typeof fetch = fetch) {
  const path = (id: string) =>
    `/v1/subscriptions/${input(() => uuid(id))}/billing-schedule`;
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
      if (!bearer) throw new ScheduleError('authentication_required');
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
        let reason: ScheduleReason | null = null;
        if (response.status === 409) {
          try {
            const v = record(await response.json());
            if (reasons.includes(v.reason as ScheduleReason))
              reason = v.reason as ScheduleReason;
          } catch {
            /* Untrusted error text is never displayed. */
          }
        }
        throw new ScheduleError(
          codes[response.status] ?? 'dependency_unavailable',
          reason,
        );
      }
      return await response.json();
    } catch (error) {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (error instanceof ApiError) throw error;
      return invalid();
    }
  }
  const identity = <T extends { subscription: Subscription }>(
    v: T,
    id: string,
  ): T => (v.subscription.id === id ? v : invalid());
  return {
    async get(id: string, token: TokenProvider, signal?: AbortSignal) {
      return identity(
        parseScheduleView(await request(path(id), token, signal)),
        id,
      );
    },
    async preview(
      id: string,
      rule: ScheduleRule,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => parseScheduleRule(rule)),
        result = identity(
          parseSchedulePreview(
            await request(`${path(id)}/preview`, token, signal, 'POST', body),
          ),
          id,
        );
      return sameScheduleRule(body, result.rule) ? result : invalid();
    },
    async save(
      id: string,
      value: ScheduleSave,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body: ScheduleSave = input(() => ({
        ...parseScheduleRule(value),
        request_id: uuid(value.request_id),
        expected_version: integer(value.expected_version, 1),
        expected_as_of: date(value.expected_as_of),
        expected_candidate: date(value.expected_candidate),
        confirmed: value.confirmed === true ? true : invalid(),
        ...(value.history_basis === undefined
          ? {}
          : { history_basis: parseHistoryBasis(value.history_basis) }),
      }));
      if (body.expected_candidate < body.expected_as_of)
        throw new ApiError('invalid_request');
      const result = parseScheduleSaveResult(
          await request(path(id), token, signal, 'PUT', body),
        ),
        r = result.receipt;
      identity(result.current, id);
      return r.request_id === body.request_id &&
        r.applied_version === body.expected_version + 1 &&
        r.as_of === body.expected_as_of &&
        r.candidate_date === body.expected_candidate &&
        sameScheduleRule(r.rule, body) &&
        (body.history_basis === undefined
          ? r.history_evidence === undefined
          : !!r.history_evidence &&
            JSON.stringify(parseHistoryBasis(r.history_evidence)) ===
              JSON.stringify(body.history_basis))
        ? result
        : invalid();
    },
  };
}
export type ScheduleAPI = ReturnType<typeof createScheduleAPI>;
