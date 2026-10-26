import {
  validSyncRange,
  type MailSyncAPI,
  type SyncRun,
  type SyncInput,
} from './mail-sync-api.ts';
import type { MailAccount } from './mail-api.ts';
import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
export type SyncForm = {
  since: string;
  before: string;
  max_messages: string;
  expected_version: number;
  dirty: boolean;
};
export type SyncView = {
  account: MailAccount | null;
  form: SyncForm;
  runs: SyncRun[];
  uncertain: boolean;
  conflict: boolean;
  status:
    'idle' | 'loading' | 'saving' | 'ready' | 'error' | 'denied' | 'disposed';
  error: ErrorCode | null;
};
type Pending = { account: string; input: SyncInput };
export type SyncRequests = Map<string, Pending>;
const blank = (version = 1): SyncForm => ({
  since: '',
  before: '',
  max_messages: '',
  expected_version: version,
  dirty: false,
});
export const emptySyncView = (): SyncView => ({
  account: null,
  form: blank(),
  runs: [],
  uncertain: false,
  conflict: false,
  status: 'idle',
  error: null,
});
export const syncActive = (run: SyncRun) =>
  ['queued', 'leased', 'running'].includes(run.state);
export const selectedSyncFolders = (account: MailAccount | null) =>
  account?.folders.filter((f) => f.selected && f.available && f.selectable)
    .length ?? 0;
export function createMailSyncController(
  api: MailSyncAPI,
  token: TokenProvider,
  changed: (view: SyncView) => void,
  onDenied: () => void,
  refreshAccount: () => void,
  randomUUID: () => string = () => crypto.randomUUID(),
  pending: SyncRequests = new Map(),
) {
  let view = emptySyncView(),
    generation = 0,
    disposed = false;
  let abort: AbortController | undefined,
    reading: number | null = null,
    mutating: number | null = null;
  const terminalNotified = new Set<string>();
  const publish = (patch: Partial<SyncView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  const allowed = () => !disposed && view.status !== 'denied';
  const begin = () => {
    abort?.abort();
    abort = new AbortController();
    reading = null;
    return { seq: ++generation, signal: abort.signal };
  };
  const current = (seq: number) => allowed() && seq === generation;
  function fail(error: unknown, seq: number) {
    if (!current(seq)) return;
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    if (code === 'access_denied' || code === 'authentication_required') {
      begin();
      pending.clear();
      view = { ...emptySyncView(), status: 'denied' };
      changed(view);
      onDenied();
    } else {
      publish({
        status: 'error',
        error: code,
        ...(code === 'conflict' ? { conflict: true } : {}),
      });
      if (code === 'conflict') refreshAccount();
    }
  }
  const insert = (run: SyncRun) =>
    [run, ...view.runs.filter((value) => value.id !== run.id)]
      .sort((a, b) => b.created_at.localeCompare(a.created_at))
      .slice(0, 20);
  function notifyTerminal(runs: SyncRun[]) {
    let notify = false;
    for (const run of runs) {
      if (!syncActive(run) && !terminalNotified.has(run.id)) {
        terminalNotified.add(run.id);
        notify = true;
      }
    }
    if (notify) refreshAccount();
  }
  async function refresh() {
    const account = view.account;
    if (!allowed() || !account || mutating !== null || reading !== null) return;
    const { seq, signal } = begin();
    reading = seq;
    publish({ status: 'loading', error: null });
    try {
      const runs = await api.list(account.id, token, signal);
      if (current(seq)) {
        publish({ runs, status: 'ready' });
        notifyTerminal(runs);
      }
    } catch (error) {
      fail(error, seq);
    } finally {
      if (reading === seq) reading = null;
    }
  }
  async function submit(request: Pending) {
    if (!allowed() || mutating !== null || view.account?.id !== request.account)
      return;
    const { seq, signal } = begin();
    mutating = seq;
    pending.set(request.account, request);
    publish({ status: 'saving', error: null });
    let terminal = false;
    try {
      const run = await api.start(
        request.account,
        request.input,
        token,
        signal,
      );
      if (current(seq)) {
        pending.delete(request.account);
        terminal = !syncActive(run);
        publish({ runs: insert(run), uncertain: false, status: 'ready' });
        notifyTerminal([run]);
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
  return {
    state: () => view,
    setAccount(account: MailAccount) {
      if (!allowed()) return;
      if (view.account?.id !== account.id) {
        begin();
        mutating = null;
        const request = pending.get(account.id);
        view = {
          ...emptySyncView(),
          account,
          form: request
            ? {
                since: request.input.since,
                before: request.input.before,
                max_messages: String(request.input.max_messages),
                expected_version: request.input.expected_version,
                dirty: true,
              }
            : blank(account.version),
          uncertain: !!request,
          conflict:
            !!request && request.input.expected_version !== account.version,
        };
        changed(view);
        return;
      }
      publish({
        account,
        form: view.form.dirty ? view.form : blank(account.version),
        conflict:
          view.conflict ||
          (view.form.dirty && view.form.expected_version !== account.version),
      });
    },
    edit(patch: Partial<Pick<SyncForm, 'since' | 'before' | 'max_messages'>>) {
      if (allowed() && mutating === null && !view.uncertain)
        publish({ form: { ...view.form, ...patch, dirty: true } });
    },
    discard() {
      if (!allowed() || mutating !== null || view.uncertain) return;
      publish({
        form: blank(view.account?.version),
        conflict: false,
        error: null,
      });
    },
    refresh,
    async poll() {
      if (view.uncertain || view.runs.some(syncActive)) await refresh();
    },
    async start() {
      const { account, form } = view;
      if (
        !allowed() ||
        !account ||
        mutating !== null ||
        view.uncertain ||
        view.conflict ||
        account.state !== 'ready' ||
        selectedSyncFolders(account) === 0 ||
        view.runs.some(syncActive) ||
        !validSyncRange(form.since, form.before, form.max_messages)
      )
        return;
      await submit({
        account: account.id,
        input: {
          expected_version: form.expected_version,
          request_id: randomUUID(),
          since: form.since,
          before: form.before,
          max_messages: Number(form.max_messages),
        },
      });
    },
    async retry() {
      const request = view.account && pending.get(view.account.id);
      if (request && view.uncertain) await submit(request);
    },
    async cancel(id: string) {
      const account = view.account,
        run = view.runs.find(
          (run) => run.id === id && run.account_id === account?.id,
        );
      if (!allowed() || !account || !run || mutating !== null) return;
      const { seq, signal } = begin();
      mutating = seq;
      publish({ status: 'saving', error: null });
      try {
        const result = await api.cancel(account.id, run.id, token, signal);
        if (current(seq)) {
          publish({ status: 'ready', runs: insert(result) });
          notifyTerminal([result]);
        }
      } catch (error) {
        fail(error, seq);
      } finally {
        if (mutating === seq) mutating = null;
      }
    },
    dispose() {
      if (disposed) return;
      begin();
      disposed = true;
      view = { ...emptySyncView(), status: 'disposed' };
      changed(view);
    },
  };
}
