import { getContext, setContext } from 'svelte';
import type { CurrentUser, TokenProvider, createAPI } from './api.ts';
import type { createCatalogAPI } from './catalog-api.ts';
import type { createAIRuntimeAPI } from './airuntime-api.ts';
import type { AIRoutesAPI } from './ai-routes-api.ts';
import type { createMailAPI } from './mail-api.ts';
import type { createMailSyncAPI } from './mail-sync-api.ts';
import type { createMailProcessingAPI } from './mail-processing-api.ts';
import type { ThemePreference } from './theme.ts';
import type { NotificationsAPI } from './notifications-api.ts';
import type { PushController } from './push-controller.ts';

const appSession = Symbol('cycrio-app-session');
export type AppSession = {
  readonly user: CurrentUser | null;
  readonly scope: number;
  readonly token: TokenProvider;
  readonly theme: ThemePreference;
  setTheme: (preference: ThemePreference) => void;
  onDenied: () => void;
  signOut?: () => Promise<void> | void;
  api: ReturnType<typeof createAPI>;
  catalogAPI: ReturnType<typeof createCatalogAPI>;
  runtimeAPI: ReturnType<typeof createAIRuntimeAPI>;
  routesAPI: AIRoutesAPI;
  mailAPI: ReturnType<typeof createMailAPI>;
  mailSyncAPI: ReturnType<typeof createMailSyncAPI>;
  mailProcessingAPI: ReturnType<typeof createMailProcessingAPI>;
  notificationsAPI: NotificationsAPI;
  readonly pushController?: PushController;
};
export function provideAppSession(session: AppSession) {
  setContext(appSession, session);
}
export function getAppSession(): AppSession {
  const session = getContext<AppSession>(appSession);
  if (!session) throw new Error('App session is unavailable');
  return session;
}
