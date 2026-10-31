import type { TokenProvider } from './api.ts';
import {
  array,
  createDailyRequest,
  input,
  integer,
  pagination,
  record,
  text,
  uuid,
} from './daily-api.ts';
import {
  parseProcessingEvent,
  type ProcessingEvent,
} from './mail-processing-api.ts';
export type Candidate = {
  result_id: string;
  event_index: number;
  account_id: string;
  account_label: string;
  event: ProcessingEvent;
  mail_count: number;
};
export type CandidateFilter = {
  account_id: string;
  limit: number;
  offset: number;
};
export type CandidatePage = {
  candidates: Candidate[];
  total: number;
  limit: number;
  offset: number;
};
export type ImportSummary = {
  mail_count: number;
  pending_count: number;
  succeeded_count: number;
  unrelated_count: number;
  failed_count: number;
  unknown_count: number;
  candidate_count: number;
  subscription_count: number;
};
export function createApprovalAPI(base: string, fetcher: typeof fetch = fetch) {
  const request = createDailyRequest(base, fetcher);
  return {
    async candidates(
      f: CandidateFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<CandidatePage> {
      const q = input(
        () =>
          new URLSearchParams({
            ...(f.account_id ? { account_id: uuid(f.account_id) } : {}),
            limit: String(integer(f.limit, 1, 100)),
            offset: String(integer(f.offset)),
          }),
      );
      const v = record(
        await request(`/v1/subscriptions/candidates?${q}`, token, signal),
      );
      return {
        ...pagination(v),
        candidates: array(v.candidates, (x) => {
          const c = record(x);
          return {
            result_id: uuid(c.result_id),
            event_index: integer(c.event_index, 0, 31),
            account_id: uuid(c.account_id),
            account_label: text(c.account_label, 200),
            event: parseProcessingEvent(c.event),
            mail_count: integer(c.mail_count, 1),
          };
        }),
      };
    },
    async dismiss(
      source: Pick<Candidate, 'result_id' | 'event_index'>,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => ({
        source_result_id: uuid(source.result_id),
        source_event_index: integer(source.event_index, 0, 31),
        confirmed: true,
      }));
      await request(
        '/v1/subscriptions/candidates/dismiss',
        token,
        signal,
        'POST',
        body,
      );
    },
    async summary(
      account: string,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<ImportSummary> {
      const id = input(() => uuid(account));
      const v = record(
        await request(
          `/v1/mail/accounts/${id}/processing-summary`,
          token,
          signal,
        ),
      );
      return {
        mail_count: integer(v.mail_count),
        pending_count: integer(v.pending_count),
        succeeded_count: integer(v.succeeded_count),
        unrelated_count: integer(v.unrelated_count),
        failed_count: integer(v.failed_count),
        unknown_count: integer(v.unknown_count),
        candidate_count: integer(v.candidate_count),
        subscription_count: integer(v.subscription_count),
      };
    },
  };
}
