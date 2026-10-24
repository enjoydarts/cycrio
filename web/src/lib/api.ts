export type ErrorCode =
  'authentication_required' | 'access_denied' | 'dependency_unavailable';
export type Role = 'user' | 'platform_admin';
export type CurrentUser = { id: string; role: Role };
export type PublicConfiguration = {
  configured: boolean;
  clerk_publishable_key: string;
};
export type TokenProvider = () => Promise<string | null>;

export class ApiError extends Error {
  readonly code: ErrorCode;

  constructor(code: ErrorCode) {
    super(code);
    this.name = 'ApiError';
    this.code = code;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

export function createAPI(base: string, fetcher: typeof fetch = fetch) {
  async function request(
    path: string,
    token?: TokenProvider,
    signal?: AbortSignal,
  ): Promise<unknown> {
    try {
      const headers = new Headers({ Accept: 'application/json' });
      if (token) {
        const value = await token();
        if (!value) throw new ApiError('authentication_required');
        headers.set('Authorization', `Bearer ${value}`);
      }
      const response = await fetcher(`${base.replace(/\/$/, '')}${path}`, {
        method: 'GET',
        credentials: 'omit',
        cache: 'no-store',
        headers,
        signal,
      });
      if (!response.ok) {
        throw new ApiError(
          response.status === 401
            ? 'authentication_required'
            : response.status === 403
              ? 'access_denied'
              : 'dependency_unavailable',
        );
      }
      return await response.json();
    } catch (error) {
      if (error instanceof ApiError) throw error;
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      throw new ApiError('dependency_unavailable');
    }
  }

  return {
    async configuration(signal?: AbortSignal): Promise<PublicConfiguration> {
      const body = await request('/v1/public/config', undefined, signal);
      if (
        !isRecord(body) ||
        typeof body.configured !== 'boolean' ||
        typeof body.clerk_publishable_key !== 'string' ||
        (body.configured && !body.clerk_publishable_key)
      ) {
        throw new ApiError('dependency_unavailable');
      }
      return {
        configured: body.configured,
        clerk_publishable_key: body.clerk_publishable_key,
      };
    },
    async me(token: TokenProvider, signal?: AbortSignal): Promise<CurrentUser> {
      const body = await request('/v1/me', token, signal);
      if (
        !isRecord(body) ||
        typeof body.id !== 'string' ||
        !body.id ||
        (body.role !== 'user' && body.role !== 'platform_admin')
      ) {
        throw new ApiError('dependency_unavailable');
      }
      return { id: body.id, role: body.role };
    },
    async adminStatus(
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<{ role: 'platform_admin' }> {
      const body = await request('/v1/admin/status', token, signal);
      if (!isRecord(body) || body.role !== 'platform_admin') {
        throw new ApiError('dependency_unavailable');
      }
      return { role: 'platform_admin' };
    },
  };
}
