import type { TokenProvider } from './api.ts';
import {
  parseAdditionalSources,
  type CandidateSource,
  type CandidateSources,
} from './candidate-selection.ts';
import {
  array,
  choice,
  createDailyRequest,
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
import {
  parseProcessingEvent,
  type ProcessingEvent,
} from './mail-processing-api.ts';
export type Candidate = {
  received_at?: string | null;
  result_id: string;
  event_index: number;
  account_id: string;
  account_label: string;
  event: ProcessingEvent;
  mail_count: number;
  related_count?: number;
};
export type CandidateFilter = {
  q?: string;
  sort?: 'received_desc' | 'received_asc' | 'service' | 'service_desc';
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
export function parseCandidate(value: unknown): Candidate {
  const c = record(value);
  return {
    received_at: nullable(c.received_at ?? null, timestamp),
    result_id: uuid(c.result_id),
    event_index: integer(c.event_index, 0, 31),
    account_id: uuid(c.account_id),
    account_label: text(c.account_label, 200),
    event: parseProcessingEvent(c.event),
    mail_count: integer(c.mail_count, 1),
    related_count: integer(c.related_count ?? 1, 1),
  };
}
export function createApprovalAPI(base: string, fetcher: typeof fetch = fetch) {
  const request = createDailyRequest(base, fetcher);
  const parsePage = (value: unknown): CandidatePage => {
    const v = record(value);
    return {
      ...pagination(v),
      candidates: array(v.candidates, parseCandidate),
    };
  };
  return {
    async getCandidate(
      source: CandidateSource,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<Candidate> {
      const path = input(
        () =>
          `/v1/subscription-candidates/${uuid(source.result_id)}/${integer(source.event_index, 0, 31)}`,
      );
      const result = parseCandidate(await request(path, token, signal));
      return result.result_id === source.result_id &&
        result.event_index === source.event_index
        ? result
        : invalid();
    },
    async related(
      anchor: CandidateSource,
      limit: number,
      offset: number,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<CandidatePage> {
      const query = input(
        () =>
          new URLSearchParams({
            result_id: uuid(anchor.result_id),
            event_index: String(integer(anchor.event_index, 0, 31)),
            limit: String(integer(limit, 1, 100)),
            offset: String(integer(offset)),
          }),
      );
      return parsePage(
        await request(
          `/v1/subscriptions/candidates/related?${query}`,
          token,
          signal,
        ),
      );
    },
    async candidates(
      f: CandidateFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<CandidatePage> {
      const q = input(
        () =>
          new URLSearchParams({
            q: text((f.q ?? '').trim(), 200, true),
            sort: choice(f.sort ?? 'received_desc', [
              'received_desc',
              'received_asc',
              'service',
              'service_desc',
            ]),
            ...(f.account_id ? { account_id: uuid(f.account_id) } : {}),
            limit: String(integer(f.limit, 1, 100)),
            offset: String(integer(f.offset)),
          }),
      );
      return parsePage(
        await request(`/v1/subscriptions/candidates?${q}`, token, signal),
      );
    },
    async dismiss(
      source: CandidateSources,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => ({
        source_result_id: uuid(source.result_id),
        source_event_index: integer(source.event_index, 0, 31),
        ...parseAdditionalSources(source, source.additional_sources),
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
