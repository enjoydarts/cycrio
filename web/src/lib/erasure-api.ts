import type { TokenProvider } from './api.ts';
import {
  choice,
  createDailyRequest,
  input,
  invalid,
  record,
  uuid,
} from './daily-api.ts';
export type ErasureInput = {
  request_id: string;
  confirmed: true;
  confirmation: '退会する';
};
export type ErasureReceipt = {
  request_id: string;
  status: 'pending' | 'completed';
};
export function createErasureAPI(base: string, fetcher: typeof fetch = fetch) {
  const request = createDailyRequest(base, fetcher);
  const receipt = (value: unknown, id: string): ErasureReceipt => {
    const v = record(value);
    if (uuid(v.request_id) !== id) return invalid();
    return {
      request_id: id,
      status: choice(v.status, ['pending', 'completed']),
    };
  };
  return {
    async request(
      value: ErasureInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      const body = input(() => {
        if (value.confirmed !== true || value.confirmation !== '退会する')
          return invalid();
        return {
          request_id: uuid(value.request_id),
          confirmed: true,
          confirmation: '退会する',
        };
      });
      return receipt(
        await request('/v1/me/withdrawal', token, signal, 'POST', body),
        body.request_id,
      );
    },
    async status(id: string, token: TokenProvider, signal?: AbortSignal) {
      const requestID = input(() => uuid(id));
      return receipt(
        await request(
          `/v1/me/withdrawal?request_id=${requestID}`,
          token,
          signal,
        ),
        requestID,
      );
    },
  };
}
export type ErasureAPI = ReturnType<typeof createErasureAPI>;
