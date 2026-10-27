import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type {
  EmailAttempt,
  IntegrationAPI,
  IntegrationView,
  TestRequest,
} from './integrations-api.ts';
export type IntegrationState = {
  busy: boolean;
  view: IntegrationView | null;
  sender: string;
  instance: string;
  version: number;
  dirty: boolean;
  error: ErrorCode | null;
  ack: EmailAttempt | null;
  pending: TestRequest | null;
  disposed: boolean;
};
export const emptyIntegrationState = (): IntegrationState => ({
  busy: false,
  view: null,
  sender: '',
  instance: '',
  version: 0,
  dirty: false,
  error: null,
  ack: null,
  pending: null,
  disposed: false,
});
export function createIntegrationController(
  api: IntegrationAPI,
  token: TokenProvider,
  changed: (s: IntegrationState) => void,
  onDenied: () => void,
) {
  let state = emptyIntegrationState();
  let generation = 0;
  const abort = new AbortController();
  function emit(p: Partial<IntegrationState>) {
    state = { ...state, ...p };
    changed(state);
  }
  function fail(e: unknown) {
    const code = e instanceof ApiError ? e.code : 'dependency_unavailable';
    emit({ error: code });
    if (code === 'authentication_required' || code === 'access_denied') {
      dispose();
      onDenied();
    }
  }
  async function load() {
    if (state.busy || state.disposed) return;
    const id = ++generation;
    emit({ busy: true, error: null });
    try {
      const view = await api.view(token, abort.signal);
      if (id !== generation) return;
      emit({
        view,
        ...(!state.dirty
          ? {
              sender: view.config.sender,
              instance: view.config.clerk_instance_id,
              version: view.config.version,
            }
          : {}),
      });
    } catch (e) {
      if (id === generation) fail(e);
    } finally {
      if (id === generation) emit({ busy: false });
    }
  }
  async function save() {
    if (state.busy || state.disposed || state.pending) return;
    const id = ++generation;
    emit({ busy: true, error: null });
    try {
      const saved = await api.save(
        {
          expected_version: state.version,
          sender: state.sender,
          clerk_instance_id: state.instance,
        },
        token,
        abort.signal,
      );
      if (id !== generation) return;
      emit({
        version: saved.version,
        dirty: false,
        view: state.view
          ? {
              ...state.view,
              config: saved,
              profile_ready:
                state.view.config.clerk_instance_id ===
                  saved.clerk_instance_id && state.view.profile_ready,
            }
          : null,
      });
    } catch (e) {
      if (id === generation) fail(e);
    } finally {
      if (id === generation) emit({ busy: false });
    }
  }
  async function send(confirm: boolean) {
    if (!confirm || state.busy || state.disposed || !state.view) return;
    if (
      !state.pending &&
      (state.dirty || !state.view.profile_ready || !state.view.resend_ready)
    )
      return;
    const pending = state.pending ?? {
      request_id: crypto.randomUUID(),
      expected_version: state.version,
      confirm: true,
    };
    const id = ++generation;
    emit({ pending, busy: true, error: null });
    try {
      const ack = await api.test(pending, token, abort.signal);
      if (id === generation) emit({ ack });
    } catch (e) {
      if (id === generation) fail(e);
    } finally {
      if (id === generation) emit({ busy: false });
    }
  }
  function dispose() {
    generation++;
    abort.abort();
    emit({ disposed: true, busy: false, view: null, ack: null, pending: null });
  }
  return {
    get state() {
      return state;
    },
    load,
    save,
    send,
    adoptLatestBasis() {
      if (!state.busy && !state.disposed && !state.pending && state.view)
        emit({ version: state.view.config.version, error: null });
    },
    edit(p: { sender?: string; instance?: string }) {
      if (!state.busy && !state.disposed) emit({ ...p, dirty: true });
    },
    newTest() {
      if (!state.busy && !state.disposed)
        emit({ pending: null, ack: null, error: null });
    },
    dispose,
  };
}
