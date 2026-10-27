import type { Role } from './api.ts';
export const settingsSections = [
  { id: 'mail', title: 'メール連携', admin: false },
  { id: 'appearance', title: '表示', admin: false },
  { id: 'ai', title: 'AI の準備・送信許可', admin: true },
  { id: 'ai-models', title: 'AI モデル', admin: true, aliasFor: 'ai' },
  { id: 'ai-runtime', title: 'AI 実行・接続', admin: true, aliasFor: 'ai' },
  {
    id: 'mail-policy',
    title: 'メール解析の送信条件',
    admin: true,
    aliasFor: 'ai',
  },
  { id: 'credentials', title: 'API キー', admin: true },
  { id: 'integrations', title: 'メール送信・外部イベント', admin: true },
  { id: 'catalog', title: 'モデルカタログ', admin: true },
  { id: 'usage', title: 'AI 利用原価', admin: true },
] as const;
export const availableSettings = (role: Role) =>
  settingsSections.filter(
    (section) => !section.admin || role === 'platform_admin',
  );
export function settingsNavigationGroups(role: Role) {
  const items = availableSettings(role).filter((item) => !('aliasFor' in item));
  return [
    {
      id: 'personal',
      title: '個人設定',
      items: items.filter((item) => !item.admin),
    },
    {
      id: 'admin',
      title: '管理者設定',
      items: items.filter((item) => item.admin),
    },
  ].filter((group) => group.items.length > 0);
}
export function settingsNavigationSection(id: string) {
  const section = settingsSections.find((item) => item.id === id);
  return section && 'aliasFor' in section ? section.aliasFor : id;
}
export function settingsAccess(
  id: string,
  role: Role,
): 'allowed' | 'denied' | 'not_found' {
  const section = settingsSections.find((item) => item.id === id);
  if (!section) return 'not_found';
  return section.admin && role !== 'platform_admin' ? 'denied' : 'allowed';
}
