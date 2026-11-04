import type { AIBudgetState } from './aibudget-api.ts';
export const owner = '11111111-1111-4111-8111-111111111111';
export const budgetState = (
  patch: Partial<AIBudgetState> = {},
): AIBudgetState => ({
  user_id: owner,
  version: 0,
  monthly_limit_usd: null,
  month: '2026-11',
  timezone: 'Asia/Tokyo',
  from: '2026-10-31T15:00:00Z',
  to: '2026-11-30T15:00:00Z',
  spent_usd: '0',
  unknown_calls: 0,
  pending_calls: 0,
  non_usd_calls: 0,
  blocked_reason: '',
  ...patch,
});
