import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import {
  routeRecord,
  routeText,
  routeInt,
  routeBool,
  routeID,
  routeTime,
  routeNullable,
  routeArray,
  invalidRoute,
} from './ai-routes-api.ts';
export type IntegrationConfig = {
  version: number;
  sender: string;
  clerk_instance_id: string;
};
export type EmailAttempt = {
  id: string;
  request_id: string;
  state: 'unknown' | 'accepted' | 'failed';
  created_at: string;
  sent: boolean;
  delivered: boolean;
  bounced: boolean;
  complained: boolean;
  failed: boolean;
};
export type IntegrationView = {
  config: IntegrationConfig;
  profile_ready: boolean;
  profile_synced_at: string | null;
  resend_ready: boolean;
  clerk_webhook_ready: boolean;
  resend_webhook_ready: boolean;
  attempts: EmailAttempt[];
};
export type TestRequest = {
  request_id: string;
  expected_version: number;
  confirm: boolean;
};
const config = (v: unknown): IntegrationConfig => {
  const x = routeRecord(v);
  return {
    version: routeInt(x.version),
    sender: routeText(x.sender),
    clerk_instance_id: routeText(x.clerk_instance_id),
  };
};
export function parseEmailAttempt(v: unknown): EmailAttempt {
  const x = routeRecord(v);
  const state = routeText(x.state);
  if (!['unknown', 'accepted', 'failed'].includes(state)) return invalidRoute();
  return {
    id: routeID(x.id),
    request_id: routeID(x.request_id),
    state: state as EmailAttempt['state'],
    created_at: routeTime(x.created_at),
    sent: routeBool(x.sent),
    delivered: routeBool(x.delivered),
    bounced: routeBool(x.bounced),
    complained: routeBool(x.complained),
    failed: routeBool(x.failed),
  };
}
export function parseIntegrationView(v: unknown): IntegrationView {
  const x = routeRecord(v);
  return {
    config: config(x.config),
    profile_ready: routeBool(x.profile_ready),
    profile_synced_at: routeNullable(x.profile_synced_at, routeTime),
    resend_ready: routeBool(x.resend_ready),
    clerk_webhook_ready: routeBool(x.clerk_webhook_ready),
    resend_webhook_ready: routeBool(x.resend_webhook_ready),
    attempts: routeArray(x.attempts, parseEmailAttempt, 30),
  };
}
export function createIntegrationAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  async function request(
    method: string,
    path: string,
    token: TokenProvider,
    signal?: AbortSignal,
    body?: unknown,
  ): Promise<unknown> {
    try {
      const bearer = await token();
      if (!bearer) throw new ApiError('authentication_required');
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      const r = await fetcher(
        `${base.replace(/\/$/, '')}/v1/admin/integrations${path}`,
        {
          method,
          signal,
          credentials: 'omit',
          cache: 'no-store',
          headers: {
            Accept: 'application/json',
            Authorization: `Bearer ${bearer}`,
            ...(body ? { 'Content-Type': 'application/json' } : {}),
          },
          ...(body ? { body: JSON.stringify(body) } : {}),
        },
      );
      if (!r.ok) {
        const errors: Record<number, ErrorCode> = {
          400: 'invalid_request',
          401: 'authentication_required',
          403: 'access_denied',
          409: 'conflict',
        };
        throw new ApiError(errors[r.status] ?? 'dependency_unavailable');
      }
      return await r.json();
    } catch (e) {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (e instanceof ApiError) throw e;
      throw new ApiError('dependency_unavailable');
    }
  }
  return {
    view: async (token: TokenProvider, signal?: AbortSignal) =>
      parseIntegrationView(await request('GET', '', token, signal)),
    save: async (
      input: {
        expected_version: number;
        sender: string;
        clerk_instance_id: string;
      },
      token: TokenProvider,
      signal?: AbortSignal,
    ) => config(await request('PUT', '', token, signal, input)),
    test: async (
      input: TestRequest,
      token: TokenProvider,
      signal?: AbortSignal,
    ) =>
      parseEmailAttempt(
        await request('POST', '/test', token, signal, {
          request_id: input.request_id,
          expected_version: input.expected_version,
          confirm: input.confirm,
        }),
      ),
  };
}
export type IntegrationAPI = ReturnType<typeof createIntegrationAPI>;
