import type { MailAutomationState } from './mail-automation-api.ts';

const administratorAIReasons = new Set([
  'budget_exceeded',
  'budget_unknown',
  'runtime_inactive',
  'policy_disabled',
  'full_email_not_allowed',
  'policy_reconfirmation_required',
  'configuration_unavailable',
  'configuration_changed',
]);

export function automationAIWaiting(s: {
  state: MailAutomationState['state'];
  ready: boolean;
  reason?: string;
  blocked_reasons: string[];
  preparation_blocked_reasons?: string[];
}): boolean {
  if (s.state !== 'enabled' || s.ready) return false;
  const reasons = [
    s.reason ?? '',
    ...s.blocked_reasons,
    ...(s.preparation_blocked_reasons ?? []),
  ].filter(Boolean);
  return (
    reasons.length > 0 && reasons.every((r) => administratorAIReasons.has(r))
  );
}

export function automationAISetupPath(reasons: string[]): string {
  if (reasons.some((r) => ['budget_exceeded', 'budget_unknown'].includes(r)))
    return '/settings/usage';
  return reasons.some((r) =>
    [
      'policy_disabled',
      'full_email_not_allowed',
      'policy_reconfirmation_required',
    ].includes(r),
  )
    ? '/settings/ai?stage=permission'
    : '/settings/ai';
}

export const automationAIWaitingDescription =
  '取り込み条件と進捗を保持しています。管理者のAI設定が整うと、取り込み・解析を自動で再開します。利用者による条件の再確認は不要です。';

export function automationProgressStatus(s: MailAutomationState): string {
  if (s.state === 'stopped') return '停止中';
  if (
    automationAIWaiting(s) &&
    automationAISetupPath([
      s.reason,
      ...s.blocked_reasons,
      ...s.preparation_blocked_reasons,
    ]) === '/settings/usage'
  )
    return 'AI 月額上限の確認待ち';
  if (automationAIWaiting(s)) return '管理者のAI設定待ち';
  if (s.state === 'needs_confirmation') return '条件の再確認が必要';
  if (s.state === 'not_configured') return '取り込み開始前';
  if (s.retry_at) return '接続の再試行待ち';
  if (s.running) return 'メール確認中';
  if (!s.progress.bootstrap_complete) return '初回同期の準備中';
  if (!s.progress.historical_complete) return 'メール探索中';
  if (s.progress.pending_count) return '解析中';
  return '次の確認待ち';
}

export function automationProgressDescription(s: MailAutomationState): string {
  if (s.state === 'stopped')
    return '取り込み・解析は停止中です。再開すると進捗を引き継ぎます。';
  if (automationAIWaiting(s))
    return automationAISetupPath([
      s.reason,
      ...s.blocked_reasons,
      ...s.preparation_blocked_reasons,
    ]) === '/settings/usage'
      ? 'AI の月額上限または原価の確認が必要です。取り込み条件と進捗を保持し、判定条件が整うと自動で再開します。'
      : automationAIWaitingDescription;
  if (s.state === 'needs_confirmation')
    return '条件を確認して開始するまで、取り込み・解析は再開しません。';
  if (s.state === 'not_configured')
    return '初回の期間と送信条件を確認して取り込みを開始してください。';
  if (s.retry_at)
    return '接続確認を一時的に待機しています。進捗は保持しています。';
  if (!s.progress.bootstrap_complete)
    return 'メールサーバーの同期位置を確認しています。準備後に指定期間のメール探索と AI 解析が順次始まります。表示が0件でも、メールがないことを意味しません。';
  if (!s.progress.historical_complete)
    return '指定期間のメールを順次探しています。探索が終わるまで総件数は確定しません。';
  return '指定期間の探索は完了しました。解析結果は確認待ちに追加されます。';
}
