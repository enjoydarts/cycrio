import { ApiError, type TokenProvider } from './api.ts';
import { modelProviders, type ModelProvider } from './catalog-api.ts';

export const catalogChangeKinds = [
  'added',
  'updated',
  'retired',
  'restored',
  'price_changed',
  'deleted',
] as const;
export type CatalogChangeKind = (typeof catalogChangeKinds)[number];
export const catalogChangeNames: Record<CatalogChangeKind, string> = {
  added: 'モデル追加',
  updated: 'モデル情報の変更',
  retired: '提供終了を検知',
  restored: '提供再開を検知',
  price_changed: '料金の変更',
  deleted: '管理者による削除',
};
type SnapshotPrice = {
  currency: string;
  source: string;
  source_url: string;
  rates: Record<string, { amount: string; unit: string }>;
};
export type CatalogSnapshot = {
  display_name: string;
  enabled: boolean;
  available: boolean;
  text_input: boolean | null;
  structured_output: boolean | null;
  context_length: number | null;
  max_input_tokens: number | null;
  max_output_tokens: number | null;
  price: SnapshotPrice | null;
};
export type CatalogChange = {
  id: string;
  model_id: string;
  provider: ModelProvider;
  model_name: string;
  upstream_model_id: string;
  kind: CatalogChangeKind;
  occurred_at: string;
  source_url: string;
  before: CatalogSnapshot | null;
  after: CatalogSnapshot;
};
export type CatalogChangesFilter = {
  provider?: ModelProvider | '';
  model_id?: string;
  kind?: CatalogChangeKind | '';
  offset?: number;
};
export type CatalogChangesPage = {
  changes: CatalogChange[];
  total: number;
  limit: number;
  offset: number;
};
const invalid = (): never => {
  throw new ApiError('dependency_unavailable');
};
const object = (value: unknown): Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : invalid();
const text = (value: unknown): string =>
  typeof value === 'string' ? value : invalid();
const integer = (value: unknown): number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
    ? value
    : invalid();
const bool = (value: unknown): boolean =>
  typeof value === 'boolean' ? value : invalid();
const nullable = <T>(value: unknown, parse: (value: unknown) => T): T | null =>
  value === null ? null : parse(value);
const choice = <T extends string>(value: unknown, choices: readonly T[]): T =>
  typeof value === 'string' && choices.includes(value as T)
    ? (value as T)
    : invalid();
const uuid = (value: unknown): string =>
  typeof value === 'string' &&
  /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(value)
    ? value
    : invalid();
const timestamp = (value: unknown): string =>
  typeof value === 'string' &&
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(
    value,
  ) &&
  Number.isFinite(Date.parse(value))
    ? value
    : invalid();
