import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  createAIBudgetController,
  emptyBudgetView,
} from './aibudget-controller.ts';
import { owner, budgetState } from './aibudget-test-fixtures.ts';
import type { AIBudgetAPI, AIBudgetState } from './aibudget-api.ts';
const state = (patch = {}) => budgetState(patch) as AIBudgetState;
test('save requires confirmation, keeps CAS and edited amount after conflict', async () => {
  let saves = 0;
  let view = emptyBudgetView();
  const api: AIBudgetAPI = {
    read: async () => state(),
    save: async (_id, input) => {
      saves++;
      assert.equal(input.expected_version, 0);
      throw new ApiError('conflict');
    },
  };
  const c = createAIBudgetController(
    api,
    async () => 'token',
    (v) => (view = v),
    () => {},
  );
  await c.load(owner);
  c.edit('2.000000000001');
  await c.save();
  assert.equal(saves, 0);
  c.confirm(true);
  await c.save();
  assert.equal(view.amount, '2.000000000001');
  assert.equal(view.state?.version, 0);
  assert.equal(view.error, 'conflict');
});
test('unknown save reconciles with GET only and preserves unmatched edited value', async () => {
  let reads = 0,
    saves = 0;
  let view = emptyBudgetView();
  const c = createAIBudgetController(
    {
      read: async () => {
        reads++;
        return state();
      },
      save: async () => {
        saves++;
        throw new ApiError('dependency_unavailable');
      },
    },
    async () => 'token',
    (v) => (view = v),
    () => {},
  );
  await c.load(owner);
  c.edit('0');
  c.confirm(true);
  await c.save();
  assert.equal(reads, 2);
  assert.equal(saves, 1);
  assert.equal(view.amount, '0');
  assert.equal(view.uncertain, true);
  await c.save();
  assert.equal(saves, 1);
});
test('late previous owner response is discarded', async () => {
  let resolve!: (s: AIBudgetState) => void;
  let view = emptyBudgetView();
  const other = '22222222-2222-4222-8222-222222222222';
  const c = createAIBudgetController(
    {
      read: async (id) =>
        id === owner
          ? new Promise((r) => (resolve = r))
          : state({ user_id: other }),
      save: async () => state(),
    },
    async () => 'token',
    (v) => (view = v),
    () => {},
  );
  const first = c.load(owner);
  await c.load(other);
  resolve(state());
  await first;
  assert.equal(view.state?.user_id, other);
});
test('uncertain save accepts exact next revision and decimal normalization only', async () => {
  for (const [wanted, stored, accepted] of [
    ['1.00', '1', true],
    ['10', '1', false],
    ['0', '0.0', true],
    ['', '0', false],
  ] as const) {
    let reads = 0;
    let view = emptyBudgetView();
    const c = createAIBudgetController(
      {
        read: async () =>
          ++reads === 1
            ? state()
            : state({ version: 1, monthly_limit_usd: stored }),
        save: async () => {
          throw new ApiError('dependency_unavailable');
        },
      },
      async () => 'token',
      (v) => (view = v),
      () => {},
    );
    await c.load(owner);
    c.edit(wanted);
    c.confirm(true);
    await c.save();
    assert.equal(view.saved, accepted);
    assert.equal(view.uncertain, !accepted);
  }
});
test('explicit reconciliation retains payload and original CAS until GET confirms it', async () => {
  let reads = 0,
    saves = 0;
  let view = emptyBudgetView();
  const c = createAIBudgetController(
    {
      read: async () =>
        ++reads < 3 ? state() : state({ version: 1, monthly_limit_usd: '4' }),
      save: async () => {
        saves++;
        throw new ApiError('dependency_unavailable');
      },
    },
    async () => 'token',
    (v) => (view = v),
    () => {},
  );
  await c.load(owner);
  c.edit('4.00');
  c.confirm(true);
  await c.save();
  assert.equal(view.amount, '4.00');
  assert.equal(view.state?.version, 0);
  await c.reconcile();
  assert.equal(view.saved, true);
  assert.equal(saves, 1);
  assert.equal(reads, 3);
});
