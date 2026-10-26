import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import {
  parseProcessingEvent,
  type ProcessingEvent,
} from './mail-processing-api.ts';
import {
  array,
  choice,
  date,
  input,
  integer,
  invalid,
  nullable,
  pagination,
  record,
  timestamp,
  uuid,
} from './daily-api.ts';
export type BillingKind = 'invoice' | 'payment' | 'refund';
export type BillingValues = {
  kind: BillingKind;
  occurred_on: string;
  amount: string;
  currency: string;
  refund_of_id: string | null;
  state: 'active' | 'voided';
};
export type BillingSource = {
  result_id: string;
  event_index: number;
  original_event: ProcessingEvent;
};
export type BillingEntry = BillingValues & {
  id: string;
  subscription_id: string;
  version: number;
  sources: BillingSource[];
  created_at: string;
  updated_at: string;
};
export type BillingFilter = {
  kind: BillingKind | 'all';
  state: 'active' | 'voided' | 'all';
  month: string;
  source_result_id?: string;
  source_event_index?: number;
  limit: number;
  offset: number;
};
export type BillingPage = {
  events: BillingEntry[];
  total: number;
  limit: number;
  offset: number;
};
export type DuplicateFilter = Pick<
  BillingValues,
  'kind' | 'occurred_on' | 'amount' | 'currency'
> & { exclude_id: string | null; limit: number; offset: number };
export type DuplicatePage = BillingPage & { token: string };
export type BillingCreate = BillingValues & {
  request_id: string;
  source_result_id: string | null;
  source_event_index: number | null;
  confirmed: boolean;
  duplicate_confirmed: boolean;
  duplicate_token: string | null;
};
export type BillingUpdate = BillingValues & {
  expected_version: number;
  confirmed: boolean;
  duplicate_confirmed: boolean;
  duplicate_token: string | null;
};
export type BillingSourceInput = {
  expected_version: number;
  source_result_id: string;
  source_event_index: number;
  confirmed: boolean;
};
export type BillingSummary = {
  month: string;
  start_date: string;
  end_date: string;
  timezone: 'Asia/Tokyo';
  subscription_id: string | null;
  currencies: {
    currency: string;
    payment_amount: string;
    refund_amount: string;
    net_amount: string;
    payment_count: number;
    refund_count: number;
  }[];
  invoice_count: number;
  voided_count: number;
};
const reasons = [
  'version_conflict',
  'possible_duplicate',
  'refund_conflict',
  'source_conflict',
  'request_conflict',
] as const;
export type BillingReason = (typeof reasons)[number];
export class BillingError extends ApiError {
  readonly reason: BillingReason | null;
  constructor(code: ErrorCode, reason: BillingReason | null = null) {
    super(code);
    this.reason = reason;
  }
}
export const billingCurrency = (v: unknown): string =>
  typeof v === 'string' && /^[A-Z]{3}$/.test(v) ? v : invalid();
const fingerprint = (v: unknown): string =>
  typeof v === 'string' && /^[a-f0-9]{64}$/.test(v) ? v : invalid();
const exactAmount = (v: unknown): string =>
  typeof v === 'string' && /^\d{1,24}(\.\d{1,12})?$/.test(v) ? v : invalid();
