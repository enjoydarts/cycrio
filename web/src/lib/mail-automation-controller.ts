import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import {
  exclusiveEnd,
  inclusiveEnd,
  validAutomationRange,
  type AutomationBasis,
  type AutomationInput,
  type AutomationControl,
  type MailAutomationAPI,
  type MailAutomationState,
} from './mail-automation-api.ts';
import type {
  MailProcessingAPI,
  ProcessingPreview,
} from './mail-processing-api.ts';
export type AutomationDraft = {
  since: string;
  through: string;
  basis: AutomationBasis;
  version: number;
  preview: ProcessingPreview;
  conditions: string;
};
type Action = 'start' | 'stop' | 'resume';
type Pending = { action: Action; body: AutomationInput | AutomationControl };
export type AutomationView = {
  account: string;
  snapshot: MailAutomationState | null;
  preview: ProcessingPreview | null;
  preferences: { enabled: boolean; version: number } | null;
  draft: AutomationDraft | null;
  dirty: boolean;
  conflict: boolean;
  confirmed: boolean;
  pending: Pending | null;
  uncertain: boolean;
  acknowledged: Action | null;
  readError: ErrorCode | null;
  error: ErrorCode | null;
  loading: boolean;
  busy: boolean;
  denied: boolean;
};
export const emptyAutomationView = (): AutomationView => ({
  account: '',
  snapshot: null,
  preview: null,
  preferences: null,
  draft: null,
  dirty: false,
  conflict: false,
  confirmed: false,
  pending: null,
  uncertain: false,
  acknowledged: null,
  readError: null,
  error: null,
  loading: false,
  busy: false,
  denied: false,
});
const conditions = (s: MailAutomationState, p: ProcessingPreview) =>
  JSON.stringify([
    s.version,
    s.basis,
    s.scope ?? s.default_scope,
    p.primary,
    p.secondary,
    p.full_email_allowed,
  ]);
const matching = (s: MailAutomationState, p: ProcessingPreview) =>
  s.account_id === p.account_id &&
  s.basis.account_version === p.expected_version &&
  s.basis.runtime_version === p.runtime_version &&
  s.basis.profile_id === p.profile_id &&
  s.basis.policy_version === p.policy_version;
