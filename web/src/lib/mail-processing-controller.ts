import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type {
  MailProcessingAPI,
  ProcessingInput,
  ProcessingPreview,
  ProcessingResult,
  ProcessingRetryInput,
  ProcessingRun,
} from './mail-processing-api.ts';
export type ProcessingBasis = Pick<
  ProcessingInput,
  'expected_version' | 'runtime_version' | 'profile_id' | 'policy_version'
>;
export type ProcessingForm = {
  max_messages: string;
  confirmed: boolean;
  basis: ProcessingBasis | null;
  dirty: boolean;
};
type PendingProcessingRequest = {
  account: string;
  input: ProcessingInput | ProcessingRetryInput;
  message: string | null;
};
export type ProcessingRequests = Map<string, PendingProcessingRequest>;
export type ProcessingView = {
  account: string | null;
  preview: ProcessingPreview | null;
  runs: ProcessingRun[];
  results: ProcessingResult[];
  form: ProcessingForm;
  uncertain: boolean;
  conflict: boolean;
  status:
    'idle' | 'loading' | 'saving' | 'ready' | 'error' | 'denied' | 'disposed';
  error: ErrorCode | null;
};
const blank = (basis: ProcessingBasis | null = null): ProcessingForm => ({
  max_messages: '',
  confirmed: false,
  basis,
  dirty: false,
});
export const emptyProcessingView = (): ProcessingView => ({
  account: null,
  preview: null,
  runs: [],
  results: [],
  form: blank(),
  uncertain: false,
  conflict: false,
  status: 'idle',
  error: null,
});
export const processingActive = (run: ProcessingRun) =>
  ['queued', 'leased', 'running'].includes(run.state);
export const validProcessingMaximum = (v: string) =>
  /^\d+$/.test(v) && Number(v) >= 1 && Number(v) <= 100;
const basis = (preview: ProcessingPreview): ProcessingBasis | null =>
  preview.profile_id
    ? {
        expected_version: preview.expected_version,
        runtime_version: preview.runtime_version,
        profile_id: preview.profile_id,
        policy_version: preview.policy_version,
      }
    : null;
const sameBasis = (a: ProcessingBasis | null, b: ProcessingBasis | null) =>
  a?.expected_version === b?.expected_version &&
  a?.runtime_version === b?.runtime_version &&
  a?.profile_id === b?.profile_id &&
  a?.policy_version === b?.policy_version;
