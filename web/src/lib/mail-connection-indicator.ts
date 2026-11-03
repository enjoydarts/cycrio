import type { MailAccount } from './mail-api.ts';
import type { OverviewConnection } from './overview-api.ts';
import { automationAIWaiting } from './mail-automation-progress.ts';
import {
  processingBlocked,
  processingReasons,
} from './mail-processing-text.ts';

type Indicator = {
  tone: 'neutral' | 'ready' | 'running' | 'warning' | 'error';
  label: string;
};

export function mailConnectionIndicator(
  account: Pick<MailAccount, 'state' | 'last_error'>,
  connection: OverviewConnection | null | undefined,
): Indicator {
  const details: string[] = [];
  function timestamp(label: string, value: string | null | undefined) {
    if (!value || !Number.isFinite(Date.parse(value))) return;
    details.push(
      `${label}: ${new Intl.DateTimeFormat('ja-JP', {
        timeZone: 'Asia/Tokyo',
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hourCycle: 'h23',
      }).format(new Date(value))}（日本時間）`,
    );
  }
  timestamp('最終成功', connection?.last_success_at);
  timestamp('最終実行開始', connection?.last_started_at);
  if (connection?.automation_state === 'enabled') {
    if (connection.ready && !connection.retry_at)
      timestamp('次回確認', connection.next_check_at);
    timestamp('再試行予定', connection.retry_at);
  }
  const reasons = [
    ...new Set(
      [
        connection?.automation_reason ?? '',
        ...(connection?.blocked_reasons ?? []),
      ].filter(Boolean),
    ),
  ];
  const labels: Record<string, string> = {
    ...processingBlocked,
    ...processingReasons,
    configuration_changed: 'AI の送信条件が変わりました。',
    account_changed: 'メール接続条件が変わりました。',
    uid_validity_changed: 'メールボックスの識別情報が変わりました。',
    owner_stopped: '利用者が取り込みを停止しました。',
  };
  for (const reason of reasons)
    details.push(labels[reason] ?? '取り込み条件の確認が必要です。');
  const result = (tone: Indicator['tone'], label: string): Indicator => ({
    tone,
    label: [label, ...details].join('。'),
  });
  if (account.state === 'needs_reauth' || connection?.state === 'needs_reauth')
    return result('error', 'メールの再認証が必要');
  if (account.state === 'disconnected' || connection?.state === 'disconnected')
    return result('neutral', 'メール接続は切断済み');
  if (account.last_error) {
    details.unshift(
      labels[account.last_error] ?? 'メールの接続確認が必要です。',
    );
    return result('error', 'メール接続エラー');
  }
  if (!connection) return result('neutral', '自動取り込みの状態は未確認');
  if (connection.automation_state === 'stopped') {
    const failed = reasons.some((reason) =>
      [
        'tls_failed',
        'authentication_failed',
        'credentials_unavailable',
        'connection_failed',
        'timeout',
        'invalid_response',
        'response_too_large',
        'folder_limit',
        'lease_expired',
      ].includes(reason),
    );
    return result(failed ? 'error' : 'neutral', '自動取り込みは停止中');
  }
  if (connection.automation_state === 'not_configured')
    return result('neutral', '自動取り込みは未設定');
  if (connection.automation_state === 'needs_confirmation')
    return result('warning', '取り込み条件の再確認が必要');
  if (connection.retry_at) return result('warning', 'メール接続の再試行待ち');
  if (
    automationAIWaiting({
      state: connection.automation_state,
      ready: connection.ready,
      reason: connection.automation_reason,
      blocked_reasons: connection.blocked_reasons,
    })
  )
    return result('warning', '管理者のAI設定待ち');
  if (
    account.state !== 'ready' ||
    connection.state !== 'ready' ||
    !connection.ready ||
    reasons.length
  )
    return result('warning', '取り込みの準備確認が必要');
  if (connection.automation_running) return result('running', 'メール確認中');
  return result('ready', '自動取り込み有効（次の確認待ち）');
}
