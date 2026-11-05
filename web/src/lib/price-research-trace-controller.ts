import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type {
  PriceResearchAPI,
  ResearchTrace,
  ResearchTraceSummary,
} from './price-research-api.ts';

export type ResearchTraceState = {
  calls: ResearchTraceSummary[];
  trace: ResearchTrace | null;
  busy: boolean;
  loaded: boolean;
  error: ErrorCode | null;
};
export const emptyResearchTraceState = (): ResearchTraceState => ({
  calls: [],
  trace: null,
  busy: false,
  loaded: false,
  error: null,
});
export function createResearchTraceController(
  api: PriceResearchAPI,
  token: TokenProvider,
  changed: (state: ResearchTraceState) => void,
  onDenied: () => void,
) {
  let state = emptyResearchTraceState(),
    generation = 0,
    blocked = false;
  let request = new AbortController();
  const emit = (patch: Partial<ResearchTraceState>) => {
    state = { ...state, ...patch };
    changed(state);
  };
  const close = () => {
    generation++;
    request.abort();
    emit({ trace: null, busy: false, error: null });
  };
  async function perform(
    action: (signal: AbortSignal) => Promise<Partial<ResearchTraceState>>,
  ) {
    if (blocked || state.busy) return;
    const id = ++generation;
    request.abort();
    request = new AbortController();
    emit({ busy: true, trace: null, error: null });
    try {
      const value = await action(request.signal);
      if (id === generation) emit({ ...value, busy: false });
    } catch (error) {
      if (id !== generation) return;
      const code =
        error instanceof ApiError ? error.code : 'dependency_unavailable';
      if (['authentication_required', 'access_denied'].includes(code)) {
        blocked = true;
        generation++;
        request.abort();
        emit({ ...emptyResearchTraceState(), error: code });
        onDenied();
      } else emit({ busy: false, error: code });
    }
  }
  return {
    get state() {
      return state;
    },
    load: (runID: string) =>
      perform(async (signal) => ({
        calls: await api.traceCalls(runID, token, signal),
        loaded: true,
      })),
    fetch: (runID: string, callID: string) =>
      perform(async (signal) => ({
        trace: await api.trace(runID, callID, token, signal),
      })),
    close,
    dispose() {
      blocked = true;
      generation++;
      request.abort();
      emit(emptyResearchTraceState());
    },
  };
}
