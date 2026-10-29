import type { ProcessingPreview } from './mail-processing-api.ts';
export function safeAnalysisReturn(value: string | null | undefined): string {
  if (
    !value ||
    !value.startsWith('/') ||
    value.startsWith('//') ||
    value.includes('\\') ||
    value.includes('#') ||
    value.includes('..') ||
    value.length > 3000
  )
    return '/analysis';
  try {
    const u = new URL(value, 'https://cycrio.invalid');
    if (
      u.origin !== 'https://cycrio.invalid' ||
      !['/analysis', '/settings/mail', '/import'].includes(u.pathname)
    )
      return '/analysis';
    if (u.pathname === '/import') {
      for (const [key, value] of u.searchParams) {
        if (
          !['account_id', 'setup_account', 'connect'].includes(key) ||
          u.searchParams.getAll(key).length !== 1
        )
          return '/analysis';
        if (
          key === 'connect'
            ? value !== 'true'
            : !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
                value,
              ) || value === '00000000-0000-0000-0000-000000000000'
        )
          return '/analysis';
      }
      return u.pathname + u.search;
    }
    for (const [key] of u.searchParams)
      if (
        ![
          'state',
          'account_id',
          'account',
          'search',
          'offset',
          'tab',
          'history',
          'filter',
          'start',
          'max_messages',
          'setup_account',
          'setup_max',
        ].includes(key)
      )
        return '/analysis';
    return u.pathname + u.search;
  } catch {
    return '/analysis';
  }
}
export function processingAction(
  reason: ProcessingPreview['blocked_reason'],
  admin: boolean,
): { label: string; href: string | null } | null {
  switch (reason) {
    case 'account_not_ready':
    case 'credentials_required':
    case 'folders_required':
      return { label: 'メール連携を確認', href: '/settings/mail' };
    case 'policy_disabled':
    case 'full_email_not_allowed':
    case 'policy_reconfirmation_required':
      return {
        label: admin
          ? '送信条件を確認して許可する'
          : '管理者による送信許可をお待ちください',
        href: admin ? '/settings/ai?stage=permission' : null,
      };
    case 'runtime_inactive':
    case 'configuration_unavailable':
      return {
        label: admin
          ? 'AI の準備を進める'
          : '管理者による AI の準備をお待ちください',
        href: admin ? '/settings/ai' : null,
      };
    case 'processing_active':
      return { label: '進行中の解析が完了するまでお待ちください', href: null };
    default:
      return null;
  }
}
export function setupReadiness(input: {
  loaded: boolean;
  accounts: number;
  preview: ProcessingPreview | null;
  admin: boolean;
  error?: boolean;
}) {
  if (input.error)
    return {
      stage: 'error',
      title: '準備状況を確認できません',
      label: 'もう一度確認',
      href: null,
    };
  if (!input.loaded)
    return {
      stage: 'loading',
      title: '準備状況を確認中',
      label: '',
      href: null,
    };
  if (!input.accounts)
    return {
      stage: 'mail',
      title: 'メールを連携してください',
      label: 'メールを連携する',
      href: '/settings/mail',
    };
  if (input.preview?.ready && input.preview.full_email_allowed)
    return {
      stage: 'ready',
      title: '解析を始められます',
      label: 'メールを解析する',
      href: '/analysis',
    };
  const effectiveReason =
    input.preview?.ready && !input.preview.full_email_allowed
      ? 'full_email_not_allowed'
      : (input.preview?.blocked_reason ?? 'configuration_unavailable');
  const action = processingAction(effectiveReason, input.admin);
  return {
    stage:
      effectiveReason === 'full_email_not_allowed' ||
      input.preview?.blocked_reason?.startsWith('policy_')
        ? 'permission'
        : input.preview?.blocked_reason === 'runtime_inactive' ||
            input.preview?.blocked_reason === 'configuration_unavailable'
          ? 'ai'
          : 'mail',
    title:
      effectiveReason === 'full_email_not_allowed' ||
      input.preview?.blocked_reason?.startsWith('policy_')
        ? '本文送信の許可が必要です'
        : input.preview?.blocked_reason === 'runtime_inactive' ||
            input.preview?.blocked_reason === 'configuration_unavailable'
          ? 'AI の準備が必要です'
          : 'メールの接続条件を確認してください',
    label: action?.label ?? 'メール連携を確認',
    href: action?.href ?? null,
  };
}
export function analysisSetupLink(
  href: string,
  returnTo: string,
  account: string,
  maximum: string,
): string {
  const target = new URL(href, 'https://cycrio.invalid');
  const back = new URL(safeAnalysisReturn(returnTo), 'https://cycrio.invalid');
  if (
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
      account,
    ) &&
    account !== '00000000-0000-0000-0000-000000000000'
  )
    back.searchParams.set('setup_account', account);
  if (/^(?:[1-9]\d?|100)$/.test(maximum))
    back.searchParams.set('setup_max', maximum);
  back.searchParams.set('start', 'true');
  target.searchParams.set('return_to', back.pathname + back.search);
  return target.pathname + target.search;
}
export function automationSetupLink(
  href: string,
  returnTo: string,
  account: string,
): string {
  const target = new URL(href, 'https://cycrio.invalid');
  const back = new URL(safeAnalysisReturn(returnTo), 'https://cycrio.invalid');
  for (const key of ['start', 'setup_max', 'max_messages'])
    back.searchParams.delete(key);
  if (
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(
      account,
    ) &&
    account !== '00000000-0000-0000-0000-000000000000'
  ) {
    back.searchParams.set('setup_account', account);
    back.searchParams.set('account_id', account);
  }
  target.searchParams.set('return_to', back.pathname + back.search);
  return target.pathname + target.search;
}
