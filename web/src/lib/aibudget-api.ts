import type { TokenProvider } from './api.ts';
import { decimal } from './catalog-api.ts';
import {
  choice,
  createDailyRequest,
  date,
  input,
  integer,
  invalid,
  nullable,
  record,
  timestamp,
  uuid,
} from './daily-api.ts';
export const budgetDecimal = (v: unknown): string =>
  typeof v === 'string' && /^(?:0|[1-9]\d{0,23})(?:\.\d{1,12})?$/.test(v)
    ? v
    : invalid();
export type AIBudgetState = {
  user_id: string;
  version: number;
  monthly_limit_usd: string | null;
  month: string;
  timezone: 'Asia/Tokyo';
  from: string;
  to: string;
  spent_usd: string;
  unknown_calls: number;
  pending_calls: number;
  non_usd_calls: number;
  blocked_reason: '' | 'budget_exceeded' | 'budget_unknown' | 'budget_pending';
};
export type AIBudgetInput = {
  expected_version: number;
  monthly_limit_usd: string | null;
  confirmed: true;
};
export function parseAIBudgetState(
  value: unknown,
  owner: string,
): AIBudgetState {
  const v = record(value),
    id = uuid(v.user_id);
  if (
    id !== uuid(owner) ||
    typeof v.month !== 'string' ||
    !/^(?!0000)\d{4}-(?:0[1-9]|1[0-2])$/.test(v.month)
  )
    return invalid();
  date(`${v.month}-01`);
  const start = new Date(`${v.month}-01T00:00:00+09:00`);
  const year = Number(v.month.slice(0, 4)),
    month = Number(v.month.slice(5));
  const expectedTo = Date.UTC(year, month, 1) - 9 * 60 * 60 * 1000;
  const from = timestamp(v.from),
    to = timestamp(v.to);
  if (Date.parse(from) !== start.getTime() || Date.parse(to) !== expectedTo)
    return invalid();
  return {
    user_id: id,
    version: integer(v.version),
    monthly_limit_usd: nullable(v.monthly_limit_usd, budgetDecimal),
    month: v.month,
    timezone: choice(v.timezone, ['Asia/Tokyo']),
    from,
    to,
    spent_usd: decimal(v.spent_usd),
    unknown_calls: integer(v.unknown_calls),
    pending_calls: integer(v.pending_calls),
    non_usd_calls: integer(v.non_usd_calls),
    blocked_reason: choice(v.blocked_reason, [
      '',
      'budget_exceeded',
      'budget_unknown',
      'budget_pending',
    ]),
  };
}
export function createAIBudgetAPI(base: string, fetcher: typeof fetch = fetch) {
  const request = createDailyRequest(base, fetcher);
  const path = (id: string) => `/v1/admin/ai/budgets/${input(() => uuid(id))}`;
  return {
    async read(id: string, token: TokenProvider, signal?: AbortSignal) {
      return parseAIBudgetState(await request(path(id), token, signal), id);
    },
    async save(
      id: string,
      v: AIBudgetInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => {
        if (v.confirmed !== true) return invalid();
        return {
          expected_version: integer(v.expected_version),
          monthly_limit_usd: nullable(v.monthly_limit_usd, budgetDecimal),
          confirmed: true,
        };
      });
      return parseAIBudgetState(
        await request(path(id), token, signal, 'PUT', body),
        id,
      );
    },
  };
}
export type AIBudgetAPI = ReturnType<typeof createAIBudgetAPI>;
