import { ApiError, type ErrorCode, type TokenProvider } from './api.ts';
import {
  decimal,
  priceFields,
  type CatalogAPI,
  type CatalogFilter,
  type Model,
  type ModelInput,
  type ModelUpdate,
  type Price,
  type PriceInput,
  type SettingsState,
  type SyncInput,
  type SyncState,
  type ModelProvider,
} from './catalog-api.ts';

export const providerNames = {
  liquid: 'Liquid AI（D1）',
  openrouter: 'OpenRouter',
  openai: 'OpenAI',
  anthropic: 'Anthropic',
};
export const catalogErrors: Record<ErrorCode, string> = {
  authentication_required: 'ログインを再確認しています。',
  access_denied: '管理者権限を再確認しています。',
  dependency_unavailable:
    '読み込み・保存に失敗しました。保存状態を再読み込みしてください。',
  invalid_request:
    '入力内容を確認してください。価格は 0 以上の小数、出典は HTTPS URL で入力してください。',
  conflict:
    '別の操作で更新されました。最新状態を読み込みました。内容を確認してから、もう一度操作してください。',
  not_found: '対象が見つかりません。再読み込みしてください。',
};
export const capability = (value: boolean | null) =>
  value === null ? '不明' : value ? '対応' : '非対応';
export const isPrimaryModel = (model: Model) =>
  model.provider === 'liquid' &&
  model.enabled &&
  model.available &&
  /^d1(?:[-/.].+)?$/.test(model.model_id);
export const nullableCapability = (value: FormDataEntryValue | null) =>
  value === 'true' ? true : value === 'false' ? false : null;
export const datetime = (value: string | null) =>
  value
    ? new Date(value).toLocaleString('ja-JP', {
        timeZone: 'Asia/Tokyo',
        hour12: false,
      })
    : '未取得';
