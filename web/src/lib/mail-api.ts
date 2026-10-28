import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
export const probeErrors = [
  '',
  'authentication_failed',
  'tls_failed',
  'connection_failed',
  'timeout',
  'response_too_large',
  'folder_limit',
  'invalid_response',
  'credentials_unavailable',
  'cancelled',
  'lease_expired',
] as const;
export type ProbeError = (typeof probeErrors)[number];
export type MailProbe = {
  id: string;
  account_id: string;
  state: 'queued' | 'leased' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  error_code: ProbeError;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
};
export type MailFolder = {
  id: string;
  name: string;
  display_name?: string;
  delimiter: string;
  selectable: boolean;
  available: boolean;
  selected: boolean;
};
export type MailAccount = {
  id: string;
  provider: 'fastmail' | 'imap' | 'gmail' | 'outlook';
  host?: string;
  port?: number;
  label: string;
  state: 'unverified' | 'ready' | 'needs_reauth' | 'disconnected';
  version: number;
  credential_version: number;
  checked_at: string | null;
  last_error: ProbeError;
  created_at: string;
  updated_at: string;
  latest_probe: MailProbe | null;
  folders: MailFolder[];
};
export type MailSave = {
  provider?: 'fastmail' | 'imap';
  host?: string;
  port?: number;
  expected_version: number;
  label: string;
  login: string;
  app_password: string;
};
export type OAuthProvider = 'gmail' | 'outlook';
export type OAuthAvailability = { provider: OAuthProvider; ready: boolean };
export type OAuthStart = {
  provider: OAuthProvider;
  label: string;
  expected_version: number;
};
export type ProbeInput = { expected_version: number; request_id: string };
const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === 'object' && !Array.isArray(value);
const uuid = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
const timestamp = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^\d{4}-\d{2}-\d{2}T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(
    value,
  ) &&
  Number.isFinite(Date.parse(value)) &&
  new Date(`${value.slice(0, 10)}T00:00:00Z`).toISOString().slice(0, 10) ===
    value.slice(0, 10);
const nullableTime = (value: unknown) => value === null || timestamp(value);
const version = (value: unknown) =>
  Number.isSafeInteger(value) && (value as number) >= 1;
const reason = (value: unknown): value is ProbeError =>
  typeof value === 'string' && probeErrors.some((code) => code === value);
