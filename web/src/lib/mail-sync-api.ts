import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import { probeErrors, type ProbeError } from './mail-api.ts';
export type SyncRun = {
  id: string;
  account_id: string;
  state:
    | 'queued'
    | 'leased'
    | 'running'
    | 'completed'
    | 'limited'
    | 'failed'
    | 'cancelled';
  since: string;
  before: string;
  max_messages: number;
  examined_count: number;
  added_count: number;
  folder_count: number;
  completed_folders: number;
  error_code: ProbeError;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
};
export type SyncInput = {
  expected_version: number;
  request_id: string;
  since: string;
  before: string;
  max_messages: number;
};
export type MailSyncAPI = {
  list(
    id: string,
    token: TokenProvider,
    signal?: AbortSignal,
  ): Promise<SyncRun[]>;
  start(
    id: string,
    input: SyncInput,
    token: TokenProvider,
    signal?: AbortSignal,
  ): Promise<SyncRun>;
  cancel(
    id: string,
    runID: string,
    token: TokenProvider,
    signal?: AbortSignal,
  ): Promise<SyncRun>;
};
const uuid = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === 'object' && !Array.isArray(value);
const date = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^(?:19\d{2}|[2-9]\d{3})-\d{2}-\d{2}$/.test(value) &&
  Number.isFinite(Date.parse(`${value}T00:00:00Z`)) &&
  new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value;
const time = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^\d{4}-\d{2}-\d{2}T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(
    value,
  ) &&
  date(value.slice(0, 10)) &&
  Number.isFinite(Date.parse(value));
const count = (value: unknown, min = 0, max = 1000): value is number =>
  Number.isSafeInteger(value) &&
  (value as number) >= min &&
  (value as number) <= max;
const invalid = (): never => {
  throw new ApiError('dependency_unavailable');
};
export function validSyncRange(
  since: string,
  before: string,
  max: string,
): boolean {
  if (
    !date(since) ||
    !date(before) ||
    !/^\d+$/.test(max) ||
    !count(Number(max), 1)
  )
    return false;
  const days =
    (Date.parse(`${before}T00:00:00Z`) - Date.parse(`${since}T00:00:00Z`)) /
    86400000;
  return days >= 1 && days <= 366;
}
function parseRun(value: unknown, accountID: string): SyncRun {
  if (
    !record(value) ||
    !uuid(value.id) ||
    value.account_id !== accountID ||
    typeof value.state !== 'string' ||
    ![
      'queued',
      'leased',
      'running',
      'completed',
      'limited',
      'failed',
      'cancelled',
    ].includes(value.state) ||
    typeof value.error_code !== 'string' ||
    !probeErrors.some((code) => code === value.error_code) ||
    !count(value.max_messages, 1) ||
    !date(value.since) ||
    !date(value.before) ||
    !validSyncRange(value.since, value.before, String(value.max_messages)) ||
    !count(value.examined_count, 0, value.max_messages) ||
    !count(value.added_count, 0, value.examined_count) ||
    !count(value.folder_count, 1) ||
    !count(value.completed_folders, 0, value.folder_count) ||
    !time(value.created_at) ||
    !(value.started_at === null || time(value.started_at)) ||
    !(value.finished_at === null || time(value.finished_at))
  )
    return invalid();
  return {
    id: value.id,
    account_id: accountID,
    state: value.state as SyncRun['state'],
    error_code: value.error_code as ProbeError,
    since: value.since as string,
    before: value.before as string,
    max_messages: value.max_messages,
    examined_count: value.examined_count,
    added_count: value.added_count,
    folder_count: value.folder_count,
    completed_folders: value.completed_folders,
    created_at: value.created_at,
    started_at: value.started_at as string | null,
    finished_at: value.finished_at as string | null,
  };
}
export function createMailSyncAPI(
  base: string,
  fetcher: typeof fetch = fetch,
): MailSyncAPI {
  const path = (id: string) => {
    if (!uuid(id)) throw new ApiError('invalid_request');
    return `/${id}`;
  };
  async function request(
    id: string,
    token: TokenProvider,
    signal?: AbortSignal,
    body?: object,
    suffix = '',
  ): Promise<unknown> {
    const url = `${base.replace(/\/$/, '')}/v1/mail/accounts${path(id)}/sync-runs${suffix}`;
    try {
      const bearer = await token();
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (!bearer) throw new ApiError('authentication_required');
      const headers = new Headers({
        Accept: 'application/json',
        Authorization: `Bearer ${bearer}`,
      });
      if (body) headers.set('Content-Type', 'application/json');
      const response = await fetcher(url, {
        method: body ? 'POST' : 'GET',
        headers,
        credentials: 'omit',
        cache: 'no-store',
        signal,
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
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
  return {
    async list(id, token, signal) {
      const body = await request(id, token, signal);
      if (!record(body) || !Array.isArray(body.runs) || body.runs.length > 20)
        return invalid();
      const runs = body.runs.map((value) => parseRun(value, id));
      if (new Set(runs.map((run) => run.id)).size !== runs.length)
        return invalid();
      return runs;
    },
    async start(id, input, token, signal) {
      if (
        !count(input.expected_version, 1, Number.MAX_SAFE_INTEGER) ||
        !uuid(input.request_id) ||
        !validSyncRange(
          input.since,
          input.before,
          String(input.max_messages),
        ) ||
        !count(input.max_messages, 1)
      )
        throw new ApiError('invalid_request');
      return parseRun(
        await request(id, token, signal, {
          expected_version: input.expected_version,
          request_id: input.request_id,
          since: input.since,
          before: input.before,
          max_messages: input.max_messages,
        }),
        id,
      );
    },
    async cancel(id, runID, token, signal) {
      const run = parseRun(
        await request(id, token, signal, {}, `${path(runID)}/cancel`),
        id,
      );
      if (run.id !== runID) return invalid();
      return run;
    },
  };
}
