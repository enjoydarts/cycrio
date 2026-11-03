import { subscription, id } from './subscriptions-fixtures.ts';
import type {
  ChangePreview,
  ChangeReceipt,
  ChangeEvidence,
} from './subscription-changes-api.ts';
export const changeEvidence = (): ChangeEvidence => ({
  result_id: id,
  event_index: 0,
  received_at: '2026-11-01T12:00:00Z',
  original_event: {
    service: 'Example',
    plan: null,
    event_type: 'change',
    amount: null,
    currency: null,
    recurring_amount: '9',
    recurring_currency: 'USD',
    interval_unit: null,
    interval_count: null,
    billed_at: null,
    period_start: null,
    period_end: null,
    next_billing_at: null,
  },
});
export const changePreview = (): ChangePreview => ({
  subscription: subscription(),
  source: changeEvidence(),
  additional_evidence: [],
  preview_token: 'a'.repeat(64),
  as_of: '2026-11-03',
  timezone: 'Asia/Tokyo',
  algorithm_version: 'subscription-change-v1',
  fields: [
    {
      key: 'amount',
      current: '0',
      proposed: '9',
      selectable: true,
      reason: '',
    },
    {
      key: 'currency',
      current: 'JPY',
      proposed: 'USD',
      selectable: true,
      reason: '',
    },
  ],
  warnings: [],
  derived_next_billing_date: null,
});
export const changeReceipt = (): ChangeReceipt => ({
  request_id: id,
  applied_version: 2,
  created_at: '2026-11-03T00:00:00Z',
  selected_fields: ['amount', 'currency'],
  before: subscription(),
  after: { ...subscription(), amount: '9', currency: 'USD', version: 2 },
  source: changeEvidence(),
  additional_evidence: [],
  algorithm_version: 'subscription-change-v1',
});
