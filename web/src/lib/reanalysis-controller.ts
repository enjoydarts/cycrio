import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type {
  MailProcessingAPI,
  ProcessingPreview,
  ProcessingReanalysisInput,
} from './mail-processing-api.ts';
type Summary = { accepted_count: number; skipped_count: number };
export type ReanalysisView = {
  account: string | null;
  before: string;
  preview: ProcessingPreview | null;
  confirmed: boolean;
  uncertain: boolean;
  error: ErrorCode | null;
  summary: Summary | null;
  status:
    'idle' | 'loading' | 'ready' | 'saving' | 'error' | 'denied' | 'disposed';
};
export const emptyReanalysisView = (): ReanalysisView => ({
  account: null,
  before: '',
  preview: null,
  confirmed: false,
  uncertain: false,
  error: null,
  summary: null,
  status: 'idle',
});
export function createReanalysisController(
  api: Pick<MailProcessingAPI, 'preview' | 'reanalyze'>,
  token: TokenProvider,
  changed: (view: ReanalysisView) => void,
  onDenied: () => void,
  randomUUID: () => string = () => crypto.randomUUID(),
  now = () => new Date(),
) {
  let view = emptyReanalysisView(),
    generation = 0,
    abort: AbortController | undefined;
  let pending: ProcessingReanalysisInput | null = null;
  const allowed = () => !['denied', 'disposed'].includes(view.status);
  const busy = () => ['loading', 'saving'].includes(view.status);
  const publish = (patch: Partial<ReanalysisView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  const begin = () => {
    abort?.abort();
    abort = new AbortController();
    return { seq: ++generation, signal: abort.signal };
  };
  const current = (seq: number) => allowed() && seq === generation;
  function fail(error: unknown) {
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    if (['authentication_required', 'access_denied'].includes(code)) {
      begin();
      pending = null;
      view = { ...emptyReanalysisView(), status: 'denied' };
      changed(view);
      onDenied();
    } else publish({ status: 'error', error: code });
  }
  async function refresh() {
    if (!allowed() || !view.account || busy() || view.uncertain) return;
    const account = view.account,
      { seq, signal } = begin();
    publish({ status: 'loading', confirmed: false, preview: null });
    try {
      const preview = await api.preview(account, token, signal);
      if (current(seq)) publish({ preview, status: 'ready' });
    } catch (error) {
      if (current(seq)) fail(error);
    }
  }
  async function submit(input: ProcessingReanalysisInput) {
    if (!allowed() || busy() || !view.account) return;
    const account = view.account,
      { seq, signal } = begin();
    pending = input;
    publish({ status: 'saving', error: null });
    try {
      const result = await api.reanalyze(account, input, token, signal);
      if (current(seq)) {
        pending = null;
        publish({
          summary: result,
          uncertain: false,
          confirmed: false,
          status: 'ready',
        });
      }
    } catch (error) {
      if (!current(seq)) return;
      const uncertain =
        !(error instanceof ApiError) || error.code === 'dependency_unavailable';
      if (!uncertain) pending = null;
      publish({ uncertain, confirmed: false });
      fail(error);
      if (
        error instanceof ApiError &&
        error.code === 'conflict' &&
        current(seq)
      )
        await refresh();
    }
  }
  return {
    state: () => view,
    async open(account: string) {
      if (!allowed() || busy() || view.uncertain) return;
      begin();
      view = { ...emptyReanalysisView(), account, before: now().toISOString() };
      changed(view);
      await refresh();
    },
    confirm(value: boolean) {
      if (allowed() && !busy() && !view.uncertain)
        publish({ confirmed: value });
    },
    refresh,
    async start() {
      const p = view.preview;
      if (
        !allowed() ||
        busy() ||
        view.uncertain ||
        view.summary ||
        !view.confirmed ||
        !p?.ready ||
        !p.profile_id ||
        p.full_email_allowed !== true
      )
        return;
      await submit({
        before: view.before,
        expected_version: p.expected_version,
        runtime_version: p.runtime_version,
        profile_id: p.profile_id,
        policy_version: p.policy_version,
        request_id: randomUUID(),
        max_messages: 100,
        full_email: true,
        confirmed: true,
      });
    },
    async replay() {
      if (view.uncertain && pending) await submit(pending);
    },
    close() {
      if (busy() || view.uncertain) return false;
      begin();
      pending = null;
      if (allowed()) {
        view = emptyReanalysisView();
        changed(view);
      }
      return true;
    },
    dispose() {
      begin();
      pending = null;
      view = { ...emptyReanalysisView(), status: 'disposed' };
      changed(view);
    },
  };
}