export const httpsURL = (value: string) => {
  try {
    const url = new URL(value);
    return (
      url.protocol === 'https:' &&
      Boolean(url.hostname) &&
      !url.username &&
      !url.password &&
      !url.hash &&
      value.length <= 512
    );
  } catch {
    return false;
  }
};
export function perMillion(value: string | undefined): string {
  if (value === undefined) return '不明・変動';
  decimal(value);
  const [whole, fraction = ''] = value.split('.');
  const digits = whole + fraction.padEnd(6, '0');
  const point = whole.length + 6;
  const left = digits.slice(0, point).replace(/^0+(?=\d)/, '') || '0';
  const right = digits.slice(point).replace(/0+$/, '');
  return right ? `${left}.${right}` : left;
}
export function modelRate(model: Model, kind: 'input' | 'output'): string {
  const price = model.current_price;
  const rate = price?.rates[kind];
  return price && rate?.unit === 'token'
    ? `${perMillion(rate.amount)} ${price.currency}`
    : '不明';
}
export function updateInput(
  model: Model,
  enabled = model.enabled,
): ModelUpdate {
  return {
    expected_version: model.version,
    display_name: model.custom_display_name ?? '',
    enabled,
    text_input: model.text_input_override,
    structured_output: model.structured_output_override,
  };
}
export type RevisionDraft<T extends object> = {
  baseline_version: number;
  values: T;
  dirty: boolean;
};
export const createRevisionDraft = <T extends object>(
  baseline_version: number,
  values: T,
): RevisionDraft<T> => ({ baseline_version, values, dirty: false });
export function reconcileRevisionDraft<T extends object>(
  draft: RevisionDraft<T> | null | undefined,
  version: number,
  values: T,
): RevisionDraft<T> {
  return draft && (draft.dirty || draft.baseline_version === version)
    ? draft
    : createRevisionDraft(version, values);
}
export const revisionInput = <T extends object>(
  draft: RevisionDraft<T>,
): T & { expected_version: number } => ({
  ...draft.values,
  expected_version: draft.baseline_version,
});
export function priceInput(form: FormData, version: number): PriceInput {
  try {
    const rates = Object.fromEntries(
      priceFields.map((key) => {
        const value = String(form.get(key) ?? '').trim();
        return [key, value === '' ? null : decimal(value)];
      }),
    ) as Record<(typeof priceFields)[number], string | null>;
    const currency = String(form.get('currency') ?? '');
    const source_url = String(form.get('source_url') ?? '').trim();
    const local = String(form.get('effective_from') ?? '');
    if (
      !/^[A-Z]{3}$/.test(currency) ||
      !httpsURL(source_url) ||
      !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(local) ||
      !Object.values(rates).some((v) => v !== null)
    )
      throw Error();
    const date = new Date(`${local}:00+09:00`);
    if (
      !Number.isFinite(date.getTime()) ||
      new Date(date.getTime() + 9 * 3600000).toISOString().slice(0, 16) !==
        local
    )
      throw Error();
    return {
      ...rates,
      expected_version: version,
      currency,
      effective_from: date.toISOString(),
      source_url,
    };
  } catch {
    throw new ApiError('invalid_request');
  }
}
export type CatalogState = {
  status:
    'idle' | 'loading' | 'saving' | 'ready' | 'error' | 'denied' | 'disposed';
  models: Model[];
  total: number;
  filter: CatalogFilter;
  selected: Model | null;
  prices: Price[];
  sync: SyncState[];
  settings: SettingsState | null;
  primary: Model | null;
  secondary: Model | null;
  error: ErrorCode | null;
  success: string | null;
};
export const emptyCatalogState = (): CatalogState => ({
  status: 'idle',
  models: [],
  total: 0,
  filter: {},
  selected: null,
  prices: [],
  sync: [],
  settings: null,
  primary: null,
  secondary: null,
  error: null,
  success: null,
});
export function createCatalogController(
  api: CatalogAPI,
  token: TokenProvider,
  changed: (state: CatalogState) => void,
  onDenied: () => void,
) {
  let state = emptyCatalogState();
  let generation = 0;
  let request = new AbortController();
  let mode: 'catalog' | 'settings' = 'catalog';
  const emit = (value: CatalogState) => {
    state = value;
    changed(value);
  };
  const active = () =>
    !['loading', 'saving', 'denied', 'disposed'].includes(state.status);
  const canRead = () =>
    !['saving', 'denied', 'disposed'].includes(state.status);
  async function run(
    status: 'loading' | 'saving',
    action: (signal: AbortSignal) => Promise<Partial<CatalogState>>,
    conflictReload?: (signal: AbortSignal) => Promise<Partial<CatalogState>>,
    replaceLoading = false,
  ) {
    if (!active() && !(replaceLoading && state.status === 'loading')) return;
    request.abort();
    request = new AbortController();
    const signal = request.signal;
    const id = ++generation;
    emit({ ...state, status, error: null, success: null });
    try {
      let result: Partial<CatalogState>;
      try {
        result = await action(signal);
      } catch (error) {
        if (
          !(error instanceof ApiError) ||
          error.code !== 'conflict' ||
          !conflictReload
        )
          throw error;
        result = {
          ...(await conflictReload(signal)),
          error: 'conflict',
          success: null,
        };
      }
      if (generation === id) emit({ ...state, ...result, status: 'ready' });
    } catch (error) {
      if (id !== generation) return;
      const code =
        error instanceof ApiError ? error.code : 'dependency_unavailable';
      if (code === 'authentication_required' || code === 'access_denied') {
        generation++;
        request.abort();
        emit({ ...emptyCatalogState(), status: 'denied', error: code });
        onDenied();
      } else emit({ ...state, status: 'error', error: code });
    }
  }
  async function readCatalog(
    signal: AbortSignal,
  ): Promise<Partial<CatalogState>> {
    const [page, sync] = await Promise.all([
      api.list(state.filter, token, signal),
      api.syncStates(token, signal),
    ]);
    const selected = state.selected
      ? await api.get(state.selected.id, token, signal)
      : null;
    const prices = selected ? await api.prices(selected.id, token, signal) : [];
    return { ...page, sync, selected, prices };
  }
  async function readSettings(
    signal: AbortSignal,
  ): Promise<Partial<CatalogState>> {
    const settings = await api.settings(token, signal);
    const [primary, secondary] = await Promise.all([
      settings.primary_model_id
        ? api.get(settings.primary_model_id, token, signal)
        : null,
      settings.secondary_model_id
        ? api.get(settings.secondary_model_id, token, signal)
        : null,
    ]);
    return { settings, primary, secondary };
  }
  const reload = (signal: AbortSignal) =>
    mode === 'settings' ? readSettings(signal) : readCatalog(signal);
  const mutate = (
    write: (signal: AbortSignal) => Promise<unknown>,
    success: string,
  ) =>
    run(
      'saving',
      async (signal) => {
        await write(signal);
        if (signal.aborted) throw Error();
        return { ...(await reload(signal)), success };
      },
      reload,
    );
  return {
    get state() {
      return state;
    },
    loadCatalog(filter: CatalogFilter = state.filter) {
      if (!canRead()) return Promise.resolve();
      mode = 'catalog';
      state = { ...state, filter };
      return run('loading', readCatalog, undefined, true);
    },
    loadSettings() {
      mode = 'settings';
      return run('loading', readSettings);
    },
    select(model: Model | null) {
      if (!active()) return Promise.resolve();
      // Retain the user's choice if a pending live search supersedes this read.
      // That search will fetch the chosen model and its price history again.
      state = { ...state, selected: model, prices: [] };
      return run('loading', async (signal) => ({
        selected: model ? await api.get(model.id, token, signal) : null,
        prices: model ? await api.prices(model.id, token, signal) : [],
      }));
    },
    add: (input: ModelInput) =>
      mutate((s) => api.add(input, token, s), 'モデルを追加しました。'),
    update: (model: Model, input: ModelUpdate) =>
      mutate(
        (s) => api.update(model.id, input, token, s),
        'モデルを更新しました。',
      ),
    savePrice: (model: Model, input: PriceInput) =>
      mutate(
        (s) => api.savePrice(model.id, input, token, s),
        '手入力価格を保存しました。',
      ),
    resetPrice: (model: Model, expected_version: number) =>
      mutate(
        (s) => api.resetPrice(model.id, expected_version, token, s),
        '価格上書きを解除しました。',
      ),
    requestSync: (provider: ModelProvider) =>
      mutate(
        (s) => api.requestSync(provider, token, s),
        '同期を依頼しました。実行状況を確認しています。',
      ),
    configureSync: (provider: ModelProvider, input: SyncInput) =>
      mutate(
        (s) => api.configureSync(provider, input, token, s),
        '同期設定を保存しました。',
      ),
    pollSync() {
      return run('loading', readCatalog);
    },
    saveSettings(primary: Model, secondary: Model) {
      return mutate(
        (s) =>
          api.saveSettings(
            state.settings?.version ?? 0,
            primary.id,
            secondary.id,
            token,
            s,
          ),
        'AI 設定の下書きを保存しました。',
      );
    },
    search(filter: CatalogFilter) {
      return run(
        'loading',
        async (signal) => ({
          ...(await api.list(filter, token, signal)),
          filter,
        }),
        undefined,
        true,
      );
    },
    validationError() {
      if (active()) emit({ ...state, error: 'invalid_request', success: null });
    },
    dispose() {
      generation++;
      request.abort();
      state = { ...emptyCatalogState(), status: 'disposed' };
    },
  };
}
