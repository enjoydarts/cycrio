import type { TokenProvider } from './api.ts';
import {
  parseProcessingResult,
  parseProcessingRun,
  type ProcessingResult,
  type ProcessingRun,
} from './mail-processing-api.ts';
import {
  array,
  choice,
  createDailyRequest,
  input,
  integer,
  invalid,
  pagination,
  nullable,
  record,
  text,
  uuid,
} from './daily-api.ts';
export type AnalysisResult = ProcessingResult & {
  account_id: string;
  account_label: string;
  registrations: { event_index: number; subscription_id: string }[];
  automation_state?:
    | 'pending'
    | 'not_applicable'
    | 'no_events'
    | 'registered'
    | 'needs_review'
    | 'failed';
  automation?: {
    event_index: number;
    state: 'registered' | 'needs_review' | 'failed';
    reason:
      | 'created'
      | 'attached'
      | 'already_registered'
      | 'ambiguous_identity'
      | 'invalid_facts'
      | 'recording_failed';
    subscription_id: string | null;
  }[];
  billing_automation?: {
    event_index: number;
    state: 'recorded' | 'not_applicable' | 'needs_review' | 'failed';
    reason:
      | 'created'
      | 'already_recorded'
      | 'missing_facts'
      | 'invalid_facts'
      | 'duplicate_candidate'
      | 'refund_target_ambiguous'
      | 'source_conflict'
      | 'recording_failed'
      | 'owner_detached'
      | 'non_financial_event';
    entry_id: string | null;
  }[];
};
export type AnalysisRun = ProcessingRun & { account_label: string };
export type AnalysisFilter = {
  account_id: string;
  state: 'all' | 'related' | 'failed' | 'unknown' | 'not_related';
  review: 'all' | 'pending' | 'registered' | 'attention';
  limit: number;
  offset: number;
};
export type AnalysisPage = {
  results: AnalysisResult[];
  total: number;
  limit: number;
  offset: number;
};
export type RunPage = {
  runs: AnalysisRun[];
  total: number;
  limit: number;
  offset: number;
};
export function parseAnalysisResult(value: unknown): AnalysisResult {
  const v = record(value),
    result = parseProcessingResult(v);
  const registrations = array(
    v.registrations,
    (x) => {
      const r = record(x);
      return {
        event_index: integer(r.event_index, 0, result.events.length - 1),
        subscription_id: uuid(r.subscription_id),
      };
    },
    32,
  );
  if (
    new Set(registrations.map((r) => r.event_index)).size !==
    registrations.length
  )
    return invalid();
  return {
    ...result,
    account_id: uuid(v.account_id),
    account_label: text(v.account_label, 200),
    registrations,
    automation_state:
      v.automation_state === undefined
        ? result.events.length
          ? 'pending'
          : 'no_events'
        : choice(v.automation_state, [
            'pending',
            'not_applicable',
            'no_events',
            'registered',
            'needs_review',
            'failed',
          ] as const),
    automation:
      v.automation === undefined
        ? []
        : array(
            v.automation,
            (x) => {
              const a = record(x);
              return {
                event_index: integer(
                  a.event_index,
                  0,
                  result.events.length - 1,
                ),
                state: choice(a.state, [
                  'registered',
                  'needs_review',
                  'failed',
                ]),
                reason: choice(a.reason, [
                  'created',
                  'attached',
                  'already_registered',
                  'ambiguous_identity',
                  'invalid_facts',
                  'recording_failed',
                ]),
                subscription_id: nullable(a.subscription_id, uuid),
              };
            },
            32,
          ),
    billing_automation:
      v.billing_automation === undefined
        ? []
        : array(
            v.billing_automation,
            (x) => {
              const a = record(x);
              return {
                event_index: integer(
                  a.event_index,
                  0,
                  result.events.length - 1,
                ),
                state: choice(a.state, [
                  'recorded',
                  'not_applicable',
                  'needs_review',
                  'failed',
                ]),
                reason: choice(a.reason, [
                  'created',
                  'already_recorded',
                  'missing_facts',
                  'invalid_facts',
                  'duplicate_candidate',
                  'refund_target_ambiguous',
                  'source_conflict',
                  'recording_failed',
                  'owner_detached',
                  'non_financial_event',
                ]),
                entry_id: nullable(a.entry_id, uuid),
              };
            },
            32,
          ),
  };
}
export function createAnalysisAPI(base: string, fetcher: typeof fetch = fetch) {
  const request = createDailyRequest(base, fetcher);
  return {
    async results(
      f: AnalysisFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<AnalysisPage> {
      const query = input(() => {
        const state = choice(f.state, [
            'all',
            'related',
            'failed',
            'unknown',
            'not_related',
          ]),
          review = choice(f.review, [
            'all',
            'pending',
            'registered',
            'attention',
          ]);
        if (['pending', 'registered'].includes(review) && state !== 'related')
          return invalid();
        return new URLSearchParams({
          ...(f.account_id ? { account_id: uuid(f.account_id) } : {}),
          state,
          review,
          limit: String(integer(f.limit, 1, 100)),
          offset: String(integer(f.offset)),
        });
      });
      const v = record(
        await request(`/v1/analysis/results?${query}`, token, signal),
      );
      return {
        results: array(v.results, parseAnalysisResult),
        ...pagination(v),
      };
    },
    async runs(
      f: Pick<AnalysisFilter, 'account_id' | 'limit' | 'offset'>,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<RunPage> {
      const query = input(
        () =>
          new URLSearchParams({
            ...(f.account_id ? { account_id: uuid(f.account_id) } : {}),
            limit: String(integer(f.limit, 1, 100)),
            offset: String(integer(f.offset)),
          }),
      );
      const v = record(
        await request(`/v1/analysis/runs?${query}`, token, signal),
      );
      return {
        runs: array(v.runs, (x) => {
          const r = record(x);
          return {
            ...parseProcessingRun(r, uuid(r.account_id)),
            account_label: text(r.account_label, 200),
          };
        }),
        ...pagination(v),
      };
    },
  };
}
export type AnalysisAPI = ReturnType<typeof createAnalysisAPI>;
