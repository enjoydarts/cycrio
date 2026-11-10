import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import {
  notificationPreferencesEqual,
  notificationKinds,
  parseNotificationPreferences,
  type NotificationPreferences,
  type NotificationSettings,
  type NotificationsAPI,
} from './notifications-api.ts';
export type NotificationSettingsView = {
  page: NotificationSettings | null;
  values: NotificationPreferences | null;
  loading: boolean;
  busy: boolean;
  confirmed: boolean;
  uncertain: boolean;
  conflict: boolean;
  saved: boolean;
  error: ErrorCode | null;
};
export function createNotificationSettings(
  api: Pick<NotificationsAPI, 'settings' | 'updateSettings'>,
  token: TokenProvider,
  changed: (view: NotificationSettingsView) => void,
  denied: () => void,
) {
  let view: NotificationSettingsView = {
      page: null,
      values: null,
      loading: false,
      busy: false,
      confirmed: false,
      uncertain: false,
      conflict: false,
      saved: false,
      error: null,
    },
    pending: NotificationPreferences | null = null,
    disposed = false,
    sequence = 0,
    abort: AbortController | undefined;
  const publish = (patch: Partial<NotificationSettingsView>) => {
    if (!disposed) {
      view = { ...view, ...patch };
      changed(view);
    }
  };
  const success = (page: NotificationSettings) => {
    pending = null;
    publish({
      page,
      values: structuredClone(page.preferences),
      busy: false,
      loading: false,
      confirmed: false,
      uncertain: false,
      conflict: false,
      saved: true,
      error: null,
    });
  };
  const failure = (error: unknown, mutating: boolean) => {
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    const lost = code === 'authentication_required' || code === 'access_denied';
    if (lost) pending = null;
    publish({
      loading: false,
      busy: false,
      error: code,
      uncertain: !lost && mutating && code === 'dependency_unavailable',
      conflict: code === 'conflict',
      saved: false,
      ...(lost ? { page: null, values: null, confirmed: false } : {}),
    });
    if (lost && !disposed) denied();
  };
  return {
    state: () => view,
    async load() {
      if (disposed || view.busy || view.uncertain) return;
      const current = ++sequence;
      abort?.abort();
      abort = new AbortController();
      publish({ loading: true, error: null });
      try {
        const page = await api.settings(token, abort.signal);
        if (disposed || current !== sequence) return;
        pending = null;
        publish({
          page,
          values: structuredClone(page.preferences),
          loading: false,
          confirmed: false,
          conflict: false,
          saved: false,
        });
      } catch (error) {
        if (!disposed && current === sequence) failure(error, false);
      }
    },
    edit(patch: Partial<NotificationPreferences>) {
      if (
        !disposed &&
        view.values &&
        !view.busy &&
        !view.uncertain &&
        !view.conflict
      )
        publish({
          values: {
            ...view.values,
            ...patch,
            kinds: Object.fromEntries(
              notificationKinds.map((kind) => {
                const channel = (patch.kinds ?? view.values!.kinds)[kind];
                return [
                  kind,
                  {
                    in_app: channel.in_app,
                    email: patch.email_opt_in === false ? false : channel.email,
                    push: patch.push_opt_in === false ? false : channel.push,
                  },
                ];
              }),
            ) as NotificationPreferences['kinds'],
            version: view.values.version,
          },
          confirmed: false,
          saved: false,
          error: null,
        });
    },
    confirm(confirmed: boolean) {
      if (!disposed && !view.busy && !view.uncertain && !view.conflict)
        publish({ confirmed });
    },
    async save() {
      if (
        disposed ||
        view.busy ||
        view.uncertain ||
        view.conflict ||
        !view.confirmed ||
        !view.values ||
        !view.page
      )
        return;
      try {
        pending = parseNotificationPreferences(view.values);
        for (const channel of ['email', 'push'] as const) {
          const consent = `${channel}_opt_in` as const;
          const prior = view.page.preferences;
          if (
            !view.page[channel].available &&
            pending[consent] &&
            (!prior[consent] ||
              notificationKinds.some(
                (kind) =>
                  pending!.kinds[kind][channel] && !prior.kinds[kind][channel],
              ))
          )
            throw new ApiError('invalid_request');
        }
      } catch {
        pending = null;
        publish({ error: 'invalid_request' });
        return;
      }
      const current = ++sequence;
      abort = new AbortController();
      publish({ busy: true, error: null });
      try {
        const page = await api.updateSettings(
          {
            ...structuredClone(pending),
            expected_version: pending.version,
            confirmed: true,
          },
          token,
          abort.signal,
        );
        if (!disposed && current === sequence) success(page);
      } catch (error) {
        if (!disposed && current === sequence) failure(error, true);
      }
    },
    async reconcile() {
      if (disposed || view.busy || !view.uncertain || !pending) return;
      const current = ++sequence;
      abort = new AbortController();
      publish({ busy: true, error: null });
      try {
        const page = await api.settings(token, abort.signal);
        if (disposed || current !== sequence) return;
        if (
          page.preferences.version === pending.version + 1 &&
          notificationPreferencesEqual(page.preferences, pending)
        )
          success(page);
        else if (page.preferences.version === pending.version)
          publish({ busy: false, uncertain: true });
        else {
          pending = null;
          publish({
            busy: false,
            uncertain: false,
            conflict: true,
            confirmed: false,
            error: 'conflict',
          });
        }
      } catch (error) {
        if (!disposed && current === sequence) failure(error, true);
      }
    },
    dispose() {
      disposed = true;
      sequence++;
      abort?.abort();
      pending = null;
    },
  };
}
