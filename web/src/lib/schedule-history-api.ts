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
  uuid,
} from './daily-api.ts';
import { parseSubscription, type Subscription } from './subscriptions-api.ts';
import {
  parseScheduleRule,
  sameScheduleRule,
  type ScheduleRule,
} from './schedule-rule.ts';
export type HistoryFilter = { start_date: string; currency: string };
export type HistoryBasis = {
  filter: HistoryFilter;
  token: string;
  algorithm_version: 'history-exact-v1';
};
export type HistorySample = {
  id: string;
  version: number;
  occurred_on: string;
  amount: string;
  currency: string;
};
export type HistoryEvidence = HistoryBasis & {
  as_of: string;
  samples: HistorySample[];
  selected_rule: ScheduleRule;
};
export type HistoryEntry = HistorySample & {
  kind: 'payment' | 'invoice' | 'refund';
  state: 'active' | 'voided';
  reason:
    | 'eligible'
    | 'voided'
    | 'invoice'
    | 'refund'
    | 'other_currency'
    | 'refunded';
};
export type HistoryCandidate = {
  rule: ScheduleRule;
  candidate_date: string | null;
  reason: '' | 'ended' | 'out_of_range' | 'fixed_term';
};
export type HistoryCounts = Record<
  | 'total'
  | 'eligible'
  | 'sampled'
  | 'older_eligible'
  | 'voided'
  | 'invoices'
  | 'refunds'
  | 'other_currency'
  | 'refunded'
  | 'referenced_refunds',
  number
>;
export type ScheduleHistoryPreview = HistoryBasis & {
  subscription: Subscription;
  as_of: string;
  timezone: 'Asia/Tokyo';
  status:
    | 'ready'
    | 'ambiguous'
    | 'insufficient'
    | 'duplicate_dates'
    | 'no_match'
    | 'too_many';
  reason: '' | 'ended' | 'fixed_term';
  counts: HistoryCounts | null;
  samples: HistorySample[];
  candidates: HistoryCandidate[];
  entries: HistoryEntry[];
};
const currency = (v: unknown): string =>
  typeof v === 'string' && /^[A-Z]{3}$/.test(v) ? v : invalid();
export function parseHistoryFilter(value: unknown): HistoryFilter {
  const v = record(value);
  return { start_date: date(v.start_date), currency: currency(v.currency) };
}
export function parseHistoryBasis(value: unknown): HistoryBasis {
  const v = record(value);
  return {
    filter: parseHistoryFilter(v.filter),
    token:
      typeof v.token === 'string' && /^[0-9a-f]{64}$/.test(v.token)
        ? v.token
        : invalid(),
    algorithm_version: choice(v.algorithm_version, [
      'history-exact-v1',
    ] as const),
  };
}
function sample(value: unknown): HistorySample {
  const v = record(value);
  return {
    id: uuid(v.id),
    version: integer(v.version, 1),
    occurred_on: date(v.occurred_on),
    amount:
      typeof v.amount === 'string' && /^\d{1,24}(\.\d{1,12})?$/.test(v.amount)
        ? v.amount
        : invalid(),
    currency: currency(v.currency),
  };
}
const ordered = (a: HistorySample, b: HistorySample) =>
  a.occurred_on < b.occurred_on ||
  (a.occurred_on === b.occurred_on && a.id < b.id);
