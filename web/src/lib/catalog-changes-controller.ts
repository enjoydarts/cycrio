import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type {
  CatalogChange,
  CatalogChangesAPI,
  CatalogChangesFilter,
} from './catalog-changes-api.ts';
export type CatalogChangesView = {
  status: 'idle' | 'loading' | 'ready' | 'error' | 'denied' | 'disposed';
  changes: CatalogChange[];
  total: number;
  limit: number;
  offset: number;
  filter: CatalogChangesFilter;
  error: ErrorCode | null;
};
export const emptyCatalogChangesView = (): CatalogChangesView => ({
  status: 'idle',
  changes: [],
  total: 0,
  limit: 20,
  offset: 0,
  filter: {},
  error: null,
});
export function createCatalogChangesController(
  api: CatalogChangesAPI,
  token: TokenProvider,
  changed: (view: CatalogChangesView) => void,
  onDenied: () => void,
) {
  let state = emptyCatalogChangesView(),
    generation = 0,
    request = new AbortController();
  const emit = (value: Partial<CatalogChangesView>) => {
    state = { ...state, ...value };
    changed(state);
  };
  return {
    get state() {
      return state;
    },
    async load(filter?: CatalogChangesFilter) {
      if (state.status === 'disposed' || state.status === 'denied') return;
      const id = ++generation;
      request.abort();
      request = new AbortController();
      const selected =
        filter === undefined
          ? state.filter
          : { ...filter, offset: filter.offset ?? 0 };
      emit({
        status: 'loading',
        filter: selected,
        changes: [],
        total: 0,
        offset: selected.offset ?? 0,
        error: null,
      });
      try {
        const page = await api.list(selected, token, request.signal);
        if (id === generation) emit({ ...page, status: 'ready' });
      } catch (error) {
        if (id !== generation) return;
        const code =
          error instanceof ApiError ? error.code : 'dependency_unavailable';
        if (code === 'access_denied' || code === 'authentication_required') {
          emit({ ...emptyCatalogChangesView(), status: 'denied', error: code });
          onDenied();
        } else emit({ status: 'error', error: code });
      }
    },
    dispose() {
      generation++;
      request.abort();
      state = { ...emptyCatalogChangesView(), status: 'disposed' };
    },
  };
}
