import {
  ApiError,
  type AuditEntry,
  type CredentialProvider,
  type CredentialSummary,
  type ErrorCode,
  type TokenProvider,
  type createAPI,
} from './api.ts';

export type CredentialsState = {
  status:
    'idle' | 'loading' | 'saving' | 'ready' | 'error' | 'denied' | 'disposed';
  credentials: CredentialSummary[];
  events: AuditEntry[];
  error: ErrorCode | null;
  success: 'saved' | 'disabled' | null;
};

export function validSecret(secret: string): boolean {
  return /^[\x21-\x7e]{8,4096}$/.test(secret);
}

export function serializeOAuthCredential(
  provider: 'gmail_oauth' | 'outlook_oauth',
  fields: { ClientID: string; ClientSecret: string; RedirectURI: string },
): string {
  const { ClientID, ClientSecret, RedirectURI } = fields;
  const fail = (): never => {
    throw new ApiError('invalid_request');
  };
  if (!/^[\x21-\x7e]+$/.test(ClientID) || !/^[\x21-\x7e]+$/.test(ClientSecret))
    return fail();
  let uri: URL;
  try {
    uri = new URL(RedirectURI);
  } catch {
    return fail();
  }
  const name = provider === 'gmail_oauth' ? 'gmail' : 'outlook';
  if (
    (uri.protocol !== 'https:' &&
      !(
        uri.protocol === 'http:' &&
        ['localhost', '127.0.0.1'].includes(uri.hostname)
      )) ||
    uri.username ||
    uri.password ||
    RedirectURI.includes('?') ||
    RedirectURI.includes('#') ||
    uri.pathname !== `/v1/mail/oauth/callback/${name}`
  )
    return fail();
  const serialized = JSON.stringify({ ClientID, ClientSecret, RedirectURI });
  if (!validSecret(serialized)) return fail();
  return serialized;
}

export function createCredentialsController(
  api: ReturnType<typeof createAPI>,
  token: TokenProvider,
  changed: (state: CredentialsState) => void,
  onDenied: () => void,
) {
  let state: CredentialsState = {
    status: 'idle',
    credentials: [],
    events: [],
    error: null,
    success: null,
  };
  let generation = 0;
  let request = new AbortController();
  const active = () =>
    !['loading', 'saving', 'denied', 'disposed'].includes(state.status);
  function emit(next: CredentialsState) {
    state = next;
    changed(next);
  }
  function begin(status: 'loading' | 'saving') {
    request.abort();
    request = new AbortController();
    const id = ++generation;
    emit({ ...state, status, error: null, success: null });
    return { id, signal: request.signal };
  }
  async function read(signal: AbortSignal) {
    const credentials = await api.credentials(token, signal);
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError');
    const events = await api.audit(token, signal);
    return { credentials, events };
  }
  function fail(error: unknown, id: number) {
    if (generation !== id) return;
    const code =
      error instanceof ApiError ? error.code : 'dependency_unavailable';
    if (code === 'authentication_required' || code === 'access_denied') {
      request.abort();
      generation++;
      emit({
        status: 'denied',
        credentials: [],
        events: [],
        error: code,
        success: null,
      });
      onDenied();
    } else {
      emit({
        status: 'error',
        credentials: [],
        events: [],
        error: code,
        success: null,
      });
    }
  }
  async function load() {
    if (!active()) return;
    const current = begin('loading');
    try {
      const data = await read(current.signal);
      if (generation === current.id)
        emit({ ...data, status: 'ready', error: null, success: null });
    } catch (error) {
      fail(error, current.id);
    }
  }
  async function mutate(
    provider: CredentialProvider,
    version: number,
    secret?: string,
  ) {
    if (!active()) return;
    if (secret !== undefined && !validSecret(secret)) {
      emit({ ...state, error: 'invalid_request', success: null });
      return;
    }
    if (
      secret === undefined &&
      !state.credentials.some(
        (row) =>
          row.provider === provider &&
          row.state === 'configured' &&
          row.version === version,
      )
    )
      return;
    const current = begin('saving');
    const success = secret === undefined ? 'disabled' : 'saved';
    let conflict = false;
    try {
      try {
        if (secret === undefined)
          await api.disableCredential(provider, version, token, current.signal);
        else
          await api.putCredential(
            provider,
            secret,
            version,
            token,
            current.signal,
          );
      } catch (error) {
        if (!(error instanceof ApiError) || error.code !== 'conflict')
          throw error;
        conflict = true;
      } finally {
        secret = undefined;
      }
      if (generation !== current.id) return;
      const data = await read(current.signal);
      if (generation === current.id)
        emit({
          ...data,
          status: 'ready',
          error: conflict ? 'conflict' : null,
          success: conflict ? null : success,
        });
    } catch (error) {
      fail(error, current.id);
    }
  }
  return {
    get state() {
      return state;
    },
    load,
    put: (provider: CredentialProvider, secret: string, version: number) =>
      mutate(provider, version, secret),
    disable: (provider: CredentialProvider, version: number) =>
      mutate(provider, version),
    dispose() {
      generation++;
      request.abort();
      state = {
        status: 'disposed',
        credentials: [],
        events: [],
        error: null,
        success: null,
      };
    },
  };
}
