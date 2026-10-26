import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
export const invalid = (): never => {
  throw new ApiError('dependency_unavailable');
};
export const record = (v: unknown): Record<string, unknown> =>
  v !== null && typeof v === 'object' && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : invalid();
export const integer = (
  v: unknown,
  min = 0,
  max = Number.MAX_SAFE_INTEGER,
): number =>
  typeof v === 'number' && Number.isSafeInteger(v) && v >= min && v <= max
    ? v
    : invalid();
export const choice = <T extends string>(
  v: unknown,
  options: readonly T[],
): T =>
  typeof v === 'string' && options.includes(v as T) ? (v as T) : invalid();
export const nullable = <T>(v: unknown, parse: (v: unknown) => T): T | null =>
  v === null ? null : parse(v);
export const text = (v: unknown, max: number, empty = false): string =>
  typeof v === 'string' &&
  [...v].length <= max &&
  (empty || !!v.trim()) &&
  !/[\p{Cc}\p{Cs}]/u.test(v)
    ? v
    : invalid();
export const uuid = (v: unknown): string =>
  typeof v === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(v) &&
  v !== '00000000-0000-0000-0000-000000000000'
    ? v
    : invalid();
export const date = (v: unknown): string =>
  typeof v === 'string' &&
  /^(?!0000)\d{4}-\d{2}-\d{2}$/.test(v) &&
  Number.isFinite(Date.parse(`${v}T00:00:00Z`)) &&
  new Date(`${v}T00:00:00Z`).toISOString().slice(0, 10) === v
    ? v
    : invalid();
export const timestamp = (v: unknown): string => {
  if (
    typeof v !== 'string' ||
    !/^\d{4}-\d{2}-\d{2}T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d{1,9})?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(
      v,
    ) ||
    !Number.isFinite(Date.parse(v))
  )
    return invalid();
  date(v.slice(0, 10));
  return v;
};
export const array = <T>(
  v: unknown,
  parse: (v: unknown) => T,
  max = 100,
): T[] => (Array.isArray(v) && v.length <= max ? v.map(parse) : invalid());
export const input = <T>(parse: () => T): T => {
  try {
    return parse();
  } catch {
    throw new ApiError('invalid_request');
  }
};
export function pagination(v: Record<string, unknown>) {
  return {
    total: integer(v.total),
    limit: integer(v.limit, 1, 100),
    offset: integer(v.offset),
  };
}
export function createDailyRequest(base: string, fetcher: typeof fetch) {
  return async (
    path: string,
    token: TokenProvider,
    signal?: AbortSignal,
    method = 'GET',
    body?: object,
  ): Promise<unknown> => {
    try {
      const bearer = await token();
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (!bearer) throw new ApiError('authentication_required');
      const response = await fetcher(`${base.replace(/\/$/, '')}${path}`, {
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
      return invalid();
    }
  };
}
