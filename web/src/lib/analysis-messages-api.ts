import type { TokenProvider } from './api.ts';
import { parseAnalysisResult, type AnalysisResult } from './analysis-api.ts';
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
export const stageStates = [
  'not_started',
  'waiting',
  'running',
  'completed',
  'skipped',
  'needs_review',
  'failed',
  'unknown',
  'cancelled',
] as const;
export const messageFilters = [
  'all',
  'primary_waiting',
  'primary_running',
  'secondary_waiting',
  'secondary_running',
  'completed',
  'attention',
  'not_related',
] as const;
export type StageState = (typeof stageStates)[number];
export type MessageFilter = {
  account_id: string;
  stage: (typeof messageFilters)[number];
  limit: number;
  offset: number;
};
export type StageCounts = Record<StageState, number>;
export type AnalysisMessage = {
  id: string;
  account_id: string;
  account_label: string;
  discovered_at: string;
  primary: StageState;
  secondary: StageState;
  registration: StageState;
  result_id: string | null;
  service: string | null;
  amount: string | null;
  currency: string | null;
  event_type: string | null;
  error_code: string;
};
export type MessageDetail = {
  message: AnalysisMessage;
  result: AnalysisResult | null;
};
export type MessagePage = {
  messages: AnalysisMessage[];
  total: number;
  limit: number;
  offset: number;
  summary: {
    discovered: number;
    primary: StageCounts;
    secondary: StageCounts;
    registration: StageCounts;
  };
};
export function parseMessage(value: unknown): AnalysisMessage {
  const v = record(value);
  return {
    id: uuid(v.id),
    account_id: uuid(v.account_id),
    account_label: text(v.account_label, 200),
    discovered_at: timestamp(v.discovered_at),
    primary: choice(v.primary, stageStates),
    secondary: choice(v.secondary, stageStates),
    registration: choice(v.registration, stageStates),
    result_id: nullable(v.result_id, uuid),
    service: nullable(v.service, (x) => text(x, 500)),
    amount: nullable(v.amount, (x) => text(x, 100)),
    currency: nullable(v.currency, (x) => text(x, 10)),
    event_type: nullable(v.event_type, (x) => text(x, 100)),
    error_code: text(v.error_code, 100, true),
  };
}
function parseCounts(value: unknown): StageCounts {
  const v = record(value);
  return Object.fromEntries(
    stageStates.map((s) => [s, integer(v[s])]),
  ) as StageCounts;
}
export function createMessageAPI(base: string, fetcher: typeof fetch = fetch) {
  const request = createDailyRequest(base, fetcher);
  return {
    async messages(
      f: MessageFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<MessagePage> {
      const query = input(
        () =>
          new URLSearchParams({
            ...(f.account_id ? { account_id: uuid(f.account_id) } : {}),
            stage: choice(f.stage, messageFilters),
            limit: String(integer(f.limit, 1, 100)),
            offset: String(integer(f.offset)),
          }),
      );
      const v = record(
          await request(`/v1/analysis/messages?${query}`, token, signal),
        ),
        s = record(v.summary);
      return {
        messages: array(v.messages, parseMessage),
        ...pagination(v),
        summary: {
          discovered: integer(s.discovered),
          primary: parseCounts(s.primary),
          secondary: parseCounts(s.secondary),
          registration: parseCounts(s.registration),
        },
      };
    },
    async message(
      id: string,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<MessageDetail> {
      const target = input(() => uuid(id)),
        v = record(
          await request(`/v1/analysis/messages/${target}`, token, signal),
        ),
        message = parseMessage(v.message),
        result = nullable(v.result, parseAnalysisResult);
      if (
        message.id !== target ||
        (result &&
          (result.id !== message.result_id ||
            result.message_id !== message.id ||
            result.account_id !== message.account_id))
      )
        return invalid();
      return { message, result };
    },
  };
}
export type MessageAPI = ReturnType<typeof createMessageAPI>;
