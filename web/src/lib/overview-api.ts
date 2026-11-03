import type { TokenProvider } from './api.ts';
import {
  parseDisplayName,
  type SubscriptionName,
} from './subscription-title.ts';
import {
  array,
  choice,
  createDailyRequest,
  date,
  integer,
  invalid,
  nullable,
  record,
  text,
  timestamp,
  uuid,
} from './daily-api.ts';
import { parseValues, type SubscriptionValues } from './subscriptions-api.ts';
import { createFeed, type FeedView } from './daily-controller.ts';

export type OverviewCounts = {
  total: number;
  active: number;
  ended: number;
  unknown: number;
  upcoming: number;
  overdue: number;
  undated: number;
  later: number;
};
export type UpcomingSubscription = SubscriptionValues &
  SubscriptionName & {
    id: string;
    provenance: 'automatic' | 'owner_confirmed';
  };
export type OverviewConnection = {
  id: string;
  label: string;
  state: 'unverified' | 'ready' | 'needs_reauth' | 'disconnected';
  automation_state:
    'not_configured' | 'enabled' | 'stopped' | 'needs_confirmation';
  ready: boolean;
  blocked_reasons: string[];
  last_success_at: string | null;
  next_check_at: string | null;
  last_started_at?: string | null;
  automation_reason?: string;
  automation_running?: boolean;
  retry_at?: string | null;
};
export type OverviewReport = {
  as_of: string;
  window_end: string;
  timezone: 'Asia/Tokyo';
  generated_at: string;
  subscriptions: OverviewCounts;
  attention_messages: number;
  upcoming: UpcomingSubscription[];
  connections: OverviewConnection[];
};
export function parseOverview(value: unknown): OverviewReport {
  const v = record(value),
    as_of = date(v.as_of),
    window_end = date(v.window_end),
    c = record(v.subscriptions);
  const end = new Date(`${as_of}T00:00:00Z`);
  end.setUTCDate(end.getUTCDate() + 30);
  if (end.toISOString().slice(0, 10) !== window_end) return invalid();
  const subscriptions = {
    total: integer(c.total),
    active: integer(c.active),
    ended: integer(c.ended),
    unknown: integer(c.unknown),
    upcoming: integer(c.upcoming),
    overdue: integer(c.overdue),
    undated: integer(c.undated),
    later: integer(c.later),
  };
  if (
    subscriptions.total !==
      subscriptions.active + subscriptions.ended + subscriptions.unknown ||
    subscriptions.active + subscriptions.unknown !==
      subscriptions.upcoming +
        subscriptions.overdue +
        subscriptions.undated +
        subscriptions.later
  )
    return invalid();
  const upcoming = array(
    v.upcoming,
    (item): UpcomingSubscription => {
      const row = record(item),
        values = parseValues(row);
      if (
        !values.next_billing_date ||
        values.status === 'ended' ||
        values.next_billing_date < as_of ||
        values.next_billing_date >= window_end
      )
        return invalid();
      return {
        ...values,
        ...parseDisplayName(row.display_name),
        id: uuid(row.id),
        provenance: choice(row.provenance, ['automatic', 'owner_confirmed']),
      };
    },
    10,
  );
  if (
    upcoming.length !== Math.min(10, subscriptions.upcoming) ||
    new Set(upcoming.map((x) => x.id)).size !== upcoming.length ||
    upcoming.some(
      (row, i) =>
        i > 0 && row.next_billing_date! < upcoming[i - 1].next_billing_date!,
    )
  )
    return invalid();
  const connections = array(
    v.connections,
    (item): OverviewConnection => {
      const row = record(item);
      if (typeof row.ready !== 'boolean') return invalid();
      return {
        id: uuid(row.id),
        label: text(row.label, 200),
        state: choice(row.state, [
          'unverified',
          'ready',
          'needs_reauth',
          'disconnected',
        ]),
        automation_state: choice(row.automation_state, [
          'not_configured',
          'enabled',
          'stopped',
          'needs_confirmation',
        ]),
        ready: row.ready,
        blocked_reasons: array(row.blocked_reasons, (x) => text(x, 200)),
        last_success_at: nullable(row.last_success_at, timestamp),
        next_check_at: nullable(row.next_check_at, timestamp),
        ...(row.last_started_at === undefined
          ? {}
          : { last_started_at: nullable(row.last_started_at, timestamp) }),
        ...(row.automation_reason === undefined
          ? {}
          : { automation_reason: text(row.automation_reason, 200, true) }),
        ...(row.automation_running === undefined
          ? {}
          : {
              automation_running:
                typeof row.automation_running === 'boolean'
                  ? row.automation_running
                  : invalid(),
            }),
        ...(row.retry_at === undefined
          ? {}
          : { retry_at: nullable(row.retry_at, timestamp) }),
      };
    },
    10,
  );
  if (new Set(connections.map((x) => x.id)).size !== connections.length)
    return invalid();
  return {
    as_of,
    window_end,
    timezone: choice(v.timezone, ['Asia/Tokyo']),
    generated_at: timestamp(v.generated_at),
    subscriptions,
    attention_messages: integer(v.attention_messages),
    upcoming,
    connections,
  };
}
export function createOverviewAPI(base: string, fetcher: typeof fetch = fetch) {
  const request = createDailyRequest(base, fetcher);
  return {
    async read(token: TokenProvider, signal?: AbortSignal) {
      return parseOverview(await request('/v1/overview', token, signal));
    },
  };
}
export function createOverviewController(
  api: Pick<ReturnType<typeof createOverviewAPI>, 'read'>,
  token: TokenProvider,
  changed: (v: FeedView<OverviewReport>) => void,
  onDenied: () => void,
) {
  return createFeed<OverviewReport, void>(
    (_filter, token, signal) => api.read(token, signal),
    token,
    changed,
    onDenied,
  );
}
