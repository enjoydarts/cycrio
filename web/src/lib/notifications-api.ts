import type { TokenProvider } from './api.ts';
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
export const notificationKinds = [
  'renewal',
  'annual_charge',
  'trial_end',
  'price_increase',
  'import_error',
] as const;
export type NotificationKind = (typeof notificationKinds)[number];
export type NotificationChannels = { in_app: boolean; email: boolean };
export type NotificationPreferences = {
  version: number;
  kinds: Record<NotificationKind, NotificationChannels>;
  lead_days: number;
  send_hour: number;
  timezone: 'Asia/Tokyo';
  email_opt_in: boolean;
};
export type NotificationSettings = {
  preferences: NotificationPreferences;
  email: {
    available: boolean;
    reason: string;
    profile_ready: boolean;
    sender_ready: boolean;
  };
  as_of: string;
  timezone: 'Asia/Tokyo';
};
export type NotificationSettingsInput = Omit<
  NotificationPreferences,
  'version'
> & { expected_version: number; confirmed: boolean };
export type NotificationFacts = {
  kind: NotificationKind;
  source_key: string;
  contract_id?: string;
  account_id?: string;
  result_id?: string;
  event_index?: number;
  episode_id?: string;
  service: string;
  due_date?: string;
  scheduled_at: string;
  date_source?: 'manual' | 'receipt_estimate' | 'explicit_trial_end';
  amount?: string;
  currency?: string;
  old_amount?: string;
  new_amount?: string;
  in_app: boolean;
  email: boolean;
};
export type Notification = {
  id: string;
  facts: NotificationFacts;
  match_basis?: 'unique_confirmed_account_service';
  created_at: string;
  read_at: string | null;
  superseded_at: string | null;
};
export type NotificationPage = {
  notifications: Notification[];
  total: number;
  unread_count: number;
  limit: number;
  offset: number;
  as_of: string;
  timezone: 'Asia/Tokyo';
};
export type NotificationFilter = {
  read: 'all' | 'unread' | 'read';
  limit: number;
  offset: number;
};
export type NotificationDelivery = {
  id: string;
  channel: 'in_app' | 'email';
  state:
    'queued' | 'claimed' | 'unknown' | 'accepted' | 'failed' | 'suppressed';
  reason: string;
  scheduled_at: string;
  created_at: string;
  updated_at: string;
  sent: boolean;
  delivered: boolean;
  bounced: boolean;
  complained: boolean;
  failed: boolean;
};
const bool = (v: unknown): boolean => (typeof v === 'boolean' ? v : invalid());
const amount = (v: unknown) =>
  typeof v === 'string' && /^\d{1,24}(\.\d{1,12})?$/.test(v) ? v : invalid();
const currency = (v: unknown) =>
  typeof v === 'string' && /^[A-Z]{3}$/.test(v) ? v : invalid();
const optional = <K extends string, V>(
  v: Record<string, unknown>,
  key: K,
  parse: (value: unknown) => V,
): Partial<Record<K, V>> =>
  v[key] === undefined ? {} : ({ [key]: parse(v[key]) } as Record<K, V>);
export function parseNotificationPreferences(
  value: unknown,
): NotificationPreferences {
  const v = record(value),
    raw = record(v.kinds);
  if (Object.keys(raw).length !== notificationKinds.length) return invalid();
  const kinds = Object.fromEntries(
    notificationKinds.map((kind) => {
      const channel = record(raw[kind]);
      if (Object.keys(channel).length !== 2) return invalid();
      return [
        kind,
        { in_app: bool(channel.in_app), email: bool(channel.email) },
      ];
    }),
  ) as NotificationPreferences['kinds'];
  const email_opt_in = bool(v.email_opt_in);
  if (!email_opt_in && Object.values(kinds).some((channel) => channel.email))
    return invalid();
  return {
    version: integer(v.version, 1),
    kinds,
    lead_days: integer(v.lead_days, 0, 30),
    send_hour: integer(v.send_hour, 0, 23),
    timezone: choice(v.timezone, ['Asia/Tokyo']),
    email_opt_in,
  };
}
export function parseNotificationSettings(
  value: unknown,
): NotificationSettings {
  const v = record(value),
    email = record(v.email);
  return {
    preferences: parseNotificationPreferences(v.preferences),
    email: {
      available: bool(email.available),
      reason: text(email.reason, 100, true),
      profile_ready: bool(email.profile_ready),
      sender_ready: bool(email.sender_ready),
    },
    as_of: date(v.as_of),
    timezone: choice(v.timezone, ['Asia/Tokyo']),
  };
}
export const notificationPreferencesEqual = (
  a: NotificationPreferences,
  b: NotificationPreferences,
) =>
  a.lead_days === b.lead_days &&
  a.send_hour === b.send_hour &&
  a.timezone === b.timezone &&
  a.email_opt_in === b.email_opt_in &&
  notificationKinds.every(
    (kind) =>
      a.kinds[kind].in_app === b.kinds[kind].in_app &&
      a.kinds[kind].email === b.kinds[kind].email,
  );