export const billingMonth = (v: unknown): string => {
  if (typeof v !== 'string' || !/^(?!0000)\d{4}-(?:0[1-9]|1[0-2])$/.test(v))
    return invalid();
  return v;
};
export function parseBillingValues(value: unknown): BillingValues {
  const v = record(value),
    kind = choice(v.kind, ['invoice', 'payment', 'refund']),
    refund_of_id = nullable(v.refund_of_id, uuid);
  if ((kind === 'refund') !== (refund_of_id !== null)) return invalid();
  return {
    kind,
    occurred_on: date(v.occurred_on),
    amount: exactAmount(v.amount),
    currency: billingCurrency(v.currency),
    refund_of_id,
    state: choice(v.state, ['active', 'voided']),
  };
}
export function parseBillingEntry(value: unknown): BillingEntry {
  const v = record(value),
    sources = array(
      v.sources,
      (item) => {
        const s = record(item);
        return {
          result_id: uuid(s.result_id),
          event_index: integer(s.event_index, 0, 31),
          original_event: parseProcessingEvent(s.original_event),
        };
      },
      Number.MAX_SAFE_INTEGER,
    );
  if (
    new Set(sources.map((s) => `${s.result_id}/${s.event_index}`)).size !==
    sources.length
  )
    return invalid();
  return {
    ...parseBillingValues(v),
    id: uuid(v.id),
    subscription_id: uuid(v.subscription_id),
    version: integer(v.version, 1),
    sources,
    created_at: timestamp(v.created_at),
    updated_at: timestamp(v.updated_at),
  };
}
export function parseBillingSummary(value: unknown): BillingSummary {
  const v = record(value),
    month = billingMonth(v.month),
    start_date = date(v.start_date),
    end_date = v.end_date === '10000-01-01' ? v.end_date : date(v.end_date);
  const [year, part] = month.split('-').map(Number),
    nextMonth =
      part === 12
        ? `${String(year + 1).padStart(4, '0')}-01-01`
        : `${String(year).padStart(4, '0')}-${String(part + 1).padStart(2, '0')}-01`;
  if (start_date !== `${month}-01` || end_date !== nextMonth) return invalid();
  const sum = (x: unknown, signed = false) =>
    typeof x === 'string' &&
    (signed ? /^-?\d+(\.\d{1,12})?$/ : /^\d+(\.\d{1,12})?$/).test(x)
      ? x
      : invalid();
  const currencies = array(
    v.currencies,
    (item) => {
      const c = record(item);
      return {
        currency: billingCurrency(c.currency),
        payment_amount: sum(c.payment_amount),
        refund_amount: sum(c.refund_amount),
        net_amount: sum(c.net_amount, true),
        payment_count: integer(c.payment_count),
        refund_count: integer(c.refund_count),
      };
    },
    Number.MAX_SAFE_INTEGER,
  );
  if (new Set(currencies.map((c) => c.currency)).size !== currencies.length)
    return invalid();
  return {
    month,
    start_date,
    end_date,
    timezone: choice(v.timezone, ['Asia/Tokyo']),
    subscription_id: nullable(v.subscription_id, uuid),
    currencies,
    invoice_count: integer(v.invoice_count),
    voided_count: integer(v.voided_count),
  };
}
const decimalKey = (value: string) => {
  const [whole, fraction = ''] = value.split('.');
  return `${whole.replace(/^0+(?=\d)/, '')}.${fraction.replace(/0+$/, '')}`;
};
export function createBillingAPI(base: string, fetcher: typeof fetch = fetch) {
  const path = (sid: string, eid?: string) =>
    `/v1/subscriptions/${input(() => uuid(sid))}/billing-events${eid !== undefined ? `/${input(() => uuid(eid))}` : ''}`;
  async function request(
    url: string,
    token: TokenProvider,
    signal?: AbortSignal,
    method = 'GET',
    body?: object,
  ): Promise<unknown> {
    try {
      const bearer = await token();
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (!bearer) throw new BillingError('authentication_required');
      const response = await fetcher(`${base.replace(/\/$/, '')}${url}`, {
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
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (!response.ok) {
        const codes: Record<number, ErrorCode> = {
          400: 'invalid_request',
          401: 'authentication_required',
          403: 'access_denied',
          404: 'not_found',
          409: 'conflict',
        };
        let reason: BillingReason | null = null;
        if (response.status === 409) {
          try {
            const error = record(await response.json());
            if (reasons.includes(error.reason as BillingReason))
              reason = error.reason as BillingReason;
          } catch {
            /* Error bodies are optional; never expose upstream messages. */
          }
        }
        throw new BillingError(
          codes[response.status] ?? 'dependency_unavailable',
          reason,
        );
      }
      const result: unknown = await response.json();
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      return result;
    } catch (error) {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (error instanceof ApiError) throw error;
      throw new BillingError('dependency_unavailable');
    }
  }
  const identity = (v: unknown, sid: string, eid?: string) => {
    const entry = parseBillingEntry(v);
    return entry.subscription_id === sid && (!eid || entry.id === eid)
      ? entry
      : invalid();
  };
  const page = (
    body: unknown,
    sid: string,
    limit: number,
    offset: number,
  ): BillingPage => {
    const v = record(body),
      p = pagination(v),
      events = array(v.events, (x) => identity(x, sid), limit);
    if (
      p.limit !== limit ||
      p.offset !== offset ||
      events.length > Math.max(0, p.total - p.offset) ||
      new Set(events.map((e) => e.id)).size !== events.length
    )
      return invalid();
    return { events, ...p };
  };
  const confirm = (v: boolean) => (v === true ? true : invalid());
  const duplicate = (v: {
    duplicate_confirmed: boolean;
    duplicate_token: string | null;
  }) => ({
    duplicate_confirmed:
      typeof v.duplicate_confirmed === 'boolean'
        ? v.duplicate_confirmed
        : invalid(),
    duplicate_token: nullable(v.duplicate_token, fingerprint),
  });
  const sourceInput = (v: BillingSourceInput) =>
    input(() => ({
      expected_version: integer(v.expected_version, 1),
      source_result_id: uuid(v.source_result_id),
      source_event_index: integer(v.source_event_index, 0, 31),
      confirmed: confirm(v.confirmed),
    }));
  return {
    async list(
      sid: string,
      filter: BillingFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<BillingPage> {
      const query = input(() => {
        const kind = choice(filter.kind, [
            'all',
            'invoice',
            'payment',
            'refund',
          ]),
          state = choice(filter.state, ['all', 'active', 'voided']);
        if (
          (filter.source_result_id === undefined) !==
          (filter.source_event_index === undefined)
        )
          return invalid();
        return new URLSearchParams({
          ...(kind !== 'all' ? { kind } : {}),
          ...(state !== 'all' ? { state } : {}),
          ...(filter.month ? { month: billingMonth(filter.month) } : {}),
          ...(filter.source_result_id !== undefined
            ? {
                source_result_id: uuid(filter.source_result_id),
                source_event_index: String(
                  integer(filter.source_event_index, 0, 31),
                ),
              }
            : {}),
          limit: String(integer(filter.limit, 1, 100)),
          offset: String(integer(filter.offset)),
        });
      });
      const result = page(
        await request(`${path(sid)}?${query}`, token, signal),
        sid,
        filter.limit,
        filter.offset,
      );
      for (const e of result.events)
        if (
          (filter.kind !== 'all' && e.kind !== filter.kind) ||
          (filter.state !== 'all' && e.state !== filter.state) ||
          (filter.month && !e.occurred_on.startsWith(`${filter.month}-`)) ||
          (filter.source_result_id !== undefined &&
            !e.sources.some(
              (s) =>
                s.result_id === filter.source_result_id &&
                s.event_index === filter.source_event_index,
            ))
        )
          return invalid();
      return result;
    },
    async get(
      sid: string,
      eid: string,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return identity(await request(path(sid, eid), token, signal), sid, eid);
    },
    async create(
      sid: string,
      v: BillingCreate,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => {
        const source_result_id = nullable(v.source_result_id, uuid),
          source_event_index = nullable(v.source_event_index, (x) =>
            integer(x, 0, 31),
          );
        if ((source_result_id === null) !== (source_event_index === null))
          return invalid();
        return {
          ...parseBillingValues(v),
          request_id: uuid(v.request_id),
          source_result_id,
          source_event_index,
          confirmed: confirm(v.confirmed),
          ...duplicate(v),
        };
      });
      return identity(
        await request(path(sid), token, signal, 'POST', body),
        sid,
      );
    },
    async update(
      sid: string,
      eid: string,
      v: BillingUpdate,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => ({
        ...parseBillingValues(v),
        expected_version: integer(v.expected_version, 1),
        confirmed: confirm(v.confirmed),
        ...duplicate(v),
      }));
      return identity(
        await request(path(sid, eid), token, signal, 'PUT', body),
        sid,
        eid,
      );
    },
    async attach(
      sid: string,
      eid: string,
      v: BillingSourceInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return identity(
        await request(
          `${path(sid, eid)}/sources`,
          token,
          signal,
          'POST',
          sourceInput(v),
        ),
        sid,
        eid,
      );
    },
    async detach(
      sid: string,
      eid: string,
      v: BillingSourceInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return identity(
        await request(
          `${path(sid, eid)}/sources`,
          token,
          signal,
          'DELETE',
          sourceInput(v),
        ),
        sid,
        eid,
      );
    },
    async duplicates(
      sid: string,
      filter: DuplicateFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<DuplicatePage> {
      const query = input(
        () =>
          new URLSearchParams({
            kind: choice(filter.kind, ['invoice', 'payment', 'refund']),
            occurred_on: date(filter.occurred_on),
            amount: exactAmount(filter.amount),
            currency: billingCurrency(filter.currency),
            ...(filter.exclude_id !== null
              ? { exclude_id: uuid(filter.exclude_id) }
              : {}),
            limit: String(integer(filter.limit, 1, 100)),
            offset: String(integer(filter.offset)),
          }),
      );
      const body = record(
          await request(
            `/v1/subscriptions/${input(() => uuid(sid))}/billing-duplicates?${query}`,
            token,
            signal,
          ),
        ),
        result = page(body, sid, filter.limit, filter.offset);
      for (const e of result.events)
        if (
          e.state !== 'active' ||
          e.id === filter.exclude_id ||
          e.kind !== filter.kind ||
          e.occurred_on !== filter.occurred_on ||
          e.currency !== filter.currency ||
          decimalKey(e.amount) !== decimalKey(filter.amount)
        )
          return invalid();
      return { ...result, token: fingerprint(body.token) };
    },
    async summary(
      month: string,
      sid: string | null,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<BillingSummary> {
      const query = input(
          () =>
            new URLSearchParams({
              month: billingMonth(month),
              ...(sid !== null ? { subscription_id: uuid(sid) } : {}),
            }),
        ),
        result = parseBillingSummary(
          await request(`/v1/billing-summary?${query}`, token, signal),
        );
      return result.month === month && result.subscription_id === sid
        ? result
        : invalid();
    },
  };
}
export type BillingAPI = ReturnType<typeof createBillingAPI>;
