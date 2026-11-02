import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type { Candidate, createApprovalAPI } from './approval-api.ts';
import type {
  CandidateSource,
  CandidateSources,
} from './candidate-selection.ts';
type API = Pick<ReturnType<typeof createApprovalAPI>, 'related' | 'dismiss'>;
export type CandidateBulkDismissState = {
  status:
    | 'idle'
    | 'loading'
    | 'ready'
    | 'saving'
    | 'uncertain'
    | 'success'
    | 'error'
    | 'denied'
    | 'disposed';
  total: number;
  completed: number;
  groups: number;
  error: ErrorCode | null;
};
export function createCandidateBulkDismiss(
  api: API,
  token: TokenProvider,
  onDenied: () => void,
  publish: (state: CandidateBulkDismissState) => void = () => {},
) {
  let state: CandidateBulkDismissState = {
    status: 'idle',
    total: 0,
    completed: 0,
    groups: 0,
    error: null,
  };
  let abort: AbortController | undefined,
    disposed = false;
  let batches: CandidateSources[] = [];
  let cursor = 0;
  const key = (r: CandidateSource) => `${r.result_id}/${r.event_index}`;
  const emit = (patch: Partial<CandidateBulkDismissState>) => {
    state = { ...state, ...patch };
    publish({ ...state });
  };
  const blocked = () =>
    disposed ||
    state.status === 'saving' ||
    state.status === 'uncertain' ||
    state.status === 'denied';
  const fail = (e: unknown, saving: boolean) => {
    const code = e instanceof ApiError ? e.code : 'dependency_unavailable';
    if (code === 'authentication_required' || code === 'access_denied') {
      batches = [];
      cursor = 0;
      emit({
        status: 'denied',
        error: code,
        total: 0,
        completed: 0,
        groups: 0,
      });
      onDenied();
    } else
      emit({
        status:
          saving && code === 'dependency_unavailable' ? 'uncertain' : 'error',
        error: code,
      });
  };
  return {
    state: () => ({ ...state }),
    async prepare(rows: Candidate[]) {
      if (blocked()) return;
      abort?.abort();
      const current = new AbortController();
      abort = current;
      batches = [];
      cursor = 0;
      emit({
        status: 'loading',
        groups: rows.length,
        total: 0,
        completed: 0,
        error: null,
      });
      try {
        if (!rows.length) throw new ApiError('invalid_request');
        const sources = new Map<string, CandidateSource>();
        const anchors = rows.map(
          ({ result_id, event_index, related_count }) => ({
            result_id,
            event_index,
            related_count,
          }),
        );
        for (const anchor of anchors) {
          if ((anchor.related_count ?? 1) <= 1) {
            sources.set(key(anchor), {
              result_id: anchor.result_id,
              event_index: anchor.event_index,
            });
            continue;
          }
          let offset = 0,
            total: number | undefined;
          const group = new Map<string, CandidateSource>();
          while (true) {
            const page = await api.related(
              anchor,
              100,
              offset,
              token,
              current.signal,
            );
            if (disposed || current.signal.aborted) return;
            if (
              page.offset !== offset ||
              page.limit < 1 ||
              page.total < 1 ||
              (total !== undefined && total !== page.total)
            )
              throw new ApiError('conflict');
            total = page.total;
            for (const row of page.candidates)
              group.set(key(row), {
                result_id: row.result_id,
                event_index: row.event_index,
              });
            if (offset + page.limit >= total) break;
            if (!page.candidates.length) throw new ApiError('conflict');
            offset += page.limit;
          }
          if (group.size !== total || !group.has(key(anchor)))
            throw new ApiError('conflict');
          for (const [id, source] of group) sources.set(id, source);
        }
        if (disposed || current.signal.aborted) return;
        const refs = [...sources.values()];
        for (let offset = 0; offset < refs.length; offset += 100) {
          const [primary, ...extra] = refs.slice(offset, offset + 100);
          batches.push({
            ...primary,
            ...(extra.length ? { additional_sources: extra } : {}),
          });
        }
        emit({ status: 'ready', total: refs.length });
      } catch (e) {
        if (!disposed && !current.signal.aborted) fail(e, false);
      }
    },
    async submit() {
      if (disposed || !['ready', 'uncertain'].includes(state.status)) return;
      abort = new AbortController();
      const current = abort;
      emit({ status: 'saving', error: null });
      try {
        while (cursor < batches.length) {
          const body = structuredClone(batches[cursor]);
          await api.dismiss(body, token, current.signal);
          if (disposed || current.signal.aborted) return;
          cursor++;
          emit({
            completed:
              state.completed + 1 + (body.additional_sources?.length ?? 0),
          });
        }
        emit({ status: 'success' });
      } catch (e) {
        if (!disposed && !current.signal.aborted) fail(e, true);
      }
    },
    dispose() {
      disposed = true;
      abort?.abort();
      batches = [];
      emit({
        status: 'disposed',
        total: 0,
        completed: 0,
        groups: 0,
        error: null,
      });
    },
  };
}
