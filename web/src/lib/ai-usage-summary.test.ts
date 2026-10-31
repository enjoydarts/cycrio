import assert from 'node:assert/strict';
import test from 'node:test';
import { providerTotals, usageCounts } from './ai-usage-summary.ts';
import type { Breakdown, Totals } from './airuntime-api.ts';

const total = (patch: Partial<Totals> = {}): Totals => ({
  currency: 'USD',
  amount: '0.1',
  estimated_amount: '0.1',
  reported_amount: '0',
  calls: 1,
  unknown_calls: 0,
  failures: 0,
  ...patch,
});
const row = (
  totals: Totals[],
  provider: Breakdown['provider'] = 'openai',
): Breakdown => ({
  provider,
  model_id: 'id',
  model_name: 'Model',
  stage: 'classification',
  totals,
});

test('provider totals add decimal strings exactly across models and stages without mixing currencies or mutating input', () => {
  const rows = [
    row([
      total(),
      total({
        currency: 'JPY',
        amount: '9007199254740993.00000001',
        estimated_amount: '0',
        reported_amount: '9007199254740993.00000001',
      }),
    ]),
    row([
      total({ amount: '0.2', estimated_amount: '0', reported_amount: '0.2' }),
      total({
        currency: 'JPY',
        amount: '0.00000009',
        estimated_amount: '0.00000009',
      }),
    ]),
    row([total({ amount: '1.25' })], 'anthropic'),
  ];
  const before = structuredClone(rows);
  assert.deepEqual(providerTotals(rows), [
    {
      provider: 'openai',
      totals: [
        total({
          amount: '0.3',
          estimated_amount: '0.1',
          reported_amount: '0.2',
          calls: 2,
        }),
        total({
          currency: 'JPY',
          amount: '9007199254740993.0000001',
          estimated_amount: '0.00000009',
          reported_amount: '9007199254740993.00000001',
          calls: 2,
        }),
      ],
      calls: 4,
      failures: 0,
    },
    {
      provider: 'anthropic',
      totals: [total({ amount: '1.25' })],
      calls: 1,
      failures: 0,
    },
  ]);
  assert.deepEqual(rows, before);
});
test('provider cards keep unknown calls in counts and known zero amounts without inventing unknown currency costs', () => {
  const rows = [
    row([
      total({ amount: '0', estimated_amount: '0' }),
      total({
        currency: 'EUR',
        amount: '0',
        estimated_amount: '0',
        calls: 2,
        unknown_calls: 2,
        failures: 1,
      }),
      total({
        currency: null,
        amount: '0',
        estimated_amount: '0',
        calls: 3,
        unknown_calls: 3,
        failures: 2,
      }),
    ]),
  ];
  assert.deepEqual(providerTotals(rows), [
    {
      provider: 'openai',
      totals: [total({ amount: '0', estimated_amount: '0' })],
      calls: 6,
      failures: 3,
    },
  ]);
  assert.deepEqual(usageCounts(rows[0].totals), { calls: 6, failures: 3 });
  assert.deepEqual(providerTotals([]), []);
  assert.deepEqual(usageCounts([]), { calls: 0, failures: 0 });
});
