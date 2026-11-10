import { createNotificationSettings } from './notification-settings-controller.ts';
import {
  notificationKinds,
  type NotificationKind,
  type NotificationPreferences,
  type NotificationsAPI,
} from './notifications-api.ts';
import type { TokenProvider } from './api.ts';
import type { PushController, PushView } from './push-controller.ts';
import type { PushBrowser } from './push-browser.ts';

export function invitationPreferences(
  prior: NotificationPreferences,
): NotificationPreferences {
  const next = structuredClone(prior);
  next.push_opt_in = true;
  if (!notificationKinds.some((kind) => next.kinds[kind].push)) {
    next.kinds.renewal.push = true;
    next.kinds.pending_review.push = true;
  }
  return next;
}

export type PushInvitationView = {
  open: boolean;
  stage: 'offer' | 'home' | 'setup';
  busy: boolean;
  error: boolean;
  deviceReady: boolean;
  kinds: NotificationKind[];
  push: PushView | null;
};

export function createPushInvitation(options: {
  owner: string;
  token: TokenProvider;
  pathname: string;
  storage: Pick<Storage, 'getItem' | 'setItem'>;
  browser: Pick<PushBrowser, 'availability' | 'currentPermission'>;
  push: Pick<PushController, 'subscribe' | 'prepare'>;
  notifications: Pick<NotificationsAPI, 'settings' | 'updateSettings'>;
  denied: () => void;
  blocked?: () => boolean;
}) {
  let disposed = false,
    loading = false,
    dismissed = false;
  let pathname = options.pathname;
  let view: PushInvitationView = {
    open: false,
    stage: 'offer',
    busy: false,
    error: false,
    deviceReady: false,
    kinds: [],
    push: null,
  };
  const listeners = new Set<(view: PushInvitationView) => void>();
  function publish(patch: Partial<PushInvitationView>) {
    if (disposed) return;
    view = { ...view, ...patch };
    listeners.forEach((fn) => fn(view));
  }
  const key = () =>
    'cycrio.push-invitation.v1.' +
    options.owner +
    '.' +
    (options.browser.availability() === 'home_screen' ? 'home' : 'push');
  function seen() {
    try {
      return options.storage.getItem(key()) !== null;
    } catch {
      return true;
    }
  }
  function eligible() {
    return (
      !disposed &&
      !dismissed &&
      !seen() &&
      !pathname.startsWith('/settings') &&
      !options.blocked?.() &&
      view.push?.page?.available &&
      view.push.status === 'idle' &&
      !view.push.busy &&
      !view.push.cleanupPending &&
      options.browser.availability() !== 'unsupported' &&
      options.browser.currentPermission() !== 'denied'
    );
  }
  const settings = createNotificationSettings(
    options.notifications,
    options.token,
    () => {},
    options.denied,
  );
  const unsubscribe = options.push.subscribe((push) => publish({ push }));
  return {
    state: () => view,
    subscribe(fn: (view: PushInvitationView) => void) {
      listeners.add(fn);
      fn(view);
      return () => {
        listeners.delete(fn);
      };
    },
    async offer(nextPath = pathname) {
      pathname = nextPath;
      if (view.open || loading || !eligible()) return;
      loading = true;
      try {
        await settings.load();
        const page = settings.state().page;
        if (!eligible() || !page?.push.available) return;
        const next = invitationPreferences(page.preferences);
        publish({
          open: true,
          kinds: notificationKinds.filter((kind) => next.kinds[kind].push),
        });
      } finally {
        loading = false;
      }
    },
    dismiss() {
      if (view.busy) return;
      try {
        options.storage.setItem(key(), 'later');
      } catch {
        /* 現在の画面では再表示しない。 */
      }
      dismissed = true;
      publish({ open: false });
    },
    synchronize() {
      if (view.open && view.stage === 'offer' && seen()) {
        dismissed = true;
        publish({ open: false });
      }
    },
    async enable() {
      if (disposed || !view.open || view.stage !== 'offer' || view.busy) return;
      try {
        options.storage.setItem(key(), 'accepted');
      } catch {
        publish({ error: true });
        return;
      }
      if (options.browser.availability() === 'home_screen') {
        publish({ stage: 'home' });
        return;
      }
      const prior = settings.state().values;
      if (!prior) return;
      publish({ stage: 'setup', busy: true, error: false });
      try {
        if (
          !prior.push_opt_in ||
          !notificationKinds.some((kind) => prior.kinds[kind].push)
        ) {
          settings.edit(invitationPreferences(prior));
          settings.confirm(true);
          await settings.save();
          if (disposed) return;
          if (!settings.state().saved) {
            publish({ error: true, busy: false });
            return;
          }
        }
        if (disposed) return;
        await options.push.prepare();
        publish({ deviceReady: true, busy: false });
      } catch {
        publish({ error: true, busy: false });
      }
    },
    dispose() {
      disposed = true;
      unsubscribe();
      settings.dispose();
      listeners.clear();
    },
  };
}
