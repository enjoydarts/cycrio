import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import {
  validRetention,
  type MailProcessingAPI,
  type PolicyInput,
  type PolicyState,
} from './mail-processing-api.ts';
export type PolicyForm = PolicyInput & { dirty: boolean };
export type ProcessingPolicyView = {
  current: PolicyState | null;
  form: PolicyForm | null;
  conflict: boolean;
  status:
    'idle' | 'loading' | 'saving' | 'ready' | 'error' | 'denied' | 'disposed';
  error: ErrorCode | null;
};
export const emptyProcessingPolicyView = (): ProcessingPolicyView => ({
  current: null,
  form: null,
  conflict: false,
  status: 'idle',
  error: null,
});
function formFor(state: PolicyState): PolicyForm | null {
  if (!state.profile) return null;
  const policy = state.policy;
  return {
    profile_id: state.profile.id,
    expected_version: policy?.version ?? 0,
    enabled: policy?.enabled ?? false,
    primary_retention: policy?.primary_retention ?? '',
    secondary_retention: policy?.secondary_retention ?? '',
    primary_confirmed: policy?.primary_confirmed ?? false,
    secondary_confirmed: policy?.secondary_confirmed ?? false,
    dirty: false,
  };
}
export const validPolicyForm = (form: PolicyForm | null) =>
  !!form &&
  validRetention(form.primary_retention, form.enabled) &&
  validRetention(form.secondary_retention, form.enabled) &&
  (!form.enabled || (form.primary_confirmed && form.secondary_confirmed));
export function createProcessingPolicyController(
  api: Pick<MailProcessingAPI, 'policy' | 'savePolicy'>,
  token: TokenProvider,
  changed: (view: ProcessingPolicyView) => void,
  onDenied: () => void,
) {
  let view = emptyProcessingPolicyView(),
    generation = 0,
    disposed = false,
    mutating = false,
    reading = false;
  let abort: AbortController | undefined;
  const publish = (patch: Partial<ProcessingPolicyView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  const allowed = () => !disposed && view.status !== 'denied';
  const begin = () => {
    abort?.abort();
    abort = new AbortController();
    reading = false;
    return { seq: ++generation, signal: abort.signal };
  };
  const current = (seq: number) => allowed() && seq === generation;
  const stale = (state: PolicyState, form: PolicyForm) =>
    state.profile?.id !== form.profile_id ||
    (state.policy?.version ?? 0) !== form.expected_version;
  function adopt(state: PolicyState, preserve: boolean) {
    const keep = preserve && view.form;
    publish({
      current: state,
      form: keep ? view.form : formFor(state),
      conflict: !!keep && (view.conflict || stale(state, keep)),
      status: 'ready',
      error: null,
    });
  }
  function fail(error: unknown, seq: number) {
    if (!current(seq)) return;
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    if (code === 'authentication_required' || code === 'access_denied') {
      begin();
      view = { ...emptyProcessingPolicyView(), status: 'denied' };
      changed(view);
      onDenied();
    } else
      publish({
        status: 'error',
        error: code,
        conflict: view.conflict || code === 'conflict',
      });
  }
  async function write(input: PolicyInput, preserve: boolean) {
    if (!allowed() || mutating) return;
    const { seq, signal } = begin();
    mutating = true;
    publish({ status: 'saving', error: null });
    try {
      const result = await api.savePolicy(input, token, signal);
      if (current(seq)) adopt(result, preserve);
    } catch (error) {
      fail(error, seq);
    } finally {
      mutating = false;
    }
  }
  return {
    state: () => view,
    async refresh() {
      if (!allowed() || mutating || reading) return;
      const { seq, signal } = begin();
      reading = true;
      publish({ status: 'loading', error: null });
      try {
        const result = await api.policy(token, signal);
        if (current(seq)) adopt(result, !!view.form?.dirty || view.conflict);
      } catch (error) {
        fail(error, seq);
      } finally {
        if (seq === generation) reading = false;
      }
    },
    edit(patch: Partial<Omit<PolicyInput, 'profile_id' | 'expected_version'>>) {
      if (!allowed() || mutating || !view.form) return;
      publish({
        form: {
          ...view.form,
          ...(patch.primary_retention !== undefined &&
          patch.primary_retention !== view.form.primary_retention
            ? { primary_confirmed: false }
            : {}),
          ...(patch.secondary_retention !== undefined &&
          patch.secondary_retention !== view.form.secondary_retention
            ? { secondary_confirmed: false }
            : {}),
          ...patch,
          dirty: true,
        },
      });
    },
    discard() {
      if (allowed() && !mutating && view.current)
        publish({ form: formFor(view.current), conflict: false, error: null });
    },
    async save() {
      const form = view.form;
      if (
        !form ||
        view.conflict ||
        !validPolicyForm(form) ||
        !view.current ||
        stale(view.current, form)
      )
        return;
      await write(
        {
          expected_version: form.expected_version,
          profile_id: form.profile_id,
          enabled: form.enabled,
          primary_retention: form.primary_retention,
          secondary_retention: form.secondary_retention,
          primary_confirmed: form.primary_confirmed,
          secondary_confirmed: form.secondary_confirmed,
        },
        false,
      );
    },
    async stop() {
      const policy = view.current?.policy;
      if (!policy?.enabled || policy.profile_id !== view.current?.profile?.id)
        return;
      await write(
        {
          expected_version: policy.version,
          profile_id: policy.profile_id,
          enabled: false,
          primary_retention: policy.primary_retention,
          secondary_retention: policy.secondary_retention,
          primary_confirmed: policy.primary_confirmed,
          secondary_confirmed: policy.secondary_confirmed,
        },
        !!view.form?.dirty || view.conflict,
      );
    },
    dispose() {
      if (disposed) return;
      begin();
      disposed = true;
      view = { ...emptyProcessingPolicyView(), status: 'disposed' };
      changed(view);
    },
  };
}
