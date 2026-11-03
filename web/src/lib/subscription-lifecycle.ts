import type { SubscriptionValues, Subscription } from './subscriptions-api.ts';
export const tokyoDate = (now = new Date()) =>
  new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Tokyo',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(now);
export function lifecycleLabels(
  value: Pick<
    SubscriptionValues,
    'trial_end_date' | 'auto_renew' | 'access_end_date'
  >,
  asOf = tokyoDate(),
) {
  const labels: string[] = [];
  if (value.trial_end_date) labels.push(`無料期間終了 ${value.trial_end_date}`);
  if (value.auto_renew === false) labels.push('自動更新停止');
  if (value.access_end_date)
    labels.push(
      `${value.access_end_date < asOf ? '利用終了' : '利用終了予定'} ${value.access_end_date}`,
    );
  return labels;
}
export function subscriptionEditorValues(subscription: Subscription) {
  return {
    ...subscription,
    next_billing_date:
      subscription.next_billing_mode === 'manual' &&
      subscription.manual_next_billing_date !== undefined
        ? subscription.manual_next_billing_date
        : subscription.next_billing_date,
  };
}
