import type { MailAutomationState } from './mail-automation-api.ts';
export function automationLastStart(s: MailAutomationState): string | null {
  if (s.last_started_at !== undefined) return s.last_started_at;
  return (
    s.recent_sync_runs
      .map((run) => run.started_at)
      .filter((x): x is string => !!x)
      .sort()
      .at(-1) ?? null
  );
}
export function automationNextCheck(
  s: MailAutomationState,
  now = Date.now(),
): { at: string | null; label: string; overdue: boolean } {
  if (s.state === 'stopped')
    return { at: null, label: '停止中（予定なし）', overdue: false };
  if (s.state === 'not_configured')
    return { at: null, label: '未設定', overdue: false };
  if (s.state === 'needs_confirmation')
    return { at: null, label: '条件の確認待ち', overdue: false };
  if (!s.ready) return { at: null, label: '設定の準備待ち', overdue: false };
  if (s.running) return { at: null, label: 'メール確認中', overdue: false };
  if (s.retry_at) return { at: s.retry_at, label: '再試行', overdue: false };
  const overdue =
    !!s.next_check_at &&
    s.progress.historical_complete &&
    Date.parse(s.next_check_at) + 60000 < now;
  return {
    at: s.next_check_at,
    label: overdue
      ? '確認予定を過ぎています'
      : s.next_check_at
        ? ''
        : '確認待ち',
    overdue,
  };
}
