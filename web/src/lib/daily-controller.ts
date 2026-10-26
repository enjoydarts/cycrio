import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
export type FeedView<T> = {
  page: T | null;
  status: 'idle' | 'loading' | 'ready' | 'error' | 'denied' | 'disposed';
  error: ErrorCode | null;
};
export function createFeed<T, F>(
  read: (filter: F, token: TokenProvider, signal?: AbortSignal) => Promise<T>,
  token: TokenProvider,
  changed: (v: FeedView<T>) => void,
  onDenied: () => void,
) {
  let view: FeedView<T> = { page: null, status: 'idle', error: null },
    generation = 0,
    abort: AbortController | undefined,
    disposed = false;
  const publish = (patch: Partial<FeedView<T>>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  return {
    state: () => view,
    async load(filter: F) {
      if (disposed || view.status === 'denied') return;
      abort?.abort();
      abort = new AbortController();
      const seq = ++generation;
      publish({ status: 'loading', error: null });
      try {
        const page = await read(filter, token, abort.signal);
        if (!disposed && seq === generation) publish({ page, status: 'ready' });
      } catch (error) {
        if (disposed || seq !== generation) return;
        const code =
          error instanceof ApiError ? error.code : 'dependency_unavailable';
        if (['authentication_required', 'access_denied'].includes(code)) {
          abort.abort();
          publish({ page: null, status: 'denied', error: code });
          onDenied();
        } else publish({ status: 'error', error: code });
      }
    },
    dispose() {
      disposed = true;
      generation++;
      abort?.abort();
      publish({ page: null, status: 'disposed', error: null });
    },
  };
}
