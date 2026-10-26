import type { Role } from './api.ts';
export const settingsSections = [
  { id: 'mail', title: 'メール連携', admin: false },
  { id: 'appearance', title: '表示', admin: false },
  { id: 'ai-models', title: 'AI モデル', admin: true },
  { id: 'ai-runtime', title: 'AI 実行・接続', admin: true },
  { id: 'mail-policy', title: 'メール解析の送信条件', admin: true },
  { id: 'credentials', title: 'API キー', admin: true },
  { id: 'catalog', title: 'モデルカタログ', admin: true },
  { id: 'usage', title: 'AI 利用原価', admin: true },
] as const;
export const availableSettings = (role: Role) =>
  settingsSections.filter(
    (section) => !section.admin || role === 'platform_admin',
  );
export function settingsAccess(
  id: string,
  role: Role,
): 'allowed' | 'denied' | 'not_found' {
  const section = settingsSections.find((item) => item.id === id);
  if (!section) return 'not_found';
  return section.admin && role !== 'platform_admin' ? 'denied' : 'allowed';
}