function invalid(): never {
  throw new ApiError('dependency_unavailable');
}
function parseProbe(value: unknown, accountID: string): MailProbe {
  if (
    !record(value) ||
    !uuid(value.id) ||
    value.account_id !== accountID ||
    typeof value.state !== 'string' ||
    ![
      'queued',
      'leased',
      'running',
      'succeeded',
      'failed',
      'cancelled',
    ].includes(value.state) ||
    !reason(value.error_code) ||
    !timestamp(value.created_at) ||
    !nullableTime(value.started_at) ||
    !nullableTime(value.finished_at)
  )
    return invalid();
  return {
    id: value.id,
    account_id: accountID,
    state: value.state as MailProbe['state'],
    error_code: value.error_code,
    created_at: value.created_at,
    started_at: value.started_at as string | null,
    finished_at: value.finished_at as string | null,
  };
}
function parseFolder(value: unknown): MailFolder {
  if (
    !record(value) ||
    !uuid(value.id) ||
    typeof value.name !== 'string' ||
    (value.display_name !== undefined &&
      typeof value.display_name !== 'string') ||
    typeof value.delimiter !== 'string' ||
    typeof value.selectable !== 'boolean' ||
    typeof value.available !== 'boolean' ||
    typeof value.selected !== 'boolean'
  )
    return invalid();
  return {
    id: value.id,
    name: value.name,
    ...(value.display_name !== undefined
      ? { display_name: value.display_name as string }
      : {}),
    delimiter: value.delimiter,
    selectable: value.selectable,
    available: value.available,
    selected: value.selected,
  };
}
function parseAccount(value: unknown): MailAccount {
  if (
    !record(value) ||
    !uuid(value.id) ||
    typeof value.provider !== 'string' ||
    !['fastmail', 'imap', 'gmail', 'outlook'].includes(value.provider) ||
    (value.host !== undefined &&
      (typeof value.host !== 'string' || value.host.length > 253)) ||
    (value.port !== undefined && value.port !== 993 && value.port !== 143) ||
    typeof value.label !== 'string' ||
    typeof value.state !== 'string' ||
    !['unverified', 'ready', 'needs_reauth', 'disconnected'].includes(
      value.state,
    ) ||
    !version(value.version) ||
    !version(value.credential_version) ||
    !nullableTime(value.checked_at) ||
    !reason(value.last_error) ||
    !timestamp(value.created_at) ||
    !timestamp(value.updated_at) ||
    !Array.isArray(value.folders)
  )
    return invalid();
  const folders = value.folders.map(parseFolder);
  if (new Set(folders.map((folder) => folder.id)).size !== folders.length)
    return invalid();
  return {
    id: value.id,
    provider: value.provider as MailAccount['provider'],
    ...(value.provider === 'imap'
      ? {
          host: value.host as string | undefined,
          port: value.port as number | undefined,
        }
      : {}),
    label: value.label,
    state: value.state as MailAccount['state'],
    version: value.version as number,
    credential_version: value.credential_version as number,
    checked_at: value.checked_at as string | null,
    last_error: value.last_error,
    created_at: value.created_at,
    updated_at: value.updated_at,
    latest_probe:
      value.latest_probe === null
        ? null
        : parseProbe(value.latest_probe, value.id),
    folders,
  };
}
export function createMailAPI(base: string, fetcher: typeof fetch = fetch) {
  async function request(
    path: string,
    token: TokenProvider,
    signal?: AbortSignal,
    method = 'GET',
    body?: object,
    options: { absolutePath?: string; credentials?: RequestCredentials } = {},
  ): Promise<unknown> {
    try {
      const bearer = await token();
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (!bearer) throw new ApiError('authentication_required');
      const headers = new Headers({
        Accept: 'application/json',
        Authorization: `Bearer ${bearer}`,
      });
      if (body) headers.set('Content-Type', 'application/json');
      const response = await fetcher(
        `${base.replace(/\/$/, '')}${options.absolutePath ?? `/v1/mail/accounts${path}`}`,
        {
          method,
          headers,
          signal,
          credentials: options.credentials ?? 'omit',
          cache: 'no-store',
          ...(body ? { body: JSON.stringify(body) } : {}),
        },
      );
      if (!response.ok) {
        const codes: Record<number, ErrorCode> = {
          400: 'invalid_request',
          401: 'authentication_required',
          403: 'access_denied',
          404: 'not_found',
          409: 'conflict',
        };
        throw new ApiError(codes[response.status] ?? 'dependency_unavailable');
      }
      return await response.json();
    } catch (error) {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (error instanceof ApiError) throw error;
      throw new ApiError('dependency_unavailable');
    }
  }
  const path = (id: string) => {
    if (!uuid(id)) throw new ApiError('invalid_request');
    return `/${id}`;
  };
  const accountResult = (body: unknown, id: string) => {
    const result = parseAccount(body);
    if (result.id !== id) return invalid();
    return result;
  };
  return {
    async oauthProviders(
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<OAuthAvailability[]> {
      const body = await request('', token, signal, 'GET', undefined, {
        absolutePath: '/v1/mail/oauth/providers',
      });
      if (
        !record(body) ||
        !Array.isArray(body.providers) ||
        body.providers.length !== 2
      )
        return invalid();
      const providers = body.providers.map(
        (row: unknown): OAuthAvailability => {
          if (
            !record(row) ||
            (row.provider !== 'gmail' && row.provider !== 'outlook') ||
            typeof row.ready !== 'boolean'
          )
            return invalid();
          return { provider: row.provider, ready: row.ready };
        },
      );
      if (new Set(providers.map((row) => row.provider)).size !== 2)
        return invalid();
      return providers;
    },
    async startOAuth(
      id: string,
      input: OAuthStart,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<string> {
      if (input.provider !== 'gmail' && input.provider !== 'outlook')
        throw new ApiError('invalid_request');
      const body = await request(
        `${path(id)}/oauth`,
        token,
        signal,
        'POST',
        {
          provider: input.provider,
          label: input.label,
          expected_version: input.expected_version,
        },
        { credentials: 'include' },
      );
      if (!record(body) || typeof body.authorization_url !== 'string')
        return invalid();
      let target: URL;
      try {
        target = new URL(body.authorization_url);
      } catch {
        return invalid();
      }
      if (
        target.protocol !== 'https:' ||
        target.username ||
        target.password ||
        target.port ||
        target.hash
      )
        return invalid();
      if (
        input.provider === 'gmail'
          ? target.hostname !== 'accounts.google.com' ||
            target.pathname !== '/o/oauth2/v2/auth'
          : target.hostname !== 'login.microsoftonline.com' ||
            target.pathname !== '/consumers/oauth2/v2.0/authorize'
      )
        return invalid();
      return target.href;
    },
    async list(
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<MailAccount[]> {
      const body = await request('', token, signal);
      if (
        !record(body) ||
        !Array.isArray(body.accounts) ||
        body.accounts.length > 10
      )
        return invalid();
      const accounts = body.accounts.map(parseAccount);
      if (new Set(accounts.map((a) => a.id)).size !== accounts.length)
        return invalid();
      return accounts;
    },
    async save(
      id: string,
      input: MailSave,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      if (
        input.provider !== undefined &&
        input.provider !== 'fastmail' &&
        input.provider !== 'imap'
      )
        throw new ApiError('invalid_request');
      return accountResult(
        await request(path(id), token, signal, 'PUT', {
          expected_version: input.expected_version,
          label: input.label,
          login: input.login,
          app_password: input.app_password,
          ...(input.provider ? { provider: input.provider } : {}),
          ...(input.provider === 'imap'
            ? { host: input.host, port: input.port }
            : {}),
        }),
        id,
      );
    },
    async disconnect(
      id: string,
      expected_version: number,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return accountResult(
        await request(`${path(id)}/disconnect`, token, signal, 'POST', {
          expected_version,
        }),
        id,
      );
    },
    async requestProbe(
      id: string,
      input: ProbeInput,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseProbe(
        await request(`${path(id)}/probes`, token, signal, 'POST', {
          expected_version: input.expected_version,
          request_id: input.request_id,
        }),
        id,
      );
    },
    async probe(
      id: string,
      probeID: string,
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return parseProbe(
        await request(`${path(id)}/probes${path(probeID)}`, token, signal),
        id,
      );
    },
    async saveFolders(
      id: string,
      expected_version: number,
      folder_ids: string[],
      token: TokenProvider,
      signal?: AbortSignal,
    ) {
      return accountResult(
        await request(`${path(id)}/folders`, token, signal, 'PUT', {
          expected_version,
          folder_ids: [...folder_ids],
        }),
        id,
      );
    },
  };
}
type CompleteMailAPI = ReturnType<typeof createMailAPI>;
export type MailAPI = Omit<CompleteMailAPI, 'oauthProviders' | 'startOAuth'> &
  Partial<Pick<CompleteMailAPI, 'oauthProviders' | 'startOAuth'>>;
