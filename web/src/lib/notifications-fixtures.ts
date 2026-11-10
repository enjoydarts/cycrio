import type {
  NotificationPage,
  NotificationSettings,
  Notification,
} from './notifications-api.ts';
export const notificationID = '77777777-7777-4777-8777-777777777777';
export const notificationSettings = (): NotificationSettings => ({
  preferences: {
    version: 1,
    kinds: {
      renewal: { in_app: true, email: false, push: false },
      annual_charge: { in_app: true, email: false, push: false },
      trial_end: { in_app: true, email: false, push: false },
      price_increase: { in_app: true, email: false, push: false },
      import_error: { in_app: true, email: false, push: false },
      pending_review: { in_app: true, email: false, push: false },
    },
    lead_days: 7,
    send_hour: 9,
    timezone: 'Asia/Tokyo',
    email_opt_in: false,
    push_opt_in: false,
  },
  email: {
    available: false,
    reason: 'delivery_engine_unavailable',
    profile_ready: false,
    sender_ready: false,
  },
  push: { available: false, reason: 'environment_disabled' },
  as_of: '2026-11-03',
  timezone: 'Asia/Tokyo',
});
export const notification = (): Notification => ({
  id: notificationID,
  facts: {
    kind: 'price_increase',
    source_key: 'synthetic-price-change',
    contract_id: '11111111-1111-4111-8111-111111111111',
    result_id: '66666666-6666-4666-8666-666666666666',
    event_index: 0,
    service: 'Example Mail',
    scheduled_at: '2026-11-03T00:00:00Z',
    currency: 'USD',
    old_amount: '9',
    new_amount: '19',
    in_app: true,
    email: false,
    push: false,
  },
  match_basis: 'unique_confirmed_account_service',
  created_at: '2026-11-03T00:00:00Z',
  read_at: null,
  superseded_at: null,
});
export const notificationPage = (): NotificationPage => ({
  notifications: [notification()],
  total: 1,
  unread_count: 1,
  limit: 20,
  offset: 0,
  as_of: '2026-11-03',
  timezone: 'Asia/Tokyo',
});