function snapshot(value: unknown): CatalogSnapshot {
  const v = object(value);
  let price: SnapshotPrice | null = null;
  if (v.price !== null) {
    const p = object(v.price),
      rates: SnapshotPrice['rates'] = {};
    const currency = text(p.currency);
    if (!/^[A-Z]{3}$/.test(currency)) return invalid();
    for (const [name, value] of Object.entries(object(p.rates))) {
      const rate = object(value),
        amount = text(rate.amount),
        unit = text(rate.unit);
      if (!/^(0|[1-9]\d*)(?:\.\d+)?$/.test(amount) || amount.length > 160)
        return invalid();
      rates[name] = { amount, unit };
    }
    price = {
      currency,
      rates,
      source: choice(p.source, ['api', 'manual', 'research']),
      source_url: text(p.source_url),
    };
  }
  return {
    display_name: text(v.display_name),
    enabled: bool(v.enabled),
    available: bool(v.available),
    text_input: nullable(v.text_input, bool),
    structured_output: nullable(v.structured_output, bool),
    context_length: nullable(v.context_length, integer),
    max_input_tokens: nullable(v.max_input_tokens, integer),
    max_output_tokens: nullable(v.max_output_tokens, integer),
    price,
  };
}
export function parseCatalogChanges(value: unknown): CatalogChangesPage {
  const v = object(value);
  if (!Array.isArray(v.changes)) return invalid();
  const limit = integer(v.limit);
  if (limit < 1 || limit > 100 || v.changes.length > limit) return invalid();
  return {
    total: integer(v.total),
    limit,
    offset: integer(v.offset),
    changes: v.changes.map((value) => {
      const c = object(value);
      return {
        id: uuid(c.id),
        model_id: uuid(c.model_id),
        provider: choice(c.provider, modelProviders),
        model_name: text(c.model_name),
        upstream_model_id: text(c.upstream_model_id),
        kind: choice(c.kind, catalogChangeKinds),
        occurred_at: timestamp(c.occurred_at),
        source_url: text(c.source_url),
        before: nullable(c.before, snapshot),
        after: snapshot(c.after),
      };
    }),
  };
}
export function safeCatalogSource(value: string): string | null {
  try {
    const url = new URL(value),
      host = url.hostname.replace(/^www\./, '');
    if (
      url.protocol !== 'https:' ||
      url.username ||
      url.password ||
      url.port ||
      url.search ||
      url.hash ||
      value.length > 2048 ||
      ![
        'openai.com',
        'developers.openai.com',
        'platform.openai.com',
        'api.openai.com',
        'anthropic.com',
        'claude.com',
        'platform.claude.com',
        'api.anthropic.com',
        'liquid.ai',
        'docs.liquid.ai',
        'api.liquid.ai',
        'openrouter.ai',
      ].includes(host)
    )
      return null;
    return value;
  } catch {
    return null;
  }
}
const rateNames: Record<string, string> = {
  input: '入力',
  output: '出力',
  cache_read: 'キャッシュ読取',
  cache_write: 'キャッシュ書込（5分）',
  cache_write_30m: 'キャッシュ書込（30分）',
  cache_write_1h: 'キャッシュ書込（1時間）',
  cache_write_12h: 'キャッシュ書込（12時間）',
  request: 'リクエスト',
};
function million(amount: string): string {
  const [whole, fraction = ''] = amount.split('.');
  const digits = whole + fraction.padEnd(6, '0');
  const cut = whole.length + 6;
  const head = digits.slice(0, cut).replace(/^0+(?=\d)/, ''),
    tail = digits.slice(cut).replace(/0+$/, '');
  return head + (tail ? `.${tail}` : '');
}
function priceText(price: SnapshotPrice | null): string {
  if (!price) return '不明';
  const rates = Object.entries(price.rates).map(
    ([name, rate]) =>
      `${rateNames[name] ?? name}: ${rate.unit === `${price.currency}/token` ? `${million(rate.amount)} ${price.currency} / 100万トークン` : `${rate.amount} ${rate.unit}`}`,
  );
  const source =
    { api: 'API取得', manual: '手入力', research: '公式価格調査' }[
      price.source
    ] ?? price.source;
  return `${source} · ${rates.length ? rates.join(' / ') : '料金不明'}`;
}
export function describeCatalogChanges(
  change: CatalogChange,
): { label: string; before: string; after: string }[] {
  const fields = [
    ['display_name', '表示名'],
    ['enabled', '利用許可'],
    ['available', '利用可能'],
    ['text_input', 'テキスト入力'],
    ['structured_output', '構造化出力'],
    ['context_length', 'コンテキスト長'],
    ['max_input_tokens', '最大入力トークン'],
    ['max_output_tokens', '最大出力トークン'],
    ['price', '料金'],
  ] as const;
  const format = (
    key: keyof CatalogSnapshot,
    value: CatalogSnapshot[keyof CatalogSnapshot] | undefined,
  ): string =>
    key === 'price'
      ? priceText(value as SnapshotPrice | null)
      : value == null
        ? '不明'
        : typeof value === 'boolean'
          ? value
            ? 'はい'
            : 'いいえ'
          : typeof value === 'number'
            ? value.toLocaleString('ja-JP')
            : String(value);
  return fields
    .filter(
      ([key]) =>
        !change.before ||
        JSON.stringify(change.before[key]) !==
          JSON.stringify(change.after[key]),
    )
    .map(([key, label]) => ({
      label,
      before: change.before ? format(key, change.before[key]) : '未登録',
      after: format(key, change.after[key]),
    }));
}
export function createCatalogChangesAPI(
  base: string,
  fetcher: typeof fetch = fetch,
) {
  return {
    async list(
      filter: CatalogChangesFilter,
      token: TokenProvider,
      signal?: AbortSignal,
    ): Promise<CatalogChangesPage> {
      try {
        const bearer = await token();
        if (!bearer) throw new ApiError('authentication_required');
        if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
        const query = new URLSearchParams({
          limit: '20',
          offset: String(filter.offset ?? 0),
        });
        for (const key of ['provider', 'kind', 'model_id'] as const)
          if (filter[key]) query.set(key, filter[key]);
        const response = await fetcher(
          `${base.replace(/\/$/, '')}/v1/admin/ai/catalog-changes?${query}`,
          {
            signal,
            credentials: 'omit',
            cache: 'no-store',
            headers: {
              Accept: 'application/json',
              Authorization: `Bearer ${bearer}`,
            },
          },
        );
        if (!response.ok)
          throw new ApiError(
            response.status === 401
              ? 'authentication_required'
              : response.status === 403
                ? 'access_denied'
                : response.status === 400
                  ? 'invalid_request'
                  : 'dependency_unavailable',
          );
        return parseCatalogChanges(await response.json());
      } catch (error) {
        if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
        if (error instanceof ApiError) throw error;
        return invalid();
      }
    },
  };
}
export type CatalogChangesAPI = ReturnType<typeof createCatalogChangesAPI>;
