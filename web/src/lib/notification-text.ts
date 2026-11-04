import type {
  Notification,
  NotificationDelivery,
  NotificationKind,
} from './notifications-api.ts';
export const notificationKindNames: Record<NotificationKind, string> = {
  renewal: '更新日',
  annual_charge: '年払い',
  trial_end: '無料期間終了',
  price_increase: '値上げ候補',
  import_error: '取り込みエラー',
};
export function notificationAction(notification: Notification) {
  const f = notification.facts;
  return f.kind === 'price_increase'
    ? `/analysis?tab=attention&result_id=${f.result_id}&event_index=${f.event_index}`
    : f.kind === 'import_error'
      ? `/import?account_id=${f.account_id}`
      : `/subscriptions/${f.contract_id}`;
}
export function deliveryState(delivery: NotificationDelivery) {
  if (delivery.channel === 'in_app')
    return delivery.state === 'accepted'
      ? 'アプリ内に表示済み'
      : 'アプリ内の表示を確認';
  if (delivery.complained) return '迷惑メールとして報告';
  if (delivery.bounced) return '宛先へ配信できませんでした';
  if (delivery.delivered) return '配信済み';
  if (delivery.failed || delivery.state === 'failed') return '送信失敗';
  const states = {
    queued: '送信待ち',
    claimed: '送信準備中',
    unknown: '送信結果不明（自動再送しません）',
    accepted: '送信受付済み（配信未確認）',
    suppressed: '送信しない',
  };
  return states[delivery.state as keyof typeof states] ?? '状態を確認';
}
export function deliveryReason(reason: string) {
  const reasons: Record<string, string> = {
    preferences_disabled: '通知設定で停止しています。',
    source_changed: '元情報が更新されました。',
    contract_inactive: '契約が通知の対象外です。',
    source_unavailable: '元情報を確認できません。',
    profile_unavailable: '本人の検証済みメール情報を確認できません。',
    sender_unavailable: '送信サービスの準備ができていません。',
    delivery_engine_unavailable: 'メール通知は準備中です。',
    lease_expired: '送信準備の期限を過ぎました。',
    provider_rejected: '送信サービスが要求を受け付けませんでした。',
    unknown_result: '受付結果を確認できません。自動で再送しません。',
  };
  return reasons[reason] ?? '通知の状態を確認してください。';
}
