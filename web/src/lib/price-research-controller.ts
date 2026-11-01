import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type { CatalogAPI, Model } from './catalog-api.ts';
import type {
  PriceResearchAPI,
  ResearchInput,
  ResearchProvider,
  ResearchState,
  ResearchTarget,
} from './price-research-api.ts';
export type PriceResearchView = {
  status:
    'idle' | 'loading' | 'saving' | 'ready' | 'error' | 'denied' | 'disposed';
  saved: ResearchState | null;
  draft: ResearchInput | null;
  dirty: boolean;
  uncertain: boolean;
  models: Model[];
  total: number;
  error: ErrorCode | null;
  success: string | null;
};
export const emptyPriceResearchView = (): PriceResearchView => ({
  status: 'idle',
  saved: null,
  draft: null,
  dirty: false,
  uncertain: false,
  models: [],
  total: 0,
  error: null,
  success: null,
});
export const researchPending = (state: ResearchState | null) =>
  ['queued', 'running'].includes(state?.latest_run?.state ?? '');
const draftOf = (state: ResearchState): ResearchInput => ({
  expected_version: state.version,
  model_id: state.model_id,
  automatic_enabled: state.automatic_enabled,
  interval_hours: state.interval_hours,
});
export function createPriceResearchController(
  api: PriceResearchAPI,
  catalog: CatalogAPI,
  token: TokenProvider,
  changed: (view: PriceResearchView) => void,
  onDenied: () => void,
) {
  let state = emptyPriceResearchView(),
    generation = 0,
    request = new AbortController();
  const emit = (value: Partial<PriceResearchView>) => {
    state = { ...state, ...value };
    changed(state);
  };
  const active = () =>
    !['loading', 'saving', 'denied', 'disposed'].includes(state.status);
  const deny = (code: ErrorCode) => {
    generation++;
    request.abort();
    emit({ ...emptyPriceResearchView(), status: 'denied', error: code });
    onDenied();
  };
  async function perform(
    action: (signal: AbortSignal) => Promise<Partial<PriceResearchView>>,
    write = false,
  ) {
    if (!active()) return;
    const id = ++generation;
    request.abort();
    request = new AbortController();
    const signal = request.signal;
    emit({ status: write ? 'saving' : 'loading', error: null, success: null });
    try {
      const value = await action(signal);
      if (id === generation) emit({ ...value, status: 'ready' });
    } catch (error) {
      if (id !== generation) return;
      const code =
        error instanceof ApiError ? error.code : 'dependency_unavailable';
      if (code === 'authentication_required' || code === 'access_denied') {
        deny(code);
        return;
      }
      if (code === 'conflict') {
        try {
          const saved = await api.load(token, signal);
          if (id === generation) emit({ saved });
        } catch (reloadError) {
          if (id !== generation) return;
          if (
            reloadError instanceof ApiError &&
            ['authentication_required', 'access_denied'].includes(
              reloadError.code,
            )
          ) {
            deny(reloadError.code);
            return;
          }
        }
      }
      if (id === generation)
        emit({
          status: 'error',
          error: code,
          uncertain:
            state.uncertain || (write && code === 'dependency_unavailable'),
        });
    }
  }
  const canRun = () =>
    !!state.saved?.model_id &&
    !state.dirty &&
    !state.uncertain &&
    !researchPending(state.saved);
  return {
    get state() {
      return state;
    },
    load(discard = false) {
      return perform(async (signal) => {
        const saved = await api.load(token, signal);
        return {
          saved,
          draft: discard || !state.dirty ? draftOf(saved) : state.draft,
          dirty: discard ? false : state.dirty,
          uncertain: false,
        };
      });
    },
    edit(values: Partial<Omit<ResearchInput, 'expected_version'>>) {
      if (active() && !state.uncertain && state.draft)
        emit({
          draft: { ...state.draft, ...values },
          dirty: true,
          success: null,
        });
    },
    search(provider: ResearchProvider, search: string, offset = 0) {
      return perform(async (signal) => {
        const page = await catalog.list(
          { provider, search, offset },
          token,
          signal,
        );
        return {
          models: page.models.filter(
            (model) => model.enabled && model.available && !model.deleted_at,
          ),
          total: page.total,
        };
      });
    },
    save() {
      if (state.uncertain || !state.draft) return Promise.resolve();
      const input = { ...state.draft };
      if (
        !input.model_id ||
        !Number.isInteger(input.interval_hours) ||
        input.interval_hours < 1 ||
        input.interval_hours > 168
      ) {
        emit({ error: 'invalid_request' });
        return Promise.resolve();
      }
      return perform(async (signal) => {
        const saved = await api.save(input, token, signal);
        return {
          saved,
          draft: draftOf(saved),
          dirty: false,
          success:
            '料金調査の設定を保存しました。Web検索テストを実行してください。',
        };
      }, true);
    },
    test() {
      if (!canRun()) return Promise.resolve();
      return perform(
        async (signal) => ({
          saved: {
            ...state.saved!,
            latest_run: await api.test(state.saved!.version, token, signal),
          },
          success: 'Web検索テストを依頼しました。',
        }),
        true,
      );
    },
    update(target: ResearchTarget) {
      if (!canRun() || !state.saved?.ready) return Promise.resolve();
      return perform(
        async (signal) => ({
          saved: {
            ...state.saved!,
            latest_run: await api.update(
              state.saved!.version,
              target,
              token,
              signal,
            ),
          },
          success: '公式価格の更新を依頼しました。',
        }),
        true,
      );
    },
    poll() {
      if (!researchPending(state.saved) || state.uncertain)
        return Promise.resolve();
      const runID = state.saved!.latest_run!.id;
      return perform(async (signal) => {
        const latest = await api.run(runID, token, signal);
        const saved = await api.load(token, signal);
        return {
          saved: {
            ...saved,
            latest_run:
              saved.latest_run?.id === runID ? latest : saved.latest_run,
          },
          draft: state.dirty ? state.draft : draftOf(saved),
        };
      });
    },
    dispose() {
      generation++;
      request.abort();
      state = { ...emptyPriceResearchView(), status: 'disposed' };
    },
  };
}
