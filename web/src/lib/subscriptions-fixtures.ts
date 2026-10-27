import type { Subscription, SubscriptionValues } from './subscriptions-api.ts';
export const id = '11111111-1111-4111-8111-111111111111';
export const values: SubscriptionValues = {
  service: 'サービス',
  plan: null,
  amount: '0',
  currency: 'JPY',
  interval_unit: null,
  interval_count: null,
  next_billing_date: '2028-02-29',
  status: 'active',
};
export const subscription = (): Subscription => ({
  provenance: 'owner_confirmed',
  ...values,
  id,
  version: 1,
  created_at: '2026-10-26T00:00:00Z',
  updated_at: '2026-10-26T00:00:00Z',
  sources: [],
});
