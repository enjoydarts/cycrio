import type { TokenProvider } from './api.ts';
import type {
  MailAutomationAPI,
  MailAutomationState,
} from './mail-automation-api.ts';
import { createFeed, type FeedView } from './daily-controller.ts';
export type ImportAccount = { id: string; label: string };
export type ImportEntry = { label: string; state: MailAutomationState };
export function createImportHistory(
  api: Pick<MailAutomationAPI, 'read'>,
  token: TokenProvider,
  changed: (view: FeedView<ImportEntry[]>) => void,
  onDenied: () => void,
) {
  return createFeed(
    async (accounts: ImportAccount[], token, signal) => {
      const entries = await Promise.all(
        accounts.map(async (account) => ({
          label: account.label,
          state: await api.read(account.id, token, signal),
        })),
      );
      return entries.filter((entry) => entry.state.id !== '');
    },
    token,
    changed,
    onDenied,
  );
}
