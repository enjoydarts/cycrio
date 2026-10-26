import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import { input } from './daily-api.ts';
import {
  parseHistoryFilter,
  type HistoryFilter,
  type ScheduleHistoryAPI,
  type ScheduleHistoryPreview,
} from './schedule-history-api.ts';
export type HistoryEditorView = {
  expanded: boolean;
  filter: HistoryFilter;
  result: ScheduleHistoryPreview | null;
  loading: boolean;
  error: ErrorCode | null;
};
export const initialHistoryView = (currency = ''): HistoryEditorView => ({
  expanded: false,
  filter: { start_date: '', currency },
  result: null,
  loading: false,
  error: null,
});
// This controller owns only the read-only filter/result, never the editing baseline.
export function createScheduleHistoryController(options: {
  id: string;
  api?: ScheduleHistoryAPI;
  token: TokenProvider;
  initial: HistoryEditorView;
  frozen: () => boolean;
  changed: (view: HistoryEditorView) => void;
  observed: (result: ScheduleHistoryPreview) => void;
  denied: (code: ErrorCode) => void;
}) {
  let view = {
      ...options.initial,
      filter: { ...options.initial.filter },
      loading: false,
    },
    generation = 0,
    read: AbortController | null = null,
    disposed = false;
  function publish(patch: Partial<HistoryEditorView>) {
    view = { ...view, ...patch };
    options.changed(view);
  }
  function cancel() {
    generation++;
    read?.abort();
    read = null;
    if (view.loading) publish({ loading: false });
  }
  function purge() {
    cancel();
    publish(initialHistoryView());
  }
  return {
    cancel,
    purge,
    clear() {
      cancel();
      publish({ result: null, error: null });
    },
    toggle(expanded: boolean) {
      if (!options.frozen()) publish({ expanded });
    },
    edit(patch: Partial<HistoryFilter>) {
      if (options.frozen()) return;
      cancel();
      publish({
        filter: { ...view.filter, ...patch },
        result: null,
        error: null,
      });
    },
    async fetch() {
      if (disposed || options.frozen() || !options.api) return;
      let filter: HistoryFilter;
      try {
        filter = input(() => parseHistoryFilter(view.filter));
      } catch {
        publish({ error: 'invalid_request' });
        return;
      }
      cancel();
      const own = generation,
        abort = new AbortController();
      read = abort;
      publish({ loading: true, result: null, error: null });
      const current = () =>
        !disposed &&
        !options.frozen() &&
        own === generation &&
        !abort.signal.aborted;
      try {
        const result = await options.api.preview(
          options.id,
          filter,
          options.token,
          abort.signal,
        );
        if (!current()) return;
        publish({ result, loading: false });
        options.observed(result);
      } catch (error) {
        if (!current()) return;
        const code =
          error instanceof ApiError ? error.code : 'dependency_unavailable';
        if (code === 'authentication_required' || code === 'access_denied') {
          purge();
          options.denied(code);
        } else publish({ loading: false, error: code });
      }
    },
    dispose() {
      cancel();
      disposed = true;
      view = initialHistoryView();
    },
  };
}
