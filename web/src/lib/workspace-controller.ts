import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type { MailAPI } from './mail-api.ts';
import type { createOverviewAPI } from './overview-api.ts';
import type { WorkspaceView } from './workspace-context.ts';

type WorkspacePolling = {
  isVisible(): boolean;
  every(callback: () => void, milliseconds: number): () => void;
  onWake(callback: () => void): () => void;
};

export function emptyWorkspace(): WorkspaceView {
  return {
    accounts: { value: null, status: 'idle', error: null },
    overview: { value: null, status: 'idle', error: null },
  };
}
export function createWorkspaceController(
  mail: Pick<MailAPI, 'list'>,
  overview: Pick<ReturnType<typeof createOverviewAPI>, 'read'>,
  token: TokenProvider,
  changed: (view: WorkspaceView) => void,
  onDenied: () => void,
) {
  let view = emptyWorkspace();
  let generation = 0;
  let disposed = false;
  let denied = false;
  let abort: AbortController | undefined;
  let stopPolling = () => {};
  function publish(next: WorkspaceView) {
    view = next;
    changed(view);
  }
  return {
    state: () => view,
    startPolling(environment: WorkspacePolling) {
      stopPolling();
      if (disposed || denied) return;
      let refreshing = false;
      const refresh = () => {
        if (disposed || denied || refreshing || !environment.isVisible())
          return;
        refreshing = true;
        void this.refresh({ background: true }).finally(() => {
          refreshing = false;
        });
      };
      const stopInterval = environment.every(refresh, 15_000);
      const stopWake = environment.onWake(refresh);
      stopPolling = () => {
        stopInterval();
        stopWake();
      };
    },
    async refresh(options: { background?: boolean } = {}) {
      if (disposed || denied) return;
      abort?.abort();
      abort = new AbortController();
      const signal = abort.signal;
      const seq = ++generation;
      if (!options.background)
        publish({
          accounts: { value: null, status: 'loading', error: null },
          overview: { value: null, status: 'loading', error: null },
        });
      const current = () => !disposed && !denied && generation === seq;
      function failure(key: keyof WorkspaceView, error: unknown) {
        if (!current()) return;
        const code: ErrorCode =
          error instanceof ApiError ? error.code : 'dependency_unavailable';
        if (code === 'authentication_required' || code === 'access_denied') {
          denied = true;
          stopPolling();
          generation++;
          abort?.abort();
          publish({
            accounts: { value: null, status: 'denied', error: code },
            overview: { value: null, status: 'denied', error: code },
          });
          onDenied();
        } else {
          publish({
            ...view,
            [key]: { value: null, status: 'error', error: code },
          });
        }
      }
      await Promise.all([
        mail
          .list(token, signal)
          .then((value) => {
            if (current())
              publish({
                ...view,
                accounts: { value, status: 'ready', error: null },
              });
          })
          .catch((error) => failure('accounts', error)),
        overview
          .read(token, signal)
          .then((value) => {
            if (current())
              publish({
                ...view,
                overview: { value, status: 'ready', error: null },
              });
          })
          .catch((error) => failure('overview', error)),
      ]);
    },
    dispose() {
      disposed = true;
      stopPolling();
      generation++;
      abort?.abort();
      publish(emptyWorkspace());
    },
  };
}
