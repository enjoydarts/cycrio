import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import {
  budgetDecimal,
  type AIBudgetAPI,
  type AIBudgetState,
} from './aibudget-api.ts';
export type BudgetView = {
  owner: string;
  state: AIBudgetState | null;
  amount: string;
  confirmed: boolean;
  busy: boolean;
  uncertain: boolean;
  error: ErrorCode | null;
  saved: boolean;
};
export const emptyBudgetView = (): BudgetView => ({
  owner: '',
  state: null,
  amount: '',
  confirmed: false,
  busy: false,
  uncertain: false,
  error: null,
  saved: false,
});
const equalAmount = (a: string | null, b: string | null) =>
  a === null || b === null
    ? a === b
    : a.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '') ===
      b.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '');
export function createAIBudgetController(
  api: AIBudgetAPI,
  token: TokenProvider,
  changed: (v: BudgetView) => void,
  onDenied: () => void,
) {
  let view = emptyBudgetView(),
    sequence = 0,
    disposed = false,
    abort = new AbortController();
  let pending: {
    owner: string;
    version: number;
    amount: string | null;
  } | null = null;
  const publish = (patch: Partial<BudgetView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  const alive = (seq: number) => !disposed && seq === sequence;
  function failure(error: unknown) {
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    if (code === 'authentication_required' || code === 'access_denied') {
      sequence++;
      pending = null;
      abort.abort();
      publish({ ...emptyBudgetView(), error: code });
      onDenied();
      return;
    }
    publish({ busy: false, error: code, confirmed: false });
  }
  async function load(owner: string) {
    if (disposed) return;
    const seq = ++sequence;
    pending = null;
    abort.abort();
    abort = new AbortController();
    publish({ ...emptyBudgetView(), owner, busy: true });
    try {
      const state = await api.read(owner, token, abort.signal);
      if (alive(seq))
        publish({ state, amount: state.monthly_limit_usd ?? '', busy: false });
    } catch (e) {
      if (alive(seq)) failure(e);
    }
  }
  async function reconcile() {
    if (disposed || !pending || !view.uncertain || view.busy) return;
    const seq = sequence,
      intent = pending;
    publish({ busy: true, error: null });
    try {
      const state = await api.read(intent.owner, token, abort.signal);
      if (!alive(seq)) return;
      if (
        state.version === intent.version + 1 &&
        equalAmount(state.monthly_limit_usd, intent.amount)
      ) {
        pending = null;
        publish({
          state,
          amount: state.monthly_limit_usd ?? '',
          busy: false,
          uncertain: false,
          saved: true,
        });
      } else
        publish({
          busy: false,
          error:
            state.version === intent.version
              ? 'dependency_unavailable'
              : 'conflict',
        });
    } catch (error) {
      if (alive(seq)) failure(error);
    }
  }
  return {
    load,
    reconcile,
    edit(amount: string) {
      if (view.busy || view.uncertain || !view.state) return;
      publish({ amount, confirmed: false, saved: false, error: null });
    },
    confirm(confirmed: boolean) {
      if (!view.busy && !view.uncertain) publish({ confirmed });
    },
    async save() {
      if (
        disposed ||
        view.busy ||
        view.uncertain ||
        !view.confirmed ||
        !view.state
      )
        return;
      let amount: string | null;
      try {
        amount = view.amount === '' ? null : budgetDecimal(view.amount);
      } catch {
        publish({ error: 'invalid_request' });
        return;
      }
      const seq = sequence,
        owner = view.owner,
        version = view.state.version;
      publish({ busy: true, error: null, saved: false });
      try {
        const state = await api.save(
          owner,
          {
            expected_version: version,
            monthly_limit_usd: amount,
            confirmed: true,
          },
          token,
          abort.signal,
        );
        if (alive(seq))
          publish({
            state,
            amount: state.monthly_limit_usd ?? '',
            confirmed: false,
            busy: false,
            saved: true,
          });
      } catch (e) {
        if (!alive(seq)) return;
        if (e instanceof ApiError && e.code !== 'dependency_unavailable') {
          failure(e);
          return;
        }
        pending = { owner, version, amount };
        publish({ uncertain: true, confirmed: false, busy: false });
        await reconcile();
      }
    },
    dispose() {
      disposed = true;
      pending = null;
      sequence++;
      abort.abort();
    },
  };
}
