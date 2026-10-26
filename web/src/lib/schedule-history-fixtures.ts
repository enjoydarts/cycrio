import { subscription } from './subscriptions-fixtures.ts';
export const historyRule = {
  interval_unit: 'month' as const,
  interval_count: 1,
  anchor_date: '2026-07-31',
  month_end: true,
};
export const historyFilter = { start_date: '2026-01-01', currency: 'JPY' };
export const historyBasis = () => ({
  filter: { ...historyFilter },
  token: 'a'.repeat(64),
  algorithm_version: 'history-exact-v1' as const,
});
export const historyPreview = () => {
  const samples = ['2026-07-31', '2026-08-31', '2026-09-30'].map(
    (occurred_on, i) => ({
      id: `0000000${i + 1}-1111-4111-8111-111111111111`,
      version: 1,
      occurred_on,
      amount: '0',
      currency: 'JPY',
    }),
  );
  return {
    subscription: subscription(),
    ...historyBasis(),
    as_of: '2026-10-26',
    timezone: 'Asia/Tokyo' as const,
    status: 'ready' as const,
    reason: '' as const,
    counts: {
      total: 3,
      eligible: 3,
      sampled: 3,
      older_eligible: 0,
      voided: 0,
      invoices: 0,
      refunds: 0,
      other_currency: 0,
      refunded: 0,
      referenced_refunds: 0,
    },
    samples,
    candidates: [
      {
        rule: { ...historyRule },
        candidate_date: '2026-10-31',
        reason: '' as const,
      },
    ],
    entries: samples.map((s) => ({
      ...s,
      kind: 'payment' as const,
      state: 'active' as const,
      reason: 'eligible' as const,
    })),
  };
};
export const historyEvidence = () => ({
  ...historyBasis(),
  as_of: '2026-10-26',
  samples: historyPreview().samples,
  selected_rule: { ...historyRule },
});
