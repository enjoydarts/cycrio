import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import { input, uuid, integer, invalid } from './daily-api.ts';
import type { CandidateSource } from './candidate-selection.ts';
import type { Candidate, createApprovalAPI } from './approval-api.ts';
export function parseCandidateSourceURL(url: {
  searchParams: Pick<URLSearchParams, 'getAll'>;
}): CandidateSource | null {
  const results = url.searchParams.getAll('result_id'),
    events = url.searchParams.getAll('event_index');
  if (!results.length && !events.length) return null;
  return input(() => {
    if (
      results.length !== 1 ||
      events.length !== 1 ||
      !/^(0|[1-9]\d?)$/.test(events[0])
    )
      return invalid();
    return {
      result_id: uuid(results[0]),
      event_index: integer(Number(events[0]), 0, 31),
    };
  });
}
export function clearCandidateSourceURL(url: { href: string }) {
  const next = new URL(url.href);
  next.searchParams.delete('result_id');
  next.searchParams.delete('event_index');
  return next.pathname + next.search + next.hash;
}
export type CandidateSourceView = {
  status: 'idle' | 'loading' | 'ready' | 'missing' | 'error' | 'denied';
  candidate: Candidate | null;
  error: ErrorCode | null;
};
export function createCandidateSourceLink(
  api: Pick<ReturnType<typeof createApprovalAPI>, 'getCandidate'>,
  token: TokenProvider,
  changed: (view: CandidateSourceView) => void,
  denied: () => void,
) {
  let view: CandidateSourceView = {
      status: 'idle',
      candidate: null,
      error: null,
    },
    sequence = 0,
    disposed = false,
    abort: AbortController | undefined;
  const publish = (patch: Partial<CandidateSourceView>) => {
    if (!disposed) {
      view = { ...view, ...patch };
      changed(view);
    }
  };
  return {
    state: () => view,
    async load(source: CandidateSource | null) {
      if (disposed || view.status === 'denied') return;
      const current = ++sequence;
      abort?.abort();
      abort = new AbortController();
      publish({
        candidate: null,
        status: source ? 'loading' : 'idle',
        error: null,
      });
      if (!source) return;
      try {
        const candidate = await api.getCandidate(source, token, abort.signal);
        if (!disposed && current === sequence)
          publish({ candidate, status: 'ready' });
      } catch (error) {
        if (disposed || current !== sequence) return;
        const code =
            error instanceof ApiError ? error.code : 'dependency_unavailable',
          lost = code === 'access_denied' || code === 'authentication_required';
        publish({
          candidate: null,
          status: lost ? 'denied' : code === 'not_found' ? 'missing' : 'error',
          error: code,
        });
        if (lost) denied();
      }
    },
    dispose() {
      disposed = true;
      sequence++;
      abort?.abort();
    },
  };
}
