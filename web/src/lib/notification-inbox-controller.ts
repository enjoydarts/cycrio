import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type {
  NotificationFilter,
  NotificationPage,
  NotificationsAPI,
} from './notifications-api.ts';
export type NotificationInboxView = {
  page: NotificationPage | null;
  status: 'idle' | 'loading' | 'ready' | 'error' | 'denied';
  reading: string | null;
  error: ErrorCode | null;
};
export function createNotificationInbox(
  api: Pick<NotificationsAPI, 'list' | 'read'>,
  token: TokenProvider,
  changed: (view: NotificationInboxView) => void,
  denied: () => void,
  unread: (count: number) => void,
) {
  let view: NotificationInboxView = {
      page: null,
      status: 'idle',
      reading: null,
      error: null,
    },
    filter: NotificationFilter = { read: 'unread', limit: 20, offset: 0 },
    sequence = 0,
    disposed = false,
    abort: AbortController | undefined,
    readAbort: AbortController | undefined;
  const publish = (patch: Partial<NotificationInboxView>) => {
    if (!disposed) {
      view = { ...view, ...patch };
      changed(view);
    }
  };
  const failure = (error: unknown) => {
    const code =
        error instanceof ApiError ? error.code : 'dependency_unavailable',
      lost = code === 'access_denied' || code === 'authentication_required';
    publish({
      status: lost ? 'denied' : 'error',
      error: code,
      reading: null,
      ...(lost ? { page: null } : {}),
    });
    if (lost && !disposed) denied();
  };
  async function load(next: NotificationFilter) {
    if (disposed || view.status === 'denied' || view.reading) return;
    filter = { ...next };
    const current = ++sequence;
    abort?.abort();
    abort = new AbortController();
    publish({ status: 'loading', error: null });
    try {
      const page = await api.list(filter, token, abort.signal);
      if (disposed || current !== sequence) return;
      publish({ page, status: 'ready' });
      unread(page.unread_count);
    } catch (error) {
      if (!disposed && current === sequence) failure(error);
    }
  }
  return {
    state: () => view,
    load,
    async read(id: string) {
      if (
        disposed ||
        view.reading ||
        view.status !== 'ready' ||
        !view.page?.notifications.some((n) => n.id === id && n.read_at === null)
      )
        return;
      readAbort = new AbortController();
      publish({ reading: id, error: null });
      try {
        await api.read(id, token, readAbort.signal);
        if (disposed) return;
        publish({ reading: null });
        await load(filter);
      } catch (error) {
        if (!disposed) failure(error);
      }
    },
    dispose() {
      disposed = true;
      sequence++;
      abort?.abort();
      readAbort?.abort();
    },
  };
}
