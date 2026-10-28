export type ErrorCode =
  | 'authentication_required'
  | 'access_denied'
  | 'dependency_unavailable'
  | 'invalid_request'
  | 'conflict'
  | 'not_found';
export const credentialProviders = [
  'liquid',
  'openrouter',
  'openai',
  'anthropic',
  'resend',
  'clerk_webhook',
  'resend_webhook',
  'gmail_oauth',
  'outlook_oauth',
] as const;
export type CredentialProvider = (typeof credentialProviders)[number];
export type CredentialSummary = {
  provider: CredentialProvider;
  state: 'not_configured' | 'configured' | 'disabled';
  version: number;
  updated_at: string | null;
};
export type AuditEntry = {
  id: string;
  actor_id: string;
  action: 'credential_created' | 'credential_updated' | 'credential_disabled';
  provider: CredentialProvider;
  version: number;
  outcome: 'success';
  created_at: string;
};
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

function isProvider(value: unknown): value is CredentialProvider {
  return credentialProviders.some((provider) => provider === value);
}

function isTimestamp(value: unknown): value is string {
  if (
    typeof value !== 'string' ||
    !/^\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(
      value,
    )
  )
    return false;
  const date = new Date(value);
  const calendar = new Date(`${value.slice(0, 10)}T00:00:00Z`);
  return (
    Number.isFinite(date.getTime()) &&
    Number.isFinite(calendar.getTime()) &&
    calendar.toISOString().slice(0, 10) === value.slice(0, 10)
  );
}

function summary(value: unknown): CredentialSummary {
  if (
    !isRecord(value) ||
    !isProvider(value.provider) ||
    typeof value.state !== 'string' ||
    !['not_configured', 'configured', 'disabled'].includes(value.state) ||
    !Number.isSafeInteger(value.version) ||
    (value.version as number) < 0 ||
    !(value.updated_at === null || isTimestamp(value.updated_at)) ||
    (value.state === 'not_configured'
      ? value.version !== 0 || value.updated_at !== null
      : value.version === 0 || value.updated_at === null)
  )
    throw new ApiError('dependency_unavailable');
  return {
    provider: value.provider,
    state: value.state as CredentialSummary['state'],
    version: value.version as number,
    updated_at: value.updated_at,
  };
}

function auditEntry(value: unknown): AuditEntry {
  if (
    !isRecord(value) ||
    typeof value.id !== 'string' ||
    !value.id ||
    typeof value.actor_id !== 'string' ||
    !value.actor_id ||
    typeof value.action !== 'string' ||
    ![
      'credential_created',
      'credential_updated',
      'credential_disabled',
    ].includes(value.action) ||
    !isProvider(value.provider) ||
    !Number.isSafeInteger(value.version) ||
    (value.version as number) < 1 ||
    value.outcome !== 'success' ||
    !isTimestamp(value.created_at)
  )
    throw new ApiError('dependency_unavailable');
  return {
    id: value.id,
    actor_id: value.actor_id,
    action: value.action as AuditEntry['action'],
    provider: value.provider,
    version: value.version as number,
    outcome: 'success',
    created_at: value.created_at,
  };
}

export function createAPI(base: string, fetcher: typeof fetch = fetch) {
  async function request(
    path: string,
    token?: TokenProvider,
    signal?: AbortSignal,
    method = 'GET',
    body?: object,
  ): Promise<unknown> {
    try {
      const headers = new Headers({ Accept: 'application/json' });
      if (token) {
        const value = await token();
        if (!value) throw new ApiError('authentication_required');
        headers.set('Authorization', `Bearer ${value}`);
      }
      if (body) headers.set('Content-Type', 'application/json');
      const response = await fetcher(`${base.replace(/\/$/, '')}${path}`, {
        method,
        credentials: 'omit',
        cache: 'no-store',
        headers,
        signal,
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      if (!response.ok) {
        throw new ApiError(
          response.status === 401
            ? 'authentication_required'
            : response.status === 403
              ? 'access_denied'
              : response.status === 400
                ? 'invalid_request'
                : response.status === 404
                  ? 'not_found'
                  : response.status === 409
                    ? 'conflict'
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
    async credentials(
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<CredentialSummary[]> {
      const body = await request('/v1/admin/credentials', token, signal);
      if (
        !isRecord(body) ||
        !Array.isArray(body.credentials) ||
        body.credentials.length !== credentialProviders.length
      )
        throw new ApiError('dependency_unavailable');
      const rows = body.credentials.map(summary);
      if (
        new Set(rows.map((row) => row.provider)).size !==
        credentialProviders.length
      )
        throw new ApiError('dependency_unavailable');
      return credentialProviders.map((provider) =>
        rows.find((row) => row.provider === provider)!,
      );
    },
    async audit(
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<AuditEntry[]> {
      const body = await request('/v1/admin/audit', token, signal);
      if (
        !isRecord(body) ||
        !Array.isArray(body.events) ||
        body.events.length > 50
      )
        throw new ApiError('dependency_unavailable');
      return body.events.map(auditEntry);
    },
    async putCredential(
      provider: CredentialProvider,
      secret: string,
      expected_version: number,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<CredentialSummary> {
      return summary(
        await request(
          `/v1/admin/credentials/${provider}`,
          token,
          signal,
          'PUT',
          { secret, expected_version },
        ),
      );
    },
    async disableCredential(
      provider: CredentialProvider,
      expected_version: number,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<CredentialSummary> {
      return summary(
        await request(
          `/v1/admin/credentials/${provider}`,
          token,
          signal,
          'DELETE',
          { expected_version },
        ),
      );
    },
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
