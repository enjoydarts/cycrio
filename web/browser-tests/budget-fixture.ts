import { report } from '../src/lib/airuntime-test-fixtures.ts';
export const budgetOwner = '11111111-1111-4111-8111-111111111111';
export function budgetFixture(mode: string) {
  let version = 0,
    limit: string | null = null;
  const response = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    });
  const state = () => ({
    user_id: budgetOwner,
    version,
    monthly_limit_usd: limit,
    month: '2026-11',
    timezone: 'Asia/Tokyo',
    from: '2026-10-31T15:00:00Z',
    to: '2026-11-30T15:00:00Z',
    spent_usd: '0.123456789012',
    unknown_calls: 0,
    pending_calls: 0,
    non_usd_calls: 0,
    blocked_reason: limit === '0' ? 'budget_exceeded' : '',
  });
  return (url: URL, method: string, body: Record<string, unknown> | null) => {
    if (url.pathname === '/synthetic/v1/admin/ai/costs')
      return response({
        ...report(),
        month: url.searchParams.get('month'),
        users: [
          {
            user_id: budgetOwner,
            calls: 1,
            unknown_calls: 0,
            failures: 0,
            totals: [],
            mail_accounts: [],
          },
        ],
        users_total: 1,
      });
    if (url.pathname === '/synthetic/v1/admin/ai/calls')
      return response({ calls: [], total: 0 });
    if (url.pathname !== `/synthetic/v1/admin/ai/budgets/${budgetOwner}`)
      throw Error('Unexpected budget fixture path');
    if (method === 'GET') return response(state());
    if (
      method !== 'PUT' ||
      !body ||
      Object.keys(body).sort().join(',') !==
        'confirmed,expected_version,monthly_limit_usd' ||
      body.confirmed !== true ||
      body.expected_version !== version ||
      !(
        body.monthly_limit_usd === null ||
        typeof body.monthly_limit_usd === 'string'
      )
    )
      throw Error('Unexpected budget payload');
    if (mode === 'budget-conflict') {
      version++;
      return response({}, 409);
    }
    version++;
    limit = body.monthly_limit_usd as string | null;
    if (mode === 'budget-uncertain') return response({}, 503);
    return response(state());
  };
}
