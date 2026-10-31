import type { TokenProvider } from './api.ts';
import type { MailAccount, MailAPI } from './mail-api.ts';
import type {
  MailProcessingAPI,
  ProcessingPreview,
} from './mail-processing-api.ts';
import type {
  MailAutomationAPI,
  MailAutomationState,
} from './mail-automation-api.ts';
import { createFeed } from './daily-controller.ts';
import { processingAction, setupReadiness } from './setup-readiness.ts';

export type GettingStartedPage = {
  accounts: MailAccount[];
  selected: MailAccount | null;
  preview: ProcessingPreview | null;
  automation: MailAutomationState | null;
};
export type GettingStartedPorts = {
  mail: Pick<MailAPI, 'list'>;
  processing: Pick<MailProcessingAPI, 'preview'>;
  automation: Pick<MailAutomationAPI, 'read'>;
};
export function createGettingStartedController(
  api: GettingStartedPorts,
  token: TokenProvider,
  changed: Parameters<typeof createFeed<GettingStartedPage, string>>[2],
  onDenied: () => void,
) {
  return createFeed<GettingStartedPage, string>(
    async (accountID, token, signal) => {
      const accounts = await api.mail.list(token, signal);
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      const selected =
        accounts.find((a) => a.id === accountID) ??
        accounts.find(
          (a) =>
            a.state === 'ready' &&
            a.folders.some((f) => f.selected && f.available && f.selectable),
        ) ??
        accounts[0] ??
        null;
      if (!selected)
        return { accounts, selected: null, preview: null, automation: null };
      const [preview, automation] = await Promise.all([
        api.processing.preview(selected.id, token, signal),
        api.automation.read(selected.id, token, signal),
      ]);
      return { accounts, selected, preview, automation };
    },
    token,
    changed,
    onDenied,
  );
}

export type GettingStartedGuide = {
  stage: string;
  title: string;
  description: string;
  label: string;
  href: string | null;
};
export function gettingStartedGuide(
  page: GettingStartedPage,
  admin: boolean,
): GettingStartedGuide {
  const account = page.selected,
    preview = page.preview,
    automation = page.automation;
  if (!account)
    return {
      stage: 'connect',
      title: 'メールを連携して始めましょう',
      description:
        '取り込みたいメールを連携し、接続と対象フォルダーを確認します。手動でサブスクを追加することもできます。',
      label: 'メールを連携する',
      href: '/settings/mail',
    };
  if (
    account.state !== 'ready' ||
    !account.folders.some((f) => f.selected && f.available && f.selectable)
  )
    return {
      stage: 'connection',
      title: '接続と対象フォルダーを確認してください',
      description:
        'メールの接続確認を行い、取り込みたいフォルダーを選択してください。',
      label: 'メール連携を確認する',
      href: '/settings/mail',
    };
  const analysis = `/analysis?tab=history&account_id=${encodeURIComponent(account.id)}`;
  if (!preview || !automation)
    return {
      stage: 'changed',
      title: '準備状況を更新してください',
      description:
        '最新の接続条件を確認できません。状態を読み直してから進めてください。',
      label: 'メール解析で確認する',
      href: analysis,
    };
  if (
    !preview.full_email_allowed ||
    (!preview.ready && preview.blocked_reason !== 'processing_active')
  ) {
    const reason = !preview.full_email_allowed
      ? 'full_email_not_allowed'
      : preview.blocked_reason;
    const action = processingAction(reason, admin);
    const readiness = setupReadiness({
      loaded: true,
      accounts: page.accounts.length,
      preview,
      admin,
    });
    return {
      stage: 'preparation',
      title: !preview.full_email_allowed
        ? '本文の送信許可が必要です'
        : readiness.title,
      description:
        action?.href === '/settings/mail'
          ? 'メールの接続と対象フォルダーの条件を確認してください。最新状態を読み直してから取り込みへ進めます。'
          : admin
            ? 'AIの準備と本文の送信条件を確認してください。設定を整えた後、取り込みを開始できます。'
            : '管理者によるAIの準備・送信条件の確認が必要です。準備が整ってから状態を更新してください。',
      label: action?.label ?? '設定を確認する',
      href: action ? action.href : '/settings',
    };
  }
  if (preview.blocked_reason === 'processing_active')
    return {
      stage: 'processing',
      title: 'メールを解析しています',
      description: 'このメール接続の取り込み履歴で進行状況を確認できます。',
      label: '進行中の処理を見る',
      href: analysis,
    };
  if (
    automation.basis.account_version !== account.version ||
    automation.basis.account_version !== preview.expected_version ||
    automation.basis.runtime_version !== preview.runtime_version ||
    automation.basis.profile_id !== (preview.profile_id ?? '') ||
    automation.basis.policy_version !== preview.policy_version
  )
    return {
      stage: 'changed',
      title: '準備の条件が変わりました',
      description:
        '最新状態を読み直してください。取り込みの条件はメール解析画面でも確認できます。',
      label: '取り込み条件を確認する',
      href: analysis,
    };
  if (automation.state === 'stopped')
    return {
      stage: 'stopped',
      title: '自動取り込みは停止中です',
      description:
        '再開する場合は、これまでの進捗と取り込み条件を確認してください。',
      label: '停止状態と再開条件を見る',
      href: analysis,
    };
  if (
    automation.state === 'needs_confirmation' ||
    !automation.preparation_ready ||
    (automation.state === 'enabled' && !automation.ready)
  )
    return {
      stage: 'confirmation',
      title: '取り込み条件の確認が必要です',
      description:
        '接続や送信条件に変更があります。メール解析画面で内容を確認してください。',
      label: '取り込み条件を確認する',
      href: analysis,
    };
  if (automation.state === 'not_configured')
    return {
      stage: 'start',
      title: '取り込みを始める準備ができています',
      description:
        '対象期間と本文の送信条件を確認して、メール解析画面から開始できます。',
      label: '取り込み条件を確認する',
      href: analysis,
    };
  if (automation.progress.failed_count || automation.progress.unknown_count)
    return {
      stage: 'attention',
      title: '取り込みの処理失敗があります',
      description:
        '失敗や結果不明の処理があります。結果不明の再試行では追加料金が発生する場合があるため、内容を確認してください。',
      label: '取り込み履歴を見る',
      href: analysis,
    };
  if (automation.discovery_failure_count || automation.retry_at)
    return {
      stage: 'retrying',
      title: 'メールの確認を再試行しています',
      description:
        '最後の確認結果と次の試行予定を、メール解析画面で確認できます。',
      label: '取り込み状況を見る',
      href: analysis,
    };
  if (
    automation.progress.historical_complete &&
    !automation.progress.pending_count
  )
    return {
      stage: 'explored',
      title: '初回期間のメール探索は完了しました',
      description:
        '解析済みの結果とサブスクへの登録状況を確認してください。メール探索の完了と、契約の登録完了は別の状態です。',
      label: '解析結果と登録状況を見る',
      href: analysis,
    };
  return {
    stage: 'importing',
    title: 'メールを取り込み・解析しています',
    description:
      '初回期間の探索と解析は順次進みます。解析結果と登録の進み具合を確認できます。',
    label: '取り込み状況を見る',
    href: analysis,
  };
}