export function parseNotification(value: unknown): Notification {
  const v = record(value),
    f = record(v.facts),
    kind = choice(f.kind, notificationKinds);
  const facts: NotificationFacts = {
    kind,
    source_key: text(f.source_key, 300),
    service: text(f.service, 200, true),
    scheduled_at: timestamp(f.scheduled_at),
    in_app: bool(f.in_app),
    email: bool(f.email),
    ...optional(f, 'contract_id', uuid),
    ...optional(f, 'account_id', uuid),
    ...optional(f, 'result_id', uuid),
    ...optional(f, 'episode_id', uuid),
    ...optional(f, 'event_index', (x) => integer(x, 0, 31)),
    ...optional(f, 'due_date', date),
    ...optional(f, 'date_source', (x) =>
      choice(x, ['manual', 'receipt_estimate', 'explicit_trial_end'] as const),
    ),
    ...optional(f, 'amount', amount),
    ...optional(f, 'currency', currency),
    ...optional(f, 'old_amount', amount),
    ...optional(f, 'new_amount', amount),
  };
  if (
    kind === 'import_error'
      ? !facts.account_id || !facts.episode_id
      : !facts.contract_id || !facts.service.trim()
  )
    return invalid();
  if (
    kind === 'price_increase' &&
    (!facts.result_id ||
      facts.event_index === undefined ||
      !facts.old_amount ||
      !facts.new_amount ||
      !facts.currency)
  )
    return invalid();
  if (
    ['renewal', 'annual_charge'].includes(kind) &&
    (!facts.due_date ||
      !['manual', 'receipt_estimate'].includes(facts.date_source ?? ''))
  )
    return invalid();
  if (
    kind === 'trial_end' &&
    (!facts.due_date || facts.date_source !== 'explicit_trial_end')
  )
    return invalid();
  return {
    id: uuid(v.id),
    facts,
    ...optional(v, 'match_basis', (x) =>
      choice(x, ['unique_confirmed_account_service'] as const),
    ),
    created_at: timestamp(v.created_at),
    read_at: nullable(v.read_at, timestamp),
    superseded_at: nullable(v.superseded_at, timestamp),
  };
}
export function parseNotificationPage(value: unknown): NotificationPage {
  const v = record(value),
    notifications = array(v.notifications, parseNotification);
  if (new Set(notifications.map((n) => n.id)).size !== notifications.length)
    return invalid();
  return {
    notifications,
    ...pagination(v),
    unread_count: integer(v.unread_count),
    as_of: date(v.as_of),
    timezone: choice(v.timezone, ['Asia/Tokyo']),
  };
}
export function createNotificationsAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  const request = createDailyRequest(base, fetcher);
  const path = (id: string) => `/v1/notifications/${input(() => uuid(id))}`;
  return {
    async settings(token: TokenProvider, signal?: AbortSignal) {
      return parseNotificationSettings(
        await request('/v1/notification-settings', token, signal),
      );
    },
    async updateSettings(
      value: NotificationSettingsInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const p = input(() =>
        parseNotificationPreferences({
          ...value,
          version: value.expected_version,
        }),
      );
      const body = input(() => {
        if (value.confirmed !== true) return invalid();
        return {
          expected_version: p.version,
          kinds: p.kinds,
          lead_days: p.lead_days,
          send_hour: p.send_hour,
          timezone: p.timezone,
          email_opt_in: p.email_opt_in,
          confirmed: true,
        };
      });
      const result = parseNotificationSettings(
        await request('/v1/notification-settings', token, signal, 'PUT', body),
      );
      return result.preferences.version === p.version + 1 &&
        notificationPreferencesEqual(result.preferences, p)
        ? result
        : invalid();
    },
    async list(
      filter: NotificationFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const query = input(
        () =>
          new URLSearchParams({
            limit: String(integer(filter.limit, 1, 100)),
            offset: String(integer(filter.offset)),
            read: choice(filter.read, ['all', 'unread', 'read']),
          }),
      );
      return parseNotificationPage(
        await request(`/v1/notifications?${query}`, token, signal),
      );
    },
    async read(id: string, token: TokenProvider, signal?: AbortSignal) {
      const result = parseNotification(
        await request(path(id) + '/read', token, signal, 'POST', {
          confirmed: true,
        }),
      );
      return result.id === id && result.read_at !== null ? result : invalid();
    },
    async deliveries(
      id: string,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<NotificationDelivery[]> {
      const page = record(
        await request(path(id) + '/deliveries', token, signal),
      );
      return array(page.deliveries, (value) => {
        const v = record(value);
        return {
          id: uuid(v.id),
          channel: choice(v.channel, ['in_app', 'email']),
          state: choice(v.state, [
            'queued',
            'claimed',
            'unknown',
            'accepted',
            'failed',
            'suppressed',
          ]),
          reason: text(v.reason, 100, true),
          scheduled_at: timestamp(v.scheduled_at),
          created_at: timestamp(v.created_at),
          updated_at: timestamp(v.updated_at),
          sent: bool(v.sent),
          delivered: bool(v.delivered),
          bounced: bool(v.bounced),
          complained: bool(v.complained),
          failed: bool(v.failed),
        };
      });
    },
  };
}
export type NotificationsAPI = ReturnType<typeof createNotificationsAPI>;