function scopedSamples(
  samples: HistorySample[],
  filter: HistoryFilter,
  asOf: string,
  uniqueDates: boolean,
) {
  const ids = new Set<string>();
  for (let i = 0; i < samples.length; i++) {
    const s = samples[i],
      previous = samples[i - 1];
    if (
      ids.has(s.id) ||
      s.occurred_on < filter.start_date ||
      s.occurred_on > asOf ||
      s.currency !== filter.currency ||
      (previous &&
        (!ordered(previous, s) ||
          (uniqueDates && previous.occurred_on === s.occurred_on)))
    )
      return invalid();
    ids.add(s.id);
  }
}
export function parseHistoryEvidence(
  value: unknown,
  rule: ScheduleRule,
  asOf: string,
): HistoryEvidence {
  const v = record(value),
    basis = parseHistoryBasis(v),
    as_of = date(v.as_of),
    selected_rule = parseScheduleRule(v.selected_rule),
    samples = array(v.samples, sample, 5);
  if (
    as_of !== asOf ||
    basis.filter.start_date > as_of ||
    samples.length < 3 ||
    !sameScheduleRule(rule, selected_rule)
  )
    return invalid();
  scopedSamples(samples, basis.filter, as_of, true);
  return { ...basis, as_of, samples, selected_rule };
}
export function parseScheduleHistoryPreview(
  value: unknown,
): ScheduleHistoryPreview {
  const v = record(value),
    status = choice(v.status, [
      'ready',
      'ambiguous',
      'insufficient',
      'duplicate_dates',
      'no_match',
      'too_many',
    ] as const),
    basis =
      status === 'too_many' || v.reason === 'fixed_term'
        ? {
            filter: parseHistoryFilter(v.filter),
            token: choice(v.token, [''] as const),
            algorithm_version: choice(v.algorithm_version, [
              'history-exact-v1',
            ] as const),
          }
        : parseHistoryBasis(v),
    as_of = date(v.as_of),
    subscription = parseSubscription(v.subscription),
    reason = choice(v.reason, ['', 'ended', 'fixed_term'] as const),
    timezone = choice(v.timezone, ['Asia/Tokyo'] as const),
    samples = array(v.samples, sample, 5),
    candidates = array(
      v.candidates,
      (x): HistoryCandidate => {
        const c = record(x),
          rule = parseScheduleRule(c.rule),
          candidate_date = nullable(c.candidate_date, date),
          reason = choice(c.reason, [
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
        return { rule, candidate_date, reason };
      },
      100,
    ),
    entries = array(
      v.entries,
      (x): HistoryEntry => {
        const e = record(x);
        return {
          ...sample(e),
          kind: choice(e.kind, ['payment', 'invoice', 'refund'] as const),
          state: choice(e.state, ['active', 'voided'] as const),
          reason: choice(e.reason, [
            'eligible',
            'voided',
            'invoice',
            'refund',
            'other_currency',
            'refunded',
          ] as const),
        };
      },
      1000,
    );
  if (
    basis.filter.start_date > as_of ||
    (reason === 'fixed_term') !==
      (subscription.contract_kind === 'fixed_term') ||
    (reason !== 'fixed_term' &&
      (reason === 'ended') !== (subscription.status === 'ended')) ||
    candidates.some(
      (c) =>
        (c.reason === 'ended') !== (reason === 'ended') ||
        (c.reason === 'fixed_term') !== (reason === 'fixed_term'),
    )
  )
    return invalid();
  const counts = nullable(v.counts, (x): HistoryCounts => {
    const c = record(x);
    return {
      total: integer(c.total, 0, 1000),
      eligible: integer(c.eligible, 0, 1000),
      sampled: integer(c.sampled, 0, 5),
      older_eligible: integer(c.older_eligible, 0, 1000),
      voided: integer(c.voided, 0, 1000),
      invoices: integer(c.invoices, 0, 1000),
      refunds: integer(c.refunds, 0, 1000),
      other_currency: integer(c.other_currency, 0, 1000),
      refunded: integer(c.refunded, 0, 1000),
      referenced_refunds: integer(c.referenced_refunds, 0, 1000),
    };
  });
  if (status === 'too_many' || reason === 'fixed_term') {
    if (
      (reason === 'fixed_term' && status !== 'insufficient') ||
      counts !== null ||
      samples.length ||
      candidates.length ||
      entries.length
    )
      return invalid();
  } else {
    if (
      !counts ||
      counts.total !== entries.length ||
      counts.total + counts.referenced_refunds > 1000 ||
      counts.total !==
        counts.eligible +
          counts.voided +
          counts.invoices +
          counts.refunds +
          counts.other_currency +
          counts.refunded ||
      counts.eligible !== counts.sampled + counts.older_eligible ||
      counts.sampled !== Math.min(5, counts.eligible) ||
      counts.sampled !== samples.length
    )
      return invalid();
    scopedSamples(samples, basis.filter, as_of, status !== 'duplicate_dates');
    const ids = new Set<string>(),
      tallies = {
        eligible: 0,
        voided: 0,
        invoice: 0,
        refund: 0,
        other_currency: 0,
        refunded: 0,
      };
    for (const [i, e] of entries.entries()) {
      if (
        ids.has(e.id) ||
        e.occurred_on < basis.filter.start_date ||
        e.occurred_on > as_of ||
        (i && !ordered(entries[i - 1], e))
      )
        return invalid();
      ids.add(e.id);
      tallies[e.reason]++;
      const expected =
        e.state === 'voided'
          ? 'voided'
          : e.kind === 'invoice'
            ? 'invoice'
            : e.kind === 'refund'
              ? 'refund'
              : e.currency !== basis.filter.currency
                ? 'other_currency'
                : e.reason === 'refunded'
                  ? 'refunded'
                  : 'eligible';
      if (e.reason !== expected) return invalid();
    }
    if (
      tallies.eligible !== counts.eligible ||
      tallies.voided !== counts.voided ||
      tallies.invoice !== counts.invoices ||
      tallies.refund !== counts.refunds ||
      tallies.other_currency !== counts.other_currency ||
      tallies.refunded !== counts.refunded
    )
      return invalid();
    const eligible = entries.filter((e) => e.reason === 'eligible');
    const latest = eligible.slice(-5);
    if (
      samples.some((s, i) => {
        const e = latest[i];
        return (
          !e ||
          s.id !== e.id ||
          s.version !== e.version ||
          s.occurred_on !== e.occurred_on ||
          s.amount !== e.amount ||
          s.currency !== e.currency
        );
      })
    )
      return invalid();
    const duplicate = eligible.some(
      (e, i) => i > 0 && e.occurred_on === eligible[i - 1].occurred_on,
    );
    if (
      (status === 'ready' && candidates.length !== 1) ||
      (status === 'ambiguous' && candidates.length < 2) ||
      (!['ready', 'ambiguous'].includes(status) && candidates.length) ||
      (status === 'insufficient') !== (!duplicate && samples.length < 3) ||
      (status === 'duplicate_dates') !== duplicate
    )
      return invalid();
    const rules = candidates.map((c) => JSON.stringify(c.rule));
    if (new Set(rules).size !== rules.length) return invalid();
  }
  return {
    ...basis,
    subscription,
    as_of,
    timezone,
    status,
    reason,
    counts,
    samples,
    candidates,
    entries,
  };
}
export function createScheduleHistoryAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  const request = createDailyRequest(base, fetcher);
  return {
    async preview(
      id: string,
      filter: HistoryFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const target = input(() => uuid(id)),
        body = input(() => parseHistoryFilter(filter));
      const result = parseScheduleHistoryPreview(
        await request(
          `/v1/subscriptions/${target}/billing-schedule/history-preview`,
          token,
          signal,
          'POST',
          body,
        ),
      );
      return result.subscription.id === target &&
        result.filter.start_date === body.start_date &&
        result.filter.currency === body.currency
        ? result
        : invalid();
    },
  };
}
export type ScheduleHistoryAPI = ReturnType<typeof createScheduleHistoryAPI>;
