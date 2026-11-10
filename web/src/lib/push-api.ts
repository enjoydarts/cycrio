import { ApiError, type TokenProvider } from './api.ts';
import {
  array,
  choice,
  createDailyRequest,
  input,
  invalid,
  record,
  timestamp,
  uuid,
} from './daily-api.ts';
export type PushDevice = {
  id: string;
  state: 'pending' | 'active' | 'disabled' | 'expired';
  created_at: string;
  updated_at: string;
};
export type PushPage = {
  app_id: string;
  available: boolean;
  reason: '' | 'environment_disabled' | 'not_configured';
  devices: PushDevice[];
};
export type PushRegistration = {
  expires_at: string;
  device_id: string;
  challenge_id: string;
  state: 'pending' | 'unknown' | 'failed';
};
export type PushCapability = { device_id: string; revocation_token: string };
export const pushSecret = (v: unknown): string => {
  if (typeof v !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(v)) return invalid();
  const raw = atob(v.replaceAll('-', '+').replaceAll('_', '/') + '=');
  return btoa(raw)
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '') === v
    ? v
    : invalid();
};
export async function deriveRevocationToken(secret: string): Promise<string> {
  const digest = await crypto.subtle.digest(
    'SHA-256',
    new TextEncoder().encode(
      'cycrio-push-revocation/v1\x00' + pushSecret(secret),
    ),
  );
  return btoa(String.fromCharCode(...new Uint8Array(digest)))
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '');
}
function exact(v: Record<string, unknown>, keys: string[]) {
  if (Object.keys(v).sort().join() !== keys.sort().join()) invalid();
}
export function parsePushDevice(value: unknown): PushDevice {
  const v = record(value);
  exact(v, ['id', 'state', 'created_at', 'updated_at']);
  return {
    id: uuid(v.id),
    state: choice(v.state, ['pending', 'active', 'disabled', 'expired']),
    created_at: timestamp(v.created_at),
    updated_at: timestamp(v.updated_at),
  };
}
export function parsePushPage(value: unknown): PushPage {
  const v = record(value);
  exact(v, ['app_id', 'available', 'reason', 'devices']);
  if (typeof v.available !== 'boolean') return invalid();
  const available = v.available,
    reason = choice(v.reason, ['', 'environment_disabled', 'not_configured']);
  if (available !== (reason === '') || (!available && v.app_id !== ''))
    return invalid();
  const devices = array(v.devices, parsePushDevice, 1000);
  if (new Set(devices.map((d) => d.id)).size !== devices.length)
    return invalid();
  return {
    app_id: available ? uuid(v.app_id) : '',
    available,
    reason,
    devices,
  };
}
export function createPushAPI(base: string, fetcher: typeof fetch = fetch) {
  const request = createDailyRequest(base, fetcher);
  async function stop(
    path: string,
    body: object | undefined,
    token?: TokenProvider,
  ) {
    try {
      const bearer = token ? await token() : null;
      if (token && !bearer) throw new ApiError('authentication_required');
      const response = await fetcher(base.replace(/\/$/, '') + path, {
        method: body ? 'POST' : 'DELETE',
        credentials: 'omit',
        cache: 'no-store',
        headers: {
          ...(bearer ? { Authorization: `Bearer ${bearer}` } : {}),
          ...(body ? { 'Content-Type': 'application/json' } : {}),
        },
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      if (response.status !== 204)
        throw new ApiError(
          (
            {
              401: 'authentication_required',
              403: 'access_denied',
              404: 'not_found',
              409: 'conflict',
            } as const
          )[response.status as 401] ?? 'dependency_unavailable',
        );
    } catch (error) {
      if (error instanceof ApiError) throw error;
      throw new ApiError('dependency_unavailable');
    }
  }
  return {
    async list(token: TokenProvider) {
      return parsePushPage(await request('/v1/push-devices', token));
    },
    async register(
      value: { subscription_id: string; request_id: string },
      token: TokenProvider,
    ): Promise<PushRegistration> {
      const body = input(() => ({
        subscription_id: uuid(value.subscription_id),
        request_id: uuid(value.request_id),
      }));
      const v = record(
        await request('/v1/push-devices', token, undefined, 'POST', body),
      );
      exact(v, ['device_id', 'challenge_id', 'state', 'expires_at']);
      return {
        expires_at: timestamp(v.expires_at),
        device_id: uuid(v.device_id),
        challenge_id: uuid(v.challenge_id),
        state: choice(v.state, ['pending', 'unknown', 'failed']),
      };
    },
    async confirm(
      value: { challenge_id: string; secret: string },
      token: TokenProvider,
    ) {
      const body = input(() => ({
        challenge_id: uuid(value.challenge_id),
        secret: pushSecret(value.secret),
      }));
      const v = record(
        await request(
          '/v1/push-devices/confirm',
          token,
          undefined,
          'POST',
          body,
        ),
      );
      exact(v, ['device', 'revocation_token']);
      const device = parsePushDevice(v.device);
      if (device.state !== 'active') return invalid();
      return { device, revocation_token: pushSecret(v.revocation_token) };
    },
    async disable(id: string, token: TokenProvider) {
      await stop('/v1/push-devices/' + input(() => uuid(id)), undefined, token);
    },
    async revoke(value: PushCapability) {
      const body = input(() => ({
        device_id: uuid(value.device_id),
        revocation_token: pushSecret(value.revocation_token),
      }));
      await stop('/v1/push-device-revocations', body);
    },
  };
}
export type PushAPI = ReturnType<typeof createPushAPI>;
