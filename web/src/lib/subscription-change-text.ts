import type { ChangeKey, ChangeValue } from './subscription-changes-api.ts';
import type { Subscription } from './subscriptions-api.ts';
import { cycle } from './daily-text.ts';
export const changeLabels: Record<ChangeKey, string> = {
  amount: '通常継続料金',
  currency: '通貨',
  interval: '請求周期',
  plan: 'プラン',
  contract_kind: '契約種別',
  effective_date: '効力日',
  access_end_date: '利用終了日',
  next_billing_date: '次回請求日',
};
const reasons: Record<string, string> = {
  future_effective:
    '将来の効力日が指定されています。効力日を迎えてから内容を再確認してください。',
  unknown_source_date:
    '元メールの受信日時が不明のため、自動で変更候補を選べません。',
  older_source:
    '現在の契約に紐付いた情報より古いメールです。契約の変更には使えません。',
  ambiguous_source_date:
    '同じ受信日時の情報があり、変更の前後関係を判断できません。',
  fixed_term_requires_clearing:
    '期間限定契約への変更には料金・請求周期・次回請求日の確認が必要です。契約編集で変更してください。',
  cancellation_requires_review:
    '解約・停止に関する通知です。契約状態や利用終了日は自動変更しません。元情報を確認してください。',
  effective_date_requires_review:
    '効力日だけでは変更時点を確定できません。契約編集で確認してください。',
  derived_billing_date:
    '請求周期を変更すると、次回請求日を元の起点から再計算します。推定日を確認してください。',
  missing_counterpart:
    '通常継続料金と通貨の組み合わせが不足しています。契約編集で補完してください。',
};
export const changeReason = (code: string) =>
  reasons[code] ?? '元情報を確認し、契約編集で変更してください。';
export function changeValue(key: ChangeKey, value: ChangeValue) {
  if (value === null) return '不明';
  if (typeof value === 'object') return cycle(value.unit, value.count);
  if (key === 'contract_kind')
    return value === 'recurring'
      ? '継続契約'
      : value === 'fixed_term'
        ? '期間限定（自動更新なし）'
        : '不明';
  return value;
}
export function subscriptionChangeValue(
  s: Subscription,
  key: ChangeKey,
): ChangeValue {
  return key === 'interval'
    ? s.interval_unit && s.interval_count
      ? { unit: s.interval_unit, count: s.interval_count }
      : null
    : (s[key] ?? null);
}