export function createMailAutomationController(
  api: MailAutomationAPI,
  processing: Pick<MailProcessingAPI, 'preview'>,
  token: TokenProvider,
  changed: (v: AutomationView) => void,
  onDenied: () => void,
  nonce: () => string = () => crypto.randomUUID(),
) {
  let v = emptyAutomationView(),
    disposed = false,
    sequence = 0,
    abort: AbortController | undefined;
  const publish = (patch: Partial<AutomationView>) => {
    if (disposed) return;
    v = { ...v, ...patch };
    changed(v);
  };
  const alive = (seq: number) => !disposed && !v.denied && seq === sequence;
  const denied = (error: unknown) => {
    if (
      error instanceof ApiError &&
      ['authentication_required', 'access_denied'].includes(error.code)
    ) {
      sequence++;
      abort?.abort();
      v = { ...emptyAutomationView(), denied: true };
      changed(v);
      onDenied();
      return true;
    }
    return false;
  };
  function adopt() {
    if (
      disposed ||
      v.denied ||
      v.busy ||
      v.pending ||
      !v.snapshot ||
      !v.preview ||
      !matching(v.snapshot, v.preview)
    )
      return;
    const s = v.snapshot,
      scope = s.scope ?? s.default_scope;
    publish({
      draft: {
        since: v.draft && (v.dirty || v.conflict) ? v.draft.since : scope.since,
        through:
          v.draft && (v.dirty || v.conflict)
            ? v.draft.through
            : inclusiveEnd(scope.before),
        basis: { ...s.basis },
        version: s.version,
        preview: v.preview,
        conditions: conditions(s, v.preview),
      },
      dirty: false,
      conflict: false,
      confirmed: false,
      error: null,
    });
  }
  async function refresh() {
    if (disposed || v.denied || !v.account || v.busy) return;
    const seq = ++sequence,
      account = v.account;
    abort?.abort();
    abort = new AbortController();
    const signal = abort.signal;
    publish({ loading: !v.snapshot });
    const guard = <T>(request: Promise<T>): Promise<T> =>
      request.catch((error) => {
        if (alive(seq)) denied(error);
        throw error;
      });
    const results = await Promise.allSettled([
      guard(api.read(account, token, signal)),
      guard(processing.preview(account, token, signal)),
      guard(api.preferences(token, signal)),
    ]);
    if (!alive(seq)) return;
    for (const r of results)
      if (r.status === 'rejected' && denied(r.reason)) return;
    const [s, p, pref] = results;
    const failed = results.find((r) => r.status === 'rejected');
    publish({
      loading: false,
      readError: failed
        ? failed.status === 'rejected' && failed.reason instanceof ApiError
          ? failed.reason.code
          : 'dependency_unavailable'
        : null,
      ...(s.status === 'fulfilled' ? { snapshot: s.value } : {}),
      ...(p.status === 'fulfilled' ? { preview: p.value } : {}),
      ...(pref.status === 'fulfilled' ? { preferences: pref.value } : {}),
    });
    if (s.status === 'fulfilled' && p.status === 'fulfilled') {
      const changedBasis =
        !matching(s.value, p.value) ||
        (!!v.draft && v.draft.conditions !== conditions(s.value, p.value));
      if (
        changedBasis &&
        v.draft &&
        (v.dirty || v.confirmed || v.conflict || v.pending)
      )
        publish({ conflict: true, confirmed: false });
      else if (
        !v.pending &&
        !v.dirty &&
        !v.conflict &&
        (!v.draft || changedBasis)
      )
        adopt();
    }
  }
  async function execute(request: Pending) {
    if (disposed || v.denied || v.busy || !v.account) return;
    const seq = ++sequence,
      account = v.account;
    abort?.abort();
    abort = new AbortController();
    publish({ busy: true, pending: request, error: null, uncertain: false });
    let accepted = false;
    try {
      const result =
        request.action === 'start'
          ? await api.save(
              account,
              request.body as AutomationInput,
              token,
              abort.signal,
            )
          : await api[request.action](
              account,
              request.body,
              token,
              abort.signal,
            );
      if (!alive(seq)) return;
      accepted = true;
      publish({
        snapshot: result,
        pending: null,
        uncertain: false,
        acknowledged: request.action,
        confirmed: false,
        dirty: false,
        conflict: false,
        busy: false,
        draft: null,
      });
    } catch (error) {
      if (!alive(seq) || denied(error)) return;
      const code =
        error instanceof ApiError ? error.code : 'dependency_unavailable';
      const uncertain = code === 'dependency_unavailable';
      publish({
        busy: false,
        error: code,
        pending: uncertain ? request : null,
        uncertain,
        confirmed: false,
        conflict: code === 'conflict' || v.conflict,
      });
      if (code === 'conflict') await refresh();
    }
    if (accepted) await refresh();
  }
  return {
    state: () => v,
    async select(account: string) {
      if (disposed || v.denied || v.pending || v.busy) return;
      sequence++;
      abort?.abort();
      v = { ...emptyAutomationView(), account };
      changed(v);
      if (account) await refresh();
    },
    refresh,
    adopt,
    edit(patch: Partial<Pick<AutomationDraft, 'since' | 'through'>>) {
      if (!v.draft || v.pending || v.busy || v.denied || disposed) return;
      publish({
        draft: { ...v.draft, ...patch },
        dirty: true,
        confirmed: false,
        acknowledged: null,
      });
    },
    confirm(value: boolean) {
      if (!v.draft || v.pending || v.busy || v.conflict || v.denied || disposed)
        return;
      publish({ confirmed: value });
    },
    async start() {
      const d = v.draft,
        s = v.snapshot,
        p = v.preview;
      if (
        !d ||
        !s ||
        !p ||
        !v.confirmed ||
        v.conflict ||
        v.pending ||
        v.busy ||
        v.readError ||
        !s.preparation_ready ||
        !p.full_email_allowed ||
        !matching(s, p) ||
        d.conditions !== conditions(s, p) ||
        !validAutomationRange(d.since, d.through)
      )
        return;
      await execute({
        action: 'start',
        body: {
          expected_settings_version: d.version,
          expected_account_version: d.basis.account_version,
          runtime_version: d.basis.runtime_version,
          profile_id: d.basis.profile_id,
          policy_version: d.basis.policy_version,
          since: d.since,
          before: exclusiveEnd(d.through),
          full_email: true,
          interval_seconds: 300,
          confirmed: true,
          auto_register: true,
          request_id: nonce(),
        },
      });
    },
    async stop() {
      if (!v.snapshot?.id || v.pending || v.busy) return;
      await execute({
        action: 'stop',
        body: {
          expected_settings_version: v.snapshot.version,
          request_id: nonce(),
        },
      });
    },
    async resume() {
      if (
        v.snapshot?.state !== 'stopped' ||
        !v.snapshot.ready ||
        v.pending ||
        v.busy
      )
        return;
      await execute({
        action: 'resume',
        body: {
          expected_settings_version: v.snapshot.version,
          request_id: nonce(),
        },
      });
    },
    async replay() {
      if (v.uncertain && v.pending && !v.busy) await execute(v.pending);
    },
    suspend() {
      sequence++;
      abort?.abort();
      publish({
        loading: false,
        busy: false,
        ...(v.pending ? { uncertain: true, confirmed: false } : {}),
      });
    },
    dispose() {
      disposed = true;
      sequence++;
      abort?.abort();
      v = emptyAutomationView();
    },
  };
}
