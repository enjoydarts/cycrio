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
  input,
  integer,
  invalid,
  nullable,
  record,
  text,
  timestamp,
  uuid,
} from './daily-api.ts';
import { createFeed, type FeedView } from './daily-controller.ts';

export type ReportContract = SubscriptionName & {
  id: string;
  service: string;
  plan: string | null;
  status: 'active' | 'ended' | 'unknown';
  contract_kind: 'recurring' | 'fixed_term' | 'unknown';
  access_end_date?: string | null;
  trial_end_date?: string | null;
  auto_renew?: boolean | null;
  billing_date_suppressed?: boolean;
  amount: string | null;
  currency: string | null;
  interval_unit: 'day' | 'week' | 'month' | 'year' | null;
  interval_count: number | null;
  next_billing_date: string | null;
  next_billing_mode?: 'automatic' | 'manual';
  monthly_amount: string | null;
  annual_amount: string | null;
  exclusion_reason:
    | ''
    | 'ended'
    | 'fixed_term'
    | 'access_ended'
    | 'missing_amount'
    | 'missing_currency'
    | 'missing_interval';
};
export type ReportCurrency = {
  currency: string;
  monthly_amount: string | null;
  annual_amount: string | null;
  contract_count: number;
  next_30_days_amount: string | null;
  next_30_days_count: number;
};
export type ActualCurrency = {
  currency: string;
  payment_amount: string | null;
  refund_amount: string | null;
  net_amount: string | null;
  invoice_amount: string | null;
  payment_count: number;
  refund_count: number;
  invoice_count: number;
};
export type MonthlyService = ActualCurrency &
  SubscriptionName & {
    subscription_id: string;
    service: string;
    plan: string | null;
  };
export type ReportDue = SubscriptionName & {
  next_billing_mode?: 'automatic' | 'manual';
  subscription_id: string;
  service: string;
  plan: string | null;
  date: string;
  amount: string | null;
  currency: string | null;
  annual_renewal: boolean;
  status: 'active' | 'unknown';
};
export type ReportCoverage = {
  total_contracts: number;
  open_contracts: number;
  active_contracts: number;
  unknown_contracts: number;
  ended_contracts: number;
  fixed_term_contracts: number;
  normalized_contracts: number;
  unpriced_contracts: number;
  missing_date_contracts: number;
  overdue_contracts: number;
  actual_record_count: number;
};
export type Report = {
  as_of: string;
  generated_at: string;
  timezone: 'Asia/Tokyo';
  month: string;
  history_start: string;
  history_end: string;
  contracts: ReportContract[];
  currencies: ReportCurrency[];
  history: { month: string; currencies: ActualCurrency[] }[];
  monthly_services: MonthlyService[];
  upcoming: ReportDue[];
  coverage: ReportCoverage;
};
export const reportMonth = (v: unknown): string =>
  typeof v === 'string' && /^(20\d{2}|21\d{2})-(0[1-9]|1[0-2])$/.test(v)
    ? v
    : invalid();
const historyMonth = (v: unknown): string =>
  typeof v === 'string' && /^(?!0000)\d{4}-(0[1-9]|1[0-2])$/.test(v)
    ? v
    : invalid();
const decimal = (v: unknown): string =>
  typeof v === 'string' && /^-?\d{1,64}(\.\d{1,12})?$/.test(v) ? v : invalid();
const unsigned = (v: unknown): string => {
  const x = decimal(v);
  return x.startsWith('-') ? invalid() : x;
};
const currency = (v: unknown): string =>
  typeof v === 'string' && /^[A-Z]{3}$/.test(v) ? v : invalid();
