import type { TokenProvider } from './api.ts';
import {
  parseProcessingEvent,
  type ProcessingEvent,
} from './mail-processing-api.ts';
import {
  array,
  choice,
  createDailyRequest,
  date,
  input,
  integer,
  invalid,
  nullable,
  pagination,
  record,
  text,
  timestamp,
  uuid,
} from './daily-api.ts';
export type SubscriptionValues = {
  service: string;
  plan: string | null;
  amount: string | null;
  currency: string | null;
  interval_unit: 'day' | 'week' | 'month' | 'year' | null;
  interval_count: number | null;
  next_billing_date: string | null;
  status: 'active' | 'ended' | 'unknown';
};
export type Subscription = SubscriptionValues & {
  provenance?: 'automatic' | 'owner_confirmed';
  id: string;
  version: number;
  created_at: string;
  updated_at: string;
  sources: {
    provenance?: 'automatic' | 'owner_confirmed';
    result_id: string;
    event_index: number;
    original_event: ProcessingEvent;
  }[];
};
export type SubscriptionFilter = {
  q: string;
  status: 'all' | 'active' | 'ended' | 'unknown';
  sort: 'next_billing' | 'service' | 'updated';
  limit: number;
  offset: number;
};
export type SubscriptionPage = {
  subscriptions: Subscription[];
  total: number;
  limit: number;
  offset: number;
};
export type SubscriptionCreate = SubscriptionValues & {
  request_id: string;
  source_result_id: string | null;
  source_event_index: number | null;
  confirmed: boolean;
};
export type SubscriptionUpdate = SubscriptionValues & {
  expected_version: number;
  confirmed: boolean;
};
export type SourceInput = {
  expected_version: number;
  source_result_id: string;
  source_event_index: number;
  confirmed: boolean;
};
export function parseValues(value: unknown): SubscriptionValues {
  const v = record(value);
  const label = (x: unknown) =>
    typeof x === 'string' &&
    x.trim().length > 0 &&
    [...x.trim()].length <= 200 &&
    !/[\u0000\p{Cs}]/u.test(x)
      ? x.trim()
      : invalid();
  const amount = nullable(v.amount, (x) =>
    typeof x === 'string' && /^\d{1,24}(\.\d{1,12})?$/.test(x) ? x : invalid(),
  );
  const currency = nullable(v.currency, (x) =>
    typeof x === 'string' && /^[A-Z]{3}$/.test(x) ? x : invalid(),
  );
  const interval_unit = nullable(v.interval_unit, (x) =>
    choice(x, ['day', 'week', 'month', 'year'] as const),
  );
  const interval_count = nullable(v.interval_count, (x) =>
    integer(x, 1, 10000),
  );
  if (
    (amount === null) !== (currency === null) ||
    (interval_unit === null) !== (interval_count === null)
  )
    return invalid();
  return {
    service: label(v.service),
    plan: nullable(v.plan, label),
    amount,
    currency,
    interval_unit,
    interval_count,
    next_billing_date: nullable(v.next_billing_date, date),
    status: choice(v.status, ['active', 'ended', 'unknown']),
  };
}
export function parseSubscription(value: unknown): Subscription {
  const v = record(value);
  const sources = array(
    v.sources,
    (x) => {
      const s = record(x);
      return {
        provenance:
          s.provenance === undefined
            ? ('owner_confirmed' as const)
            : choice(s.provenance, ['automatic', 'owner_confirmed']),
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
    ...parseValues(v),
    provenance:
      v.provenance === undefined
        ? 'owner_confirmed'
        : choice(v.provenance, ['automatic', 'owner_confirmed'] as const),
    id: uuid(v.id),
    version: integer(v.version, 1),
    created_at: timestamp(v.created_at),
    updated_at: timestamp(v.updated_at),
    sources,
  };
}
export function createSubscriptionsAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  const request = createDailyRequest(base, fetcher),
    path = (id: string) => `/v1/subscriptions/${input(() => uuid(id))}`;
  const confirm = (v: boolean) => (v === true ? true : invalid());
  return {
    async list(
      filter: SubscriptionFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<SubscriptionPage> {
      const query = input(
        () =>
          new URLSearchParams({
            q: text(filter.q, 200, true),
            status: choice(filter.status, [
              'all',
              'active',
              'ended',
              'unknown',
            ]),
            sort: choice(filter.sort, ['next_billing', 'service', 'updated']),
            limit: String(integer(filter.limit, 1, 100)),
            offset: String(integer(filter.offset)),
          }),
      );
      const v = record(
        await request(`/v1/subscriptions?${query}`, token, signal),
      );
      return {
        subscriptions: array(v.subscriptions, parseSubscription),
        ...pagination(v),
      };
    },
    async get(id: string, token: TokenProvider, signal?: AbortSignal) {
      const result = parseSubscription(await request(path(id), token, signal));
      return result.id === id ? result : invalid();
    },
    async create(
      v: SubscriptionCreate,
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
          ...parseValues(v),
          request_id: uuid(v.request_id),
          source_result_id,
          source_event_index,
          confirmed: confirm(v.confirmed),
        };
      });
      return parseSubscription(
        await request('/v1/subscriptions', token, signal, 'POST', body),
      );
    },
    async update(
      id: string,
      v: SubscriptionUpdate,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => ({
        ...parseValues(v),
        expected_version: integer(v.expected_version, 1),
        confirmed: confirm(v.confirmed),
      }));
      const result = parseSubscription(
        await request(path(id), token, signal, 'PUT', body),
      );
      return result.id === id ? result : invalid();
    },
    async attach(
      id: string,
      v: SourceInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => ({
        expected_version: integer(v.expected_version, 1),
        source_result_id: uuid(v.source_result_id),
        source_event_index: integer(v.source_event_index, 0, 31),
        confirmed: confirm(v.confirmed),
      }));
      const result = parseSubscription(
        await request(`${path(id)}/sources`, token, signal, 'POST', body),
      );
      return result.id === id ? result : invalid();
    },
  };
}
export type SubscriptionsAPI = ReturnType<typeof createSubscriptionsAPI>;
