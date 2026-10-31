import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type { SubscriptionsAPI } from './subscriptions-api.ts';
export type DeleteView = {
  busy: boolean;
  uncertain: boolean;
  locked: boolean;
  error: ErrorCode | null;
};
export function createSubscriptionDelete(
  api: Pick<SubscriptionsAPI, 'delete'>,
  token: TokenProvider,
  id: string,
  version: number,
  changed: (view: DeleteView) => void,
  done: () => void,
  denied: () => void,
) {
  let view: DeleteView = {
    busy: false,
    uncertain: false,
    locked: false,
    error: null,
  };
  let finished = false;
  const body = Object.freeze({ expected_version: version, confirmed: true });
  const publish = (patch: Partial<DeleteView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  return {
    state: () => view,
    async submit() {
      if (view.busy || finished || (view.error && !view.uncertain)) return;
      publish({ busy: true, locked: true, error: null });
      try {
        await api.delete(id, body, token);
      } catch (e) {
        const error = e instanceof ApiError ? e.code : 'dependency_unavailable';
        const uncertain = error === 'dependency_unavailable';
        publish({ busy: false, locked: uncertain, uncertain, error });
        if (error === 'authentication_required' || error === 'access_denied')
          denied();
        return;
      }
      finished = true;
      publish({ busy: false, locked: false, uncertain: false, error: null });
      done();
    },
  };
}
