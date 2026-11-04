import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import type { ErasureAPI, ErasureReceipt } from './erasure-api.ts';
export type ErasureView = {
  confirmation: string;
  confirmed: boolean;
  busy: boolean;
  uncertain: boolean;
  accepted: boolean;
  error: ErrorCode | null;
};
export function createErasureController(
  api: ErasureAPI,
  token: TokenProvider,
  changed: (view: ErasureView) => void,
  signOut: () => void | Promise<void>,
  nonce: () => string = () => crypto.randomUUID(),
) {
  let view: ErasureView = {
    confirmation: '',
    confirmed: false,
    busy: false,
    uncertain: false,
    accepted: false,
    error: null,
  };
  let disposed = false;
  let requestID: string | null = null;
  let sent = false;
  let abort: AbortController | undefined;
  const publish = (patch: Partial<ErasureView>) => {
    if (!disposed) {
      view = { ...view, ...patch };
      changed(view);
    }
  };
  const accept = async (receipt: ErasureReceipt) => {
    if (disposed) return;
    if (
      receipt.request_id !== requestID ||
      !['pending', 'completed'].includes(receipt.status)
    )
      throw new ApiError('dependency_unavailable');
    publish({
      accepted: true,
      busy: false,
      uncertain: false,
      confirmation: '',
      confirmed: false,
      error: null,
    });
    await signOut();
  };
  const fail = (error: unknown) => {
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    publish({ busy: false, uncertain: !view.accepted, error: code });
  };
  return {
    state: () => view,
    confirm(confirmation: string, confirmed: boolean) {
      if (!disposed && !sent && !view.busy)
        publish({ confirmation, confirmed, error: null });
    },
    async submit() {
      if (
        disposed ||
        sent ||
        view.busy ||
        !view.confirmed ||
        view.confirmation !== '退会する'
      )
        return;
      requestID = nonce();
      sent = true;
      abort = new AbortController();
      publish({ busy: true, error: null });
      try {
        await accept(
          await api.request(
            {
              request_id: requestID,
              confirmed: true,
              confirmation: '退会する',
            },
            token,
            abort.signal,
          ),
        );
      } catch (error) {
        if (!disposed) fail(error);
      }
    },
    async reconcile() {
      if (disposed || view.busy || !view.uncertain || !requestID) return;
      abort = new AbortController();
      publish({ busy: true, error: null });
      try {
        await accept(await api.status(requestID, token, abort.signal));
      } catch (error) {
        if (!disposed) fail(error);
      }
    },
    dispose() {
      disposed = true;
      abort?.abort();
      requestID = null;
      view = { ...view, confirmation: '', confirmed: false };
    },
  };
}
