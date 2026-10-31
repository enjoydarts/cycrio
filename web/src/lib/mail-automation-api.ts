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
  record,
  text,
  timestamp,
  uuid,
} from './daily-api.ts';
const bool = (v: unknown): boolean => (typeof v === 'boolean' ? v : invalid());
export type AutomationScope = {
  since: string;
  before: string;
  timezone: 'Asia/Tokyo';
  full_email: boolean;
  interval_seconds: number;
  auto_register: boolean;
};
export type AutomationBasis = {
  account_version: number;
  runtime_version: number;
  profile_id: string;
  policy_version: number;
};
export type AutomationInput = {
  expected_settings_version: number;
  expected_account_version: number;
  runtime_version: number;
  profile_id: string;
  policy_version: number;
  since: string;
  before: string;
  full_email: true;
  interval_seconds: 300;
  confirmed: true;
  auto_register: false;
  request_id: string;
};
export type AutomationControl = {
  expected_settings_version: number;
  request_id: string;
};
export type MailAutomationState = {
  id: string;
  account_id: string;
  version: number;
  generation: number;
  state: 'not_configured' | 'enabled' | 'stopped' | 'needs_confirmation';
  reason: string;
  ready: boolean;
  preparation_ready: boolean;
  preparation_blocked_reasons: string[];
  blocked_reasons: string[];
  scope: AutomationScope | null;
  default_scope: AutomationScope;
  basis: AutomationBasis;
  progress: {
    bootstrap_complete: boolean;
    historical_complete: boolean;
    discovered_count: number;
    pending_count: number;
    completed_count: number;
    failed_count: number;
    unknown_count: number;
  };
  last_success_at: string | null;
  next_check_at: string | null;
  discovery_failure_count: number;
  retry_at: string | null;
  recent_sync_runs: {
    id: string;
    account_id: string;
    mode: 'manual' | 'bootstrap' | 'historical' | 'incremental';
    state: string;
    since: string;
    before: string;
    max_messages: number;
    examined_count: number;
    added_count: number;
    folder_count: number;
    completed_folders: number;
    error_code: string;
    created_at: string;
    started_at: string | null;
    finished_at: string | null;
  }[];
};
function scope(value: unknown): AutomationScope {
  const v = record(value),
    since = date(v.since),
    before = date(v.before);
  if (since >= before) return invalid();
  return {
    since,
    before,
    timezone: choice(v.timezone, ['Asia/Tokyo']),
    full_email: bool(v.full_email),
    interval_seconds: integer(v.interval_seconds, 60, 86400),
    auto_register: bool(v.auto_register),
  };
}
export function parseMailAutomationState(
  value: unknown,
  accountID?: string,
): MailAutomationState {
  const v = record(value),
    b = record(v.basis),
    p = record(v.progress),
    account = uuid(v.account_id);
  if (accountID !== undefined && account !== uuid(accountID)) return invalid();
  return {
    id: v.id === '' ? '' : uuid(v.id),
    account_id: account,
    version: integer(v.version),
    generation: integer(v.generation),
    state: choice(v.state, [
      'not_configured',
      'enabled',
      'stopped',
      'needs_confirmation',
    ]),
    reason: text(v.reason, 200, true),
    ready: bool(v.ready),
    preparation_ready: bool(v.preparation_ready),
    preparation_blocked_reasons: array(v.preparation_blocked_reasons, (x) =>
      text(x, 200),
    ),
    blocked_reasons: array(v.blocked_reasons, (x) => text(x, 200)),
    scope: nullable(v.scope, scope),
    default_scope: scope(v.default_scope),
    basis: {
      account_version: integer(b.account_version, 1),
      runtime_version: integer(b.runtime_version),
      profile_id: b.profile_id === '' ? '' : uuid(b.profile_id),
      policy_version: integer(b.policy_version),
    },
    progress: {
      bootstrap_complete: bool(p.bootstrap_complete),
      historical_complete: bool(p.historical_complete),
      discovered_count: integer(p.discovered_count),
      pending_count: integer(p.pending_count),
      completed_count: integer(p.completed_count),
      failed_count: integer(p.failed_count),
      unknown_count: integer(p.unknown_count),
    },
    last_success_at: nullable(v.last_success_at, timestamp),
    next_check_at: nullable(v.next_check_at, timestamp),
    retry_at: nullable(v.retry_at, timestamp),
    discovery_failure_count: integer(v.discovery_failure_count, 0, 6),
    recent_sync_runs: array(
      v.recent_sync_runs,
      (x) => {
        const r = record(x),
          mode = choice(r.mode, [
            'manual',
            'bootstrap',
            'historical',
            'incremental',
          ]);
        if (uuid(r.account_id) !== account) return invalid();
        const range = mode === 'manual' || mode === 'historical';
        return {
          id: uuid(r.id),
          account_id: account,
          mode,
          state: choice(r.state, [
            'queued',
            'leased',
            'running',
            'completed',
            'limited',
            'failed',
            'cancelled',
          ]),
          since: range ? date(r.since) : choice(r.since, ['']),
          before: range ? date(r.before) : choice(r.before, ['']),
          max_messages: integer(r.max_messages, 1),
          examined_count: integer(r.examined_count),
          added_count: integer(r.added_count),
          folder_count: integer(r.folder_count),
          completed_folders: integer(r.completed_folders),
          error_code: text(r.error_code, 200, true),
          created_at: timestamp(r.created_at),
          started_at: nullable(r.started_at, timestamp),
          finished_at: nullable(r.finished_at, timestamp),
        };
      },
      20,
    ),
  };
}
function shiftDate(value: string, days: number) {
  const d = new Date(`${date(value)}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + days);
  return date(d.toISOString().slice(0, 10));
}
export const inclusiveEnd = (before: string) => shiftDate(before, -1);
export const exclusiveEnd = (through: string) => shiftDate(through, 1);
export function validAutomationRange(since: string, through: string) {
  try {
    const before = exclusiveEnd(through);
    return (
      date(since) >= '1900-01-01' && before <= '9999-12-31' && since < before
    );
  } catch {
    return false;
  }
}
export function createMailAutomationAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  const request = createDailyRequest(base, fetcher),
    path = (id: string) =>
      `/v1/mail/accounts/${input(() => uuid(id))}/automation`;
  const control = (v: AutomationControl) =>
    input(() => ({
      expected_settings_version: integer(v.expected_settings_version),
      request_id: uuid(v.request_id),
    }));
  return {
    async read(id: string, token: TokenProvider, signal?: AbortSignal) {
      return parseMailAutomationState(
        await request(path(id), token, signal),
        id,
      );
    },
    async preferences(token: TokenProvider, signal?: AbortSignal) {
      const p = record(
        await request('/v1/subscriptions/automation', token, signal),
      );
      return { enabled: bool(p.enabled), version: integer(p.version) };
    },
    async savePreferences(
      v: { enabled: boolean; expected_version: number },
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => ({
        enabled: bool(v.enabled),
        expected_version: integer(v.expected_version),
      }));
      const p = record(
        await request(
          '/v1/subscriptions/automation',
          token,
          signal,
          'PUT',
          body,
        ),
      );
      return { enabled: bool(p.enabled), version: integer(p.version) };
    },
    async save(
      id: string,
      v: AutomationInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => {
        if (
          v.full_email !== true ||
          v.confirmed !== true ||
          v.auto_register !== false ||
          v.interval_seconds !== 300 ||
          !validAutomationRange(v.since, inclusiveEnd(v.before))
        )
          return invalid();
        return {
          ...control(v),
          expected_account_version: integer(v.expected_account_version, 1),
          runtime_version: integer(v.runtime_version, 1),
          profile_id: uuid(v.profile_id),
          policy_version: integer(v.policy_version, 1),
          since: date(v.since),
          before: date(v.before),
          full_email: true,
          interval_seconds: 300,
          confirmed: true,
          auto_register: false,
        };
      });
      return parseMailAutomationState(
        await request(path(id), token, signal, 'PUT', body),
        id,
      );
    },
    async stop(
      id: string,
      v: AutomationControl,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseMailAutomationState(
        await request(`${path(id)}/stop`, token, signal, 'POST', control(v)),
        id,
      );
    },
    async resume(
      id: string,
      v: AutomationControl,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseMailAutomationState(
        await request(`${path(id)}/resume`, token, signal, 'POST', control(v)),
        id,
      );
    },
  };
}
export type MailAutomationAPI = ReturnType<typeof createMailAutomationAPI>;
