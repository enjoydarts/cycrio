import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type {
  MailAPI,
  MailAccount,
  MailProbe,
  ProbeInput,
} from './mail-api.ts';
export type MailForm = {
  id: string;
  baseline: number;
  label: string;
  login: string;
  app_password: string;
  credentialsDirty: boolean;
  foldersDirty: boolean;
  folder_ids: string[];
};
export type MailView = {
  status:
    'idle' | 'loading' | 'ready' | 'saving' | 'error' | 'denied' | 'disposed';
  accounts: MailAccount[];
  selected: string | null;
  form: MailForm | null;
  error: ErrorCode | null;
  success: string | null;
  uncertain: boolean;
  disconnectConfirmation: { id: string; version: number } | null;
};
export const emptyMailView = (): MailView => ({
  status: 'idle',
  accounts: [],
  selected: null,
  form: null,
  error: null,
  success: null,
  uncertain: false,
  disconnectConfirmation: null,
});
export const probePending = (probe: MailProbe | null | undefined) =>
  !!probe && ['queued', 'leased', 'running'].includes(probe.state);
const draft = (account: MailAccount): MailForm => ({
  id: account.id,
  baseline: account.version,
  label: account.label,
  login: '',
  app_password: '',
  credentialsDirty: false,
  foldersDirty: false,
  folder_ids: account.folders
    .filter((folder) => folder.selected)
    .map((folder) => folder.id),
});
export function createMailController(
  api: MailAPI,
  token: TokenProvider,
  changed: (view: MailView) => void,
  onDenied: () => void,
  randomUUID: () => string = () => crypto.randomUUID(),
) {
  let view = emptyMailView(),
    generation = 0,
    disposed = false,
    mutating = false;
  let abort: AbortController | undefined;
  type PendingProbe = { account: string; input: ProbeInput };
  let pending: PendingProbe | null = null;
  const uncertainRequests = new Map<string, PendingProbe>();
  const publish = (patch: Partial<MailView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  const allowed = () => !disposed && view.status !== 'denied';
  const begin = () => {
    abort?.abort();
    abort = new AbortController();
    return { seq: ++generation, signal: abort.signal };
  };
  const current = (seq: number) => allowed() && generation === seq;
  const selected = () =>
    view.accounts.find((account) => account.id === view.selected);
  function failed(error: unknown, seq: number) {
    if (!current(seq)) return;
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    if (code === 'authentication_required' || code === 'access_denied') {
      generation++;
      abort?.abort();
      pending = null;
      uncertainRequests.clear();
      view = { ...emptyMailView(), status: 'denied' };
      changed(view);
      onDenied();
    } else publish({ status: 'error', error: code });
  }
  function replace(account: MailAccount, reset: boolean) {
    publish({
      accounts: [...view.accounts.filter((a) => a.id !== account.id), account],
      ...(reset ? { form: draft(account) } : {}),
    });
  }
  async function refresh() {
    if (!allowed() || mutating) return;
    const { seq, signal } = begin();
    publish({ status: 'loading', error: null });
    try {
      const accounts = await api.list(token, signal);
      if (!current(seq)) return;
      const account = accounts.find((a) => a.id === view.selected);
      if (view.selected && !account && view.form?.baseline !== 0) {
        pending = null;
        publish({
          selected: null,
          form: null,
          uncertain: false,
          disconnectConfirmation: null,
        });
      }
      if (pending) {
        const latest = accounts.find((a) => a.id === pending?.account);
        if (
          !latest ||
          latest.version !== pending.input.expected_version ||
          latest.state === 'disconnected'
        ) {
          pending = null;
          uncertainRequests.delete(latest?.id ?? view.selected ?? '');
          publish({ uncertain: false });
        }
      }
      publish({
        status: 'ready',
        accounts,
        form:
          account && !view.form?.credentialsDirty && !view.form?.foldersDirty
            ? draft(account)
            : view.form,
      });
    } catch (error) {
      failed(error, seq);
    }
  }
  async function mutate(
    operation: (signal: AbortSignal) => Promise<MailAccount>,
    message: string,
    reset = true,
  ) {
    if (!allowed() || mutating) return;
    mutating = true;
    const { seq, signal } = begin();
    publish({ status: 'saving', error: null, success: null });
    try {
      const account = await operation(signal);
      if (current(seq)) {
        pending = null;
        uncertainRequests.delete(account.id);
        replace(account, reset);
        publish({
          status: 'ready',
          success: message,
          uncertain: false,
          disconnectConfirmation: null,
        });
      }
    } catch (error) {
      failed(error, seq);
    } finally {
      mutating = false;
    }
  }
  return {
    state: () => view,
    refresh,
    select(id: string) {
      if (!allowed() || !view.accounts.some((a) => a.id === id)) return;
      if (view.selected === id) return;
      begin();
      pending = uncertainRequests.get(id) ?? null;
      const account = view.accounts.find((a) => a.id === id)!;
      publish({
        status: 'ready',
        selected: id,
        form: draft(account),
        error: null,
        success: null,
        uncertain: pending !== null,
        disconnectConfirmation: null,
      });
    },
    newAccount() {
      if (!allowed() || mutating || view.accounts.length >= 10) return;
      begin();
      pending = null;
      const id = randomUUID();
      publish({
        selected: id,
        form: {
          id,
          baseline: 0,
          label: '',
          login: '',
          app_password: '',
          credentialsDirty: false,
          foldersDirty: false,
          folder_ids: [],
        },
        error: null,
        success: null,
        uncertain: false,
        disconnectConfirmation: null,
      });
    },
    edit(patch: Partial<Pick<MailForm, 'label' | 'login' | 'app_password'>>) {
      if (!allowed() || mutating || !view.form) return;
      publish({
        form: { ...view.form, ...patch, credentialsDirty: true },
        success: null,
      });
    },
    toggleFolder(id: string, checked: boolean) {
      const folder = selected()?.folders.find((f) => f.id === id);
      if (
        !allowed() ||
        mutating ||
        !view.form ||
        !folder?.available ||
        !folder.selectable
      )
        return;
      const ids = view.form.folder_ids.filter((value) => value !== id);
      if (checked) ids.push(id);
      publish({
        form: { ...view.form, folder_ids: ids, foldersDirty: true },
        success: null,
      });
    },
    discard() {
      if (!allowed() || mutating) return;
      const account = selected();
      publish({
        form: account ? draft(account) : null,
        selected: account?.id ?? null,
        error: null,
        success: null,
        disconnectConfirmation: null,
      });
    },
    async save() {
      const form = view.form;
      if (!allowed() || !form || mutating || view.uncertain) return;
      const label = form.label.trim(),
        login = form.login.trim();
      if (
        !label ||
        [...label].length > 80 ||
        login.length > 254 ||
        !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(login) ||
        !/^[\x21-\x7e]{8,1024}$/.test(form.app_password)
      ) {
        publish({ status: 'error', error: 'invalid_request' });
        return;
      }
      await mutate(
        (signal) =>
          api.save(
            form.id,
            {
              expected_version: form.baseline,
              label,
              login,
              app_password: form.app_password,
            },
            token,
            signal,
          ),
        '接続情報を保存しました。続けて接続確認を実行してください。',
      );
    },
    async requestProbe() {
      const account = selected();
      if (
        !allowed() ||
        mutating ||
        !account ||
        account.state === 'disconnected' ||
        probePending(account.latest_probe) ||
        view.form?.credentialsDirty ||
        view.form?.foldersDirty
      )
        return;
      pending ??= {
        account: account.id,
        input: { expected_version: account.version, request_id: randomUUID() },
      };
      uncertainRequests.set(account.id, pending);
      const request = pending;
      mutating = true;
      const { seq, signal } = begin();
      let terminal = false;
      publish({ status: 'saving', error: null, success: null });
      try {
        const probe = await api.requestProbe(
          request.account,
          request.input,
          token,
          signal,
        );
        if (current(seq)) {
          replace({ ...account, latest_probe: probe }, false);
          terminal = !probePending(probe);
          pending = null;
          uncertainRequests.delete(account.id);
          publish({
            status: 'ready',
            uncertain: false,
            success: '接続確認を受け付けました。結果を確認しています。',
          });
        }
      } catch (error) {
        if (current(seq)) {
          const uncertain =
            !(error instanceof ApiError) ||
            error.code === 'dependency_unavailable';
          if (!uncertain) {
            pending = null;
            uncertainRequests.delete(account.id);
          }
          publish({ uncertain });
        }
        failed(error, seq);
      } finally {
        mutating = false;
      }
      if (terminal && current(seq)) await refresh();
    },
    async poll() {
      const account = selected(),
        probe = account?.latest_probe;
      if (
        !allowed() ||
        mutating ||
        view.status === 'loading' ||
        !account ||
        !probe ||
        !probePending(probe)
      )
        return;
      const { seq, signal } = begin();
      try {
        const result = await api.probe(account.id, probe.id, token, signal);
        if (current(seq)) {
          replace({ ...account, latest_probe: result }, false);
          if (!probePending(result)) await refresh();
        }
      } catch (error) {
        failed(error, seq);
      }
    },
    async saveFolders() {
      const form = view.form,
        account = selected();
      if (
        !allowed() ||
        mutating ||
        !form ||
        !account ||
        account.state !== 'ready' ||
        form.credentialsDirty ||
        view.uncertain ||
        probePending(account.latest_probe)
      )
        return;
      const ids = form.folder_ids.filter((id) =>
        account.folders.some((f) => f.id === id && f.available && f.selectable),
      );
      await mutate(
        (signal) =>
          api.saveFolders(account.id, form.baseline, ids, token, signal),
        'フォルダー選択を保存しました。メール同期はまだ実行されません。',
      );
    },
    confirmDisconnect(confirmed: boolean) {
      const account = selected();
      if (!allowed() || mutating || !account) return;
      publish({
        disconnectConfirmation: confirmed
          ? { id: account.id, version: account.version }
          : null,
      });
    },
    async disconnect() {
      const confirmation = view.disconnectConfirmation,
        account = selected();
      if (
        !allowed() ||
        mutating ||
        !account ||
        !confirmation ||
        confirmation.id !== account.id ||
        confirmation.version !== account.version
      )
        return;
      await mutate(
        (signal) =>
          api.disconnect(confirmation.id, confirmation.version, token, signal),
        '連携を解除しました。再接続する場合は新しい接続情報を入力してください。',
      );
    },
    dispose() {
      disposed = true;
      generation++;
      abort?.abort();
      pending = null;
      uncertainRequests.clear();
      view = { ...emptyMailView(), status: 'disposed' };
      changed(view);
    },
  };
}