const label = (v: unknown) => text(v, 200);
const names = (v: Record<string, unknown>) => ({
  ...parseDisplayName(v.display_name),
  service: label(v.service),
  plan: nullable(v.plan, label),
});
function actual(value: unknown): ActualCurrency {
  const v = record(value);
  return {
    currency: currency(v.currency),
    payment_amount: nullable(v.payment_amount, unsigned),
    refund_amount: nullable(v.refund_amount, unsigned),
    net_amount: nullable(v.net_amount, decimal),
    invoice_amount: nullable(v.invoice_amount, unsigned),
    payment_count: integer(v.payment_count),
    refund_count: integer(v.refund_count),
    invoice_count: integer(v.invoice_count),
  };
}
const unique = <T>(rows: T[], key: (v: T) => string): T[] =>
  new Set(rows.map(key)).size === rows.length ? rows : invalid();
export function parseReport(value: unknown): Report {
  const v = record(value),
    month = reportMonth(v.month),
    as_of = date(v.as_of),
    history_start = date(v.history_start),
    history_end = date(v.history_end);
  const end = new Date(`${month}-01T00:00:00Z`);
  end.setUTCMonth(end.getUTCMonth() + 1);
  const start = new Date(end);
  start.setUTCMonth(start.getUTCMonth() - 12);
  if (
    history_start !== start.toISOString().slice(0, 10) ||
    history_end !== end.toISOString().slice(0, 10)
  )
    return invalid();
  const contracts = unique(
    array(
      v.contracts,
      (x) => {
        const row = record(x);
        const result: ReportContract = {
          id: uuid(row.id),
          ...names(row),
          status: choice(row.status, ['active', 'ended', 'unknown']),
          contract_kind: choice(row.contract_kind, [
            'recurring',
            'fixed_term',
            'unknown',
          ]),
          ...(row.access_end_date === undefined
            ? {}
            : { access_end_date: nullable(row.access_end_date, date) }),
          ...(row.trial_end_date === undefined
            ? {}
            : { trial_end_date: nullable(row.trial_end_date, date) }),
          ...(row.auto_renew === undefined
            ? {}
            : {
                auto_renew: nullable(row.auto_renew, (x) =>
                  typeof x === 'boolean' ? x : invalid(),
                ),
              }),
          amount: nullable(row.amount, unsigned),
          currency: nullable(row.currency, currency),
          interval_unit: nullable(row.interval_unit, (x) =>
            choice(x, ['day', 'week', 'month', 'year']),
          ),
          interval_count: nullable(row.interval_count, (x) =>
            integer(x, 1, 10000),
          ),
          next_billing_date: nullable(row.next_billing_date, date),
          billing_date_suppressed:
            row.billing_date_suppressed === undefined
              ? false
              : typeof row.billing_date_suppressed === 'boolean'
                ? row.billing_date_suppressed
                : invalid(),
          ...(row.next_billing_mode === undefined
            ? {}
            : {
                next_billing_mode: choice(row.next_billing_mode, [
                  'automatic',
                  'manual',
                ] as const),
              }),
          monthly_amount: nullable(row.monthly_amount, unsigned),
          annual_amount: nullable(row.annual_amount, unsigned),
          exclusion_reason: choice(row.exclusion_reason, [
            '',
            'ended',
            'fixed_term',
            'access_ended',
            'missing_amount',
            'missing_currency',
            'missing_interval',
          ]),
        };
        if (
          (result.monthly_amount === null) !==
            (result.annual_amount === null) ||
          (result.exclusion_reason === '') !==
            (result.monthly_amount !== null) ||
          (result.monthly_amount !== null &&
            (!result.currency ||
              result.status === 'ended' ||
              result.contract_kind === 'fixed_term'))
        )
          return invalid();
        if (
          (result.exclusion_reason === 'access_ended' &&
            (!result.access_end_date || result.access_end_date >= as_of)) ||
          (result.monthly_amount !== null &&
            result.access_end_date &&
            result.access_end_date < as_of)
        )
          return invalid();
        return result;
      },
      100000,
    ),
    (x) => x.id,
  );
  const currencies = unique(
    array(
      v.currencies,
      (x) => {
        const row = record(x);
        return {
          currency: currency(row.currency),
          monthly_amount: nullable(row.monthly_amount, unsigned),
          annual_amount: nullable(row.annual_amount, unsigned),
          contract_count: integer(row.contract_count),
          next_30_days_amount: nullable(row.next_30_days_amount, unsigned),
          next_30_days_count: integer(row.next_30_days_count),
        };
      },
      1000,
    ),
    (x) => x.currency,
  );
  const history = array(
    v.history,
    (x) => {
      const row = record(x);
      return {
        month: historyMonth(row.month),
        currencies: unique(
          array(row.currencies, actual, 1000),
          (x) => x.currency,
        ),
      };
    },
    12,
  );
  if (
    history.length !== 12 ||
    history.some((row, i) => {
      const expected = new Date(start);
      expected.setUTCMonth(expected.getUTCMonth() + i);
      return row.month !== expected.toISOString().slice(0, 7);
    })
  )
    return invalid();
  const monthly_services = unique(
    array(
      v.monthly_services,
      (x) => {
        const row = record(x);
        return {
          ...actual(row),
          subscription_id: uuid(row.subscription_id),
          ...names(row),
        };
      },
      100000,
    ),
    (x) => `${x.subscription_id}/${x.currency}`,
  );
  const limit = new Date(`${as_of}T00:00:00Z`);
  limit.setUTCDate(limit.getUTCDate() + 365);
  const upcoming = unique(
    array(
      v.upcoming,
      (x) => {
        const row = record(x),
          due = date(row.date);
        if (
          typeof row.annual_renewal !== 'boolean' ||
          due < as_of ||
          due >= limit.toISOString().slice(0, 10)
        )
          return invalid();
        return {
          subscription_id: uuid(row.subscription_id),
          ...names(row),
          date: due,
          ...(row.next_billing_mode === undefined
            ? {}
            : {
                next_billing_mode: choice(row.next_billing_mode, [
                  'automatic',
                  'manual',
                ] as const),
              }),
          amount: nullable(row.amount, unsigned),
          currency: nullable(row.currency, currency),
          annual_renewal: row.annual_renewal,
          status: choice(row.status, ['active', 'unknown'] as const),
        };
      },
      100000,
    ),
    (x) => x.subscription_id,
  );
  const c = record(v.coverage),
    coverage: ReportCoverage = {
      total_contracts: integer(c.total_contracts),
      open_contracts: integer(c.open_contracts),
      active_contracts: integer(c.active_contracts),
      unknown_contracts: integer(c.unknown_contracts),
      ended_contracts: integer(c.ended_contracts),
      fixed_term_contracts: integer(c.fixed_term_contracts),
      normalized_contracts: integer(c.normalized_contracts),
      unpriced_contracts: integer(c.unpriced_contracts),
      missing_date_contracts: integer(c.missing_date_contracts),
      overdue_contracts: integer(c.overdue_contracts),
      actual_record_count: integer(c.actual_record_count),
    };
  if (
    coverage.total_contracts !== contracts.length ||
    coverage.open_contracts !==
      coverage.active_contracts + coverage.unknown_contracts ||
    coverage.total_contracts !==
      coverage.open_contracts + coverage.ended_contracts
  )
    return invalid();
  return {
    as_of,
    generated_at: timestamp(v.generated_at),
    timezone: choice(v.timezone, ['Asia/Tokyo']),
    month,
    history_start,
    history_end,
    contracts,
    currencies,
    history,
    monthly_services,
    upcoming,
    coverage,
  };
}
export function createReportsAPI(base: string, fetcher: typeof fetch = fetch) {
  const request = createDailyRequest(base, fetcher);
  return {
    async read(
      month: string,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<Report> {
      const selected = input(() => reportMonth(month));
      const result = parseReport(
        await request(`/v1/reports?month=${selected}`, token, signal),
      );
      if (result.month !== selected) return invalid();
      return result;
    },
  };
}
export function createReportsController(
  api: Pick<ReturnType<typeof createReportsAPI>, 'read'>,
  token: TokenProvider,
  changed: (v: FeedView<Report>) => void,
  onDenied: () => void,
) {
  return createFeed<Report, string>(
    (month, token, signal) => api.read(month, token, signal),
    token,
    changed,
    onDenied,
  );
}