export function createMailProcessingController(
  api: MailProcessingAPI,
  token: TokenProvider,
  changed: (view: ProcessingView) => void,
  onDenied: () => void,
  randomUUID: () => string = () => crypto.randomUUID(),
  pending: ProcessingRequests = new Map(),
  feedOnly = false,
) {
  let view = emptyProcessingView(),
    generation = 0,
    disposed = false;
  let abort: AbortController | undefined,
    reading: number | null = null,
    mutating: number | null = null;
  const allowed = () => !disposed && view.status !== 'denied';
  const publish = (patch: Partial<ProcessingView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  const begin = () => {
    abort?.abort();
    abort = new AbortController();
    reading = null;
    return { seq: ++generation, signal: abort.signal };
  };
  const current = (seq: number) => allowed() && generation === seq;
  function fail(error: unknown, seq: number) {
    if (!current(seq)) return;
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    if (code === 'authentication_required' || code === 'access_denied') {
      begin();
      pending.clear();
      view = { ...emptyProcessingView(), status: 'denied' };
      changed(view);
      onDenied();
    } else
      publish({
        status: 'error',
        error: code,
        ...(code === 'conflict'
          ? { conflict: true, form: { ...view.form, confirmed: false } }
          : {}),
      });
  }
  const insert = (run: ProcessingRun) =>
    [run, ...view.runs.filter((v) => v.id !== run.id)]
      .sort((a, b) => b.created_at.localeCompare(a.created_at))
      .slice(0, 20);
  async function refresh() {
    if (!allowed() || !view.account || mutating !== null || reading !== null)
      return;
    const account = view.account,
      { seq, signal } = begin();
    reading = seq;
    publish({ status: 'loading', error: null });
    try {
      const [preview, runs, results] = await Promise.all([
        api.preview(account, token, signal),
        feedOnly ? Promise.resolve([]) : api.runs(account, token, signal),
        feedOnly ? Promise.resolve([]) : api.results(account, token, signal),
      ]);
      if (!current(seq)) return;
      const latest = basis(preview),
        stale =
          !sameBasis(view.form.basis, latest) ||
          view.preview?.full_email_allowed !== preview.full_email_allowed;
      const preserve = view.form.dirty || view.uncertain || view.conflict;
      publish({
        preview,
        runs,
        results,
        status: 'ready',
        form: preserve
          ? { ...view.form, ...(stale ? { confirmed: false } : {}) }
          : blank(latest),
        conflict: view.conflict || (preserve && stale),
      });
    } catch (error) {
      fail(error, seq);
    } finally {
      if (reading === seq) reading = null;
    }
  }
  async function submit(request: PendingProcessingRequest) {
    if (!allowed() || mutating !== null || request.account !== view.account)
      return;
    const { seq, signal } = begin();
    mutating = seq;
    pending.set(request.account, request);
    publish({ status: 'saving', error: null });
    let terminal = false;
    try {
      const run = request.message
        ? await api.retry(
            request.account,
            request.message,
            request.input as ProcessingRetryInput,
            token,
            signal,
          )
        : await api.start(request.account, request.input, token, signal);
      if (current(seq)) {
        pending.delete(request.account);
        terminal = !processingActive(run);
        publish({
          runs: insert(run),
          status: 'ready',
          uncertain: false,
          form: { ...view.form, confirmed: false },
        });
      }
    } catch (error) {
      if (current(seq)) {
        const uncertain =
          !(error instanceof ApiError) ||
          error.code === 'dependency_unavailable';
        if (!uncertain) pending.delete(request.account);
        publish({ uncertain });
        fail(error, seq);
      }
    } finally {
      if (mutating === seq) mutating = null;
    }
    if (terminal && current(seq)) await refresh();
  }
  const mayStart = () =>
    allowed() &&
    mutating === null &&
    !view.uncertain &&
    !view.conflict &&
    !!view.preview?.ready &&
    view.preview.full_email_allowed === true &&
    !!view.form.basis &&
    sameBasis(view.form.basis, basis(view.preview)) &&
    !view.runs.some(processingActive);
  return {
    state: () => view,
    setAccount(account: string) {
      if (!allowed() || account === view.account) return;
      begin();
      mutating = null;
      const request = pending.get(account);
      view = {
        ...emptyProcessingView(),
        account,
        uncertain: !!request,
        form: request
          ? {
              max_messages: String(request.input.max_messages),
              confirmed: request.input.confirmed,
              basis: {
                expected_version: request.input.expected_version,
                runtime_version: request.input.runtime_version,
                profile_id: request.input.profile_id,
                policy_version: request.input.policy_version,
              },
              dirty: true,
            }
          : blank(),
      };
      changed(view);
    },
    edit(patch: Partial<Pick<ProcessingForm, 'max_messages' | 'confirmed'>>) {
      if (allowed() && mutating === null && !view.uncertain)
        publish({
          form: {
            ...view.form,
            ...(patch.max_messages !== undefined &&
            patch.max_messages !== view.form.max_messages
              ? { confirmed: false }
              : {}),
            ...patch,
            dirty: true,
          },
        });
    },
    discard() {
      if (allowed() && mutating === null && !view.uncertain)
        publish({
          form: blank(view.preview ? basis(view.preview) : null),
          conflict: false,
          error: null,
        });
    },
    refresh,
    async poll() {
      if (view.uncertain || view.runs.some(processingActive)) await refresh();
    },
    async start() {
      if (
        !mayStart() ||
        !validProcessingMaximum(view.form.max_messages) ||
        !view.form.confirmed ||
        !view.preview?.pending_count
      )
        return;
      await submit({
        account: view.account!,
        message: null,
        input: {
          ...view.form.basis!,
          full_email: true,
          request_id: randomUUID(),
          max_messages: Number(view.form.max_messages),
          confirmed: true,
        },
      });
    },
    async replay() {
      const request = view.account && pending.get(view.account);
      if (view.uncertain && request) await submit(request);
    },
    async retryResult(
      resultID: string,
      chargeConfirmed: boolean,
      unknownConfirmed: boolean,
    ) {
      const result = view.results.find((r) => r.id === resultID);
      if (result)
        await this.retrySelected(result, chargeConfirmed, unknownConfirmed);
    },
    async retrySelected(
      result: ProcessingResult,
      chargeConfirmed: boolean,
      unknownConfirmed: boolean,
    ) {
      if (
        !mayStart() ||
        !result ||
        !['failed', 'unknown'].includes(result.state) ||
        !chargeConfirmed ||
        (result.state === 'unknown' && !unknownConfirmed)
      )
        return;
      await submit({
        account: view.account!,
        message: result.message_id,
        input: {
          ...view.form.basis!,
          full_email: true,
          request_id: randomUUID(),
          max_messages: 1,
          confirmed: true,
          expected_result_id: result.id,
          confirm_unknown: result.state === 'unknown' && unknownConfirmed,
        },
      });
    },
    async cancel(runID: string) {
      const run = view.runs.find((r) => r.id === runID);
      if (run) await this.cancelSelected(run);
    },
    async cancelSelected(run: ProcessingRun) {
      const runID = run.id;
      if (
        !allowed() ||
        mutating !== null ||
        !view.account ||
        !run ||
        run.account_id !== view.account ||
        !processingActive(run)
      )
        return;
      const account = view.account,
        { seq, signal } = begin();
      mutating = seq;
      publish({ status: 'saving', error: null });
      try {
        const result = await api.cancel(account, runID, token, signal);
        if (current(seq)) publish({ status: 'ready', runs: insert(result) });
      } catch (error) {
        fail(error, seq);
      } finally {
        if (mutating === seq) mutating = null;
      }
      if (current(seq) && view.status === 'ready') await refresh();
    },
    dispose() {
      if (disposed) return;
      begin();
      disposed = true;
      view = { ...emptyProcessingView(), status: 'disposed' };
      changed(view);
    },
  };
}
