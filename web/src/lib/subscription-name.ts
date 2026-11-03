import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type { Subscription, SubscriptionsAPI } from './subscriptions-api.ts';
import { normalizeDisplayName } from './subscription-title.ts';

export type NameView = {
  name: string;
  busy: boolean;
  uncertain: boolean;
  conflict: boolean;
  denied: boolean;
  error: ErrorCode | null;
};

export function createSubscriptionName(
  api: Pick<SubscriptionsAPI, 'rename' | 'get'>,
  token: TokenProvider,
  initial: Subscription,
  changed: (view: NameView) => void,
  done: (saved: Subscription) => void,
  denied: () => void,
) {
  let view: NameView = {
    name: initial.display_name ?? '',
    busy: false,
    uncertain: false,
    conflict: false,
    denied: false,
    error: null,
  };
  let disposed = false;
  let pending: {
    display_name: string | null;
    expected_version: number;
    confirmed: true;
  } | null = null;
  const publish = (patch: Partial<NameView>) => {
    if (disposed) return;
    view = { ...view, ...patch };
    changed(view);
  };
  const failure = (e: unknown) => {
    const error = e instanceof ApiError ? e.code : 'dependency_unavailable';
    const lost =
      error === 'authentication_required' || error === 'access_denied';
    publish({
      busy: false,
      uncertain: error === 'dependency_unavailable',
      conflict: error === 'conflict',
      denied: lost,
      error,
      ...(lost ? { name: '' } : {}),
    });
    if (lost && !disposed) {
      pending = null;
      denied();
    }
  };
  const success = (saved: Subscription) => {
    if (disposed) return;
    pending = null;
    publish({ busy: false, uncertain: false, error: null });
    done(saved);
  };
  const submit = async () => {
    if (!pending) return;
    publish({ busy: true, error: null });
    try {
      success(await api.rename(initial.id, pending, token));
    } catch (e) {
      failure(e);
    }
  };
  return {
    state: () => view,
    edit(name: string) {
      if (
        !disposed &&
        !view.busy &&
        !view.uncertain &&
        !view.denied &&
        !view.conflict
      )
        publish({ name, error: null });
    },
    async save() {
      if (
        disposed ||
        view.busy ||
        view.uncertain ||
        view.conflict ||
        view.denied
      )
        return;
      try {
        pending = {
          display_name: normalizeDisplayName(view.name),
          expected_version: initial.version,
          confirmed: true,
        };
      } catch {
        publish({ error: 'invalid_request' });
        return;
      }
      await submit();
    },
    async reconcile() {
      if (disposed || view.busy || !view.uncertain || !pending) return;
      publish({ busy: true, error: null });
      try {
        const latest = await api.get(initial.id, token);
        if (disposed) return;
        if (
          latest.version > pending.expected_version &&
          (latest.display_name ?? null) === pending.display_name
        )
          success(latest);
        else if (latest.version === pending.expected_version) await submit();
        else failure(new ApiError('conflict'));
      } catch (e) {
        failure(e);
      }
    },
    dispose() {
      disposed = true;
      pending = null;
    },
  };
}
