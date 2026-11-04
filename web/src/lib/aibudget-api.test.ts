import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createAIBudgetAPI,
  parseAIBudgetState,
  budgetDecimal,
} from './aibudget-api.ts';
import { owner, budgetState } from './aibudget-test-fixtures.ts';
test('budget decimals preserve precision and reject invalid input', () => {
  const amount = '123456789012345678901234.123456789012';
  assert.equal(budgetDecimal(amount), amount);
  for (const v of ['-1', '1e3', '01', '1.', '', '1.1234567890123', 1])
    assert.throws(() => budgetDecimal(v));
});
test('spent amount preserves ledger precision independently from input limits', () => {
  const spent = '123456789012345678901234567890.123456789012345678901234567890';
  assert.equal(
    parseAIBudgetState(budgetState({ spent_usd: spent }), owner).spent_usd,
    spent,
  );
});
test('state rejects wrong owner, invalid month boundary, reason, and counts', () => {
  assert.equal(parseAIBudgetState(budgetState(), owner).version, 0);
  for (const patch of [
    { user_id: '22222222-2222-4222-8222-222222222222' },
    { month: '2026-13' },
    { from: '2026-11-01T00:00:00Z' },
    { pending_calls: -1 },
    { blocked_reason: 'secret' },
    { monthly_limit_usd: 0 },
  ])
    assert.throws(() =>
      parseAIBudgetState({ ...budgetState(), ...patch }, owner),
    );
});
test('API sends confirmed CAS decimal and authenticates without cookies', async () => {
  const calls: RequestInit[] = [];
  const api = createAIBudgetAPI('https://example.test/', async (url, init) => {
    assert.equal(url, `https://example.test/v1/admin/ai/budgets/${owner}`);
    calls.push(init!);
    return new Response(
      JSON.stringify(budgetState({ version: 1, monthly_limit_usd: '0' })),
    );
  });
  await api.save(
    owner,
    { expected_version: 0, monthly_limit_usd: '0', confirmed: true },
    async () => 'token',
  );
  assert.equal(calls[0].credentials, 'omit');
  assert.deepEqual(JSON.parse(calls[0].body as string), {
    expected_version: 0,
    monthly_limit_usd: '0',
    confirmed: true,
  });
  await assert.rejects(
    api.save(
      owner,
      { expected_version: 0, monthly_limit_usd: '1', confirmed: false as true },
      async () => 'token',
    ),
  );
  assert.equal(calls.length, 1);
});
