import { ApiError, type TokenProvider } from './api.ts';
import type { CatalogAPI, Model, SettingsState } from './catalog-api.ts';
import { createRevisionDraft, type RevisionDraft } from './model-catalog.ts';
import type {
  AIRuntimeAPI,
  Call,
  ConfigValues,
  Cost,
  CostFilter,
  CostReport,
  RuntimeErrorCode,
  RuntimeState,
  TestRun,
  Usage,
  UsageRevision,
} from './airuntime-api.ts';

export const runtimeErrors: Record<RuntimeErrorCode, string> = {
  authentication_required: 'ログインを再確認しています。',
  access_denied: '管理者権限を再確認しています。',
  dependency_unavailable:
    '通信を確認できませんでした。状態を再読み込みしてください。接続テストの再確認では同じ要求 ID を使用します。',
  invalid_request: '入力内容と保存済みのモデル設定を確認してください。',
  credentials_required:
    '必要な API キーを管理画面で登録してから、実行設定を保存してください。',
  conflict:
    '別の操作で更新されました。編集中の内容と保存時の版を保持しています。最新状態を読み直してから操作してください。',
  not_found: '対象が見つかりません。状態を再読み込みしてください。',
};
export const scopeNames = {
  user: '利用者',
  admin_test: '管理者テスト',
  system: 'システム',
};
export const stageNames = {
  classification: '一次判定',
  extraction: '二次抽出',
  match: '照合',
  repair: '修復',
};
export const testStateNames = {
  queued: '待機中',
  leased: '実行準備中',
  running: '実行中',
  succeeded: '成功',
  failed: '失敗',
  unknown: '結果不明',
};
const reasonNames: Record<string, string> = {
  missing_reported_cost: '料金報告が未取得',
  invalid_reported_cost: '料金報告を確認できません',
  invalid_usage: '使用量が不明または不正',
  missing_price: '参照価格が不明',
  missing_rate: '必要な単価が不明',
  invalid_price: '単価を確認できません',
  unknown_cache_write_ttl: 'キャッシュ保存期間が不明',
  unsupported_pricing_conditions: '価格条件に未対応',
  refusal_pricing_unsupported: '拒否応答の料金条件に未対応',
  not_recorded: '未記録',
  credentials_changed:
    'API キーが変更されています。再保存と接続テストが必要です。',
  credentials_unavailable: 'API キーを利用できません。',
  credentials_required:
    'API キーが無効化または更新されています。キーを確認し、実行設定の再保存と接続テストを行ってください。',
  configuration_unavailable: '設定を利用できません。',
  model_unavailable: 'モデルを利用できません。',
  catalog_settings_changed:
    'モデル選択が変更されています。再保存と接続テストが必要です。',
};
export const reasonText = (value: string | null) =>
  value
    ? (reasonNames[value] ??
      '現在の設定・料金を確認できません。最新状態を確認してください。')
    : '状態を確認してください。';
export const outcomeNames: Record<string, string> = {
  success: '成功',
  invalid_request: '要求が不正',
  transport_error: '通信失敗',
  request_cancelled: '中断',
  response_read: '応答の読取失敗',
  response_too_large: '応答上限超過',
  invalid_json: '応答形式が不正',
  provider_http: '配信先 HTTP エラー',
  provider_error: '配信先エラー',
  model_mismatch: 'モデル不一致',
  invalid_output: '出力形式が不正',
  refusal: '応答拒否',
  incomplete: '応答未完了',
  generation_mismatch: '生成 ID 不一致',
  provider_unavailable: '配信先を利用できません',
  timeout: '時間切れ',
  configuration_unavailable: '設定を利用できません',
  execution_unavailable: '実行状態を確認できません',
  input_too_large: '入力上限超過',
  credentials_unavailable: 'API キーを利用できません',
  recording_failed: '記録失敗',
  lease_expired: '実行権の期限切れ',
};
export const outcomeText = (value: string | null) =>
  value ? (outcomeNames[value] ?? '結果を確認できません') : '未完了';
export const costSource = (cost: Cost) =>
  ({ unknown: '不明', estimated: '推定', provider_reported: '配信先報告' })[
    cost.status
  ];
export const costText = (cost: Cost) =>
  cost.amount === null
    ? '不明（0 ではありません）'
    : `${cost.amount} ${cost.currency}`;
export const usageText = (u: Usage) =>
  u.valid && u.present
    ? `入力 ${u.input_tokens} / 出力 ${u.output_tokens} token`
    : '使用量不明';
export const monthJST = (date = new Date()) =>
  new Intl.DateTimeFormat('sv-SE', {
    timeZone: 'Asia/Tokyo',
    year: 'numeric',
    month: '2-digit',
  }).format(date);
type Status =
  'idle' | 'loading' | 'saving' | 'ready' | 'error' | 'denied' | 'disposed';
const errorCode = (e: unknown): RuntimeErrorCode =>
  e instanceof ApiError
    ? e.code
    : typeof e === 'object' &&
        e !== null &&
        'code' in e &&
        e.code === 'credentials_required'
      ? 'credentials_required'
      : 'dependency_unavailable';
export const pendingTest = (run: TestRun | null | undefined) =>
  Boolean(run && ['queued', 'leased', 'running'].includes(run.state));
export type RuntimeView = {
  status: Status;
  runtime: RuntimeState | null;
  settings: SettingsState | null;
  primary: Model | null;
  secondary: Model | null;
  form: RevisionDraft<ConfigValues> | null;
  error: RuntimeErrorCode | null;
  success: string | null;
  requestUncertain: boolean;
};
export const emptyRuntimeView = (): RuntimeView => ({
  status: 'idle',
  runtime: null,
  settings: null,
  primary: null,
  secondary: null,
  form: null,
  error: null,
  success: null,
  requestUncertain: false,
});
const validApprovedProviderSlug = (value: string) =>
  value.length <= 128 &&
  /^[a-z0-9][a-z0-9._-]*(\/[a-z0-9][a-z0-9._-]*)?$/.test(value);
export function runtimeCurrent(view: RuntimeView): boolean {
  return Boolean(
    view.runtime?.draft &&
    (view.runtime.draft.secondary.provider !== 'openrouter' ||
      validApprovedProviderSlug(view.runtime.draft.openrouter_provider)) &&
    view.settings?.id === view.runtime.draft.catalog_settings_id &&
    !view.form?.dirty &&
    view.form?.baseline_version === view.runtime.version &&
    !view.requestUncertain,
  );
}
export function canActivate(view: RuntimeView): boolean {
  const r = view.runtime;
  return (
    runtimeCurrent(view) &&
    r?.latest_test?.state === 'succeeded' &&
    r.latest_test.profile_id === r.draft?.id
  );
}
export function createRuntimeController(
  api: AIRuntimeAPI,
  catalog: CatalogAPI,
  token: TokenProvider,
  changed: (view: RuntimeView) => void,
  onDenied: () => void,
  randomUUID: () => string = () => crypto.randomUUID(),
) {
  let view = emptyRuntimeView();
  let generation = 0;
  let abort: AbortController | undefined;
  let disposed = false;
  let mutating = false;
  let pendingRequest: {
    expected_version: number;
    profile_id: string;
    request_id: string;
  } | null = null;
  const publish = (patch: Partial<RuntimeView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  const begin = () => {
    abort?.abort();
    abort = new AbortController();
    return { signal: abort.signal, seq: ++generation };
  };
  const current = (seq: number) =>
    !disposed && generation === seq && view.status !== 'denied';
  function failed(error: unknown, seq: number) {
    if (!current(seq)) return;
    const code = errorCode(error);
    if (code === 'authentication_required' || code === 'access_denied') {
      abort?.abort();
      generation++;
      pendingRequest = null;
      view = { ...emptyRuntimeView(), status: 'denied' };
      changed(view);
      onDenied();
    } else publish({ status: 'error', error: code });
  }
  function values(
    runtime: RuntimeState,
    settings: SettingsState,
  ): ConfigValues {
    const p = runtime.draft;
    return {
      catalog_settings_id: settings.id ?? '',
      max_input_bytes: p?.max_input_bytes ?? 8192,
      max_output_tokens: p?.max_output_tokens ?? 1024,
      timeout_seconds: p?.timeout_seconds ?? 30,
      openrouter_provider: p?.openrouter_provider ?? '',
    };
  }
  async function read(reset: boolean) {
    if (disposed || mutating || view.status === 'denied') return;
    const { seq, signal } = begin();
    publish({ status: 'loading', error: null, success: null });
    try {
      const [runtime, settings] = await Promise.all([
        api.state(token, signal),
        catalog.settings(token, signal),
      ]);
      const [primary, secondary] = await Promise.all([
        settings.primary_model_id
          ? catalog.get(settings.primary_model_id, token, signal)
          : null,
        settings.secondary_model_id
          ? catalog.get(settings.secondary_model_id, token, signal)
          : null,
      ]);
      if (!current(seq)) return;
      const form =
        reset || !view.form
          ? createRevisionDraft(runtime.version, values(runtime, settings))
          : view.form;
      if (pendingRequest && pendingRequest.profile_id !== runtime.draft?.id) {
        pendingRequest = null;
        publish({ requestUncertain: false });
      }
      publish({ status: 'ready', runtime, settings, primary, secondary, form });
    } catch (error) {
      failed(error, seq);
    }
  }
  async function mutation(
    operation: (signal: AbortSignal) => Promise<RuntimeState>,
    success: string,
    preserveDirtyForm = false,
  ) {
    if (disposed || mutating || view.status === 'denied') return;
    mutating = true;
    const { seq, signal } = begin();
    publish({ status: 'saving', error: null, success: null });
    try {
      const runtime = await operation(signal);
      if (current(seq)) {
        pendingRequest = null;
        publish({
          status: 'ready',
          runtime,
          form:
            preserveDirtyForm && view.form?.dirty
              ? view.form
              : view.settings
                ? createRevisionDraft(
                    runtime.version,
                    values(runtime, view.settings),
                  )
                : null,
          requestUncertain: false,
          success,
        });
      }
    } catch (error) {
      failed(error, seq);
    } finally {
      mutating = false;
    }
  }
  return {
    state: () => view,
    load: () => read(true),
    refresh: () => read(false),
    edit(patch: Partial<ConfigValues>) {
      if (!view.form || mutating || disposed || view.status === 'denied')
        return;
      publish({
        form: {
          ...view.form,
          values: { ...view.form.values, ...patch },
          dirty: true,
        },
        success: null,
      });
    },
    async save() {
      const form = view.form;
      const settings = view.settings;
      if (
        !form ||
        !settings?.id ||
        view.requestUncertain ||
        pendingTest(view.runtime?.latest_test)
      )
        return;
      if (
        view.secondary?.provider === 'openrouter' &&
        !validApprovedProviderSlug(form.values.openrouter_provider)
      ) {
        publish({ status: 'error', error: 'invalid_request' });
        return;
      }
      await mutation(
        (signal) =>
          api.save(
            {
              ...form.values,
              catalog_settings_id: settings.id!,
              openrouter_provider:
                view.secondary?.provider === 'openrouter'
                  ? form.values.openrouter_provider
                  : '',
              expected_version: form.baseline_version,
            },
            token,
            signal,
          ),
        '実行設定を保存しました。接続テストと有効化は別の操作です。',
      );
    },
    async requestTest(confirmed: boolean, fresh = false) {
      if (
        !confirmed ||
        disposed ||
        mutating ||
        view.status === 'denied' ||
        pendingTest(view.runtime?.latest_test) ||
        !view.runtime?.draft
      )
        return;
      if (!pendingRequest && !runtimeCurrent(view)) return;
      if (fresh && pendingRequest && !view.requestUncertain)
        pendingRequest = null;
      pendingRequest ??= {
        expected_version: view.runtime.version,
        profile_id: view.runtime.draft.id,
        request_id: randomUUID(),
      };
      const input = pendingRequest;
      mutating = true;
      const { seq, signal } = begin();
      publish({ status: 'saving', error: null, success: null });
      try {
        const run = await api.requestTest(input, token, signal);
        if (current(seq) && view.runtime)
          publish({
            status: 'ready',
            runtime: { ...view.runtime, latest_test: run },
            requestUncertain: false,
            success: '接続テストを受け付けました。結果を確認しています。',
          });
      } catch (error) {
        if (current(seq)) {
          const uncertain = errorCode(error) === 'dependency_unavailable';
          if (!uncertain) pendingRequest = null;
          publish({ requestUncertain: uncertain });
        }
        failed(error, seq);
      } finally {
        mutating = false;
      }
    },
    async poll() {
      const run = view.runtime?.latest_test;
      if (!run || !pendingTest(run) || mutating || disposed) return;
      const { seq, signal } = begin();
      try {
        const test = await api.test(run.id, token, signal);
        if (current(seq) && view.runtime) {
          publish({ runtime: { ...view.runtime, latest_test: test } });
          if (!pendingTest(test)) await read(false);
        }
      } catch (error) {
        failed(error, seq);
      }
    },
    async activate() {
      if (
        !canActivate(view) ||
        !view.runtime?.draft ||
        !view.runtime.latest_test
      )
        return;
      const r = view.runtime;
      await mutation(
        (signal) =>
          api.activate(
            {
              expected_version: r.version,
              profile_id: r.draft!.id,
              test_id: r.latest_test!.id,
            },
            token,
            signal,
          ),
        'テスト成功版を有効化しました。',
      );
    },
    async deactivate() {
      if (!view.runtime?.active) return;
      const version = view.runtime.version;
      await mutation(
        (signal) => api.deactivate(version, token, signal),
        '実行を停止しました。',
        true,
      );
    },
    dispose() {
      disposed = true;
      generation++;
      abort?.abort();
      pendingRequest = null;
      view = { ...emptyRuntimeView(), status: 'disposed' };
      changed(view);
    },
  };
}
export type UsageView = {
  status: Status;
  filter: CostFilter;
  report: CostReport | null;
  calls: { calls: Call[]; total: number } | null;
  usersOffset: number;
  callsOffset: number;
  revisions: UsageRevision[];
  revisionCallID: string | null;
  error: RuntimeErrorCode | null;
  reconciling: string | null;
};
export const emptyUsageView = (): UsageView => ({
  status: 'idle',
  filter: { month: monthJST() },
  report: null,
  calls: null,
  usersOffset: 0,
  callsOffset: 0,
  revisions: [],
  revisionCallID: null,
  error: null,
  reconciling: null,
});
export function createUsageController(
  api: AIRuntimeAPI,
  token: TokenProvider,
  changed: (view: UsageView) => void,
  onDenied: () => void,
) {
  let view = emptyUsageView();
  let generation = 0;
  let abort: AbortController | undefined;
  let disposed = false;
  let reconciling = false;
  const publish = (patch: Partial<UsageView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  const begin = () => {
    abort?.abort();
    abort = new AbortController();
    return { seq: ++generation, signal: abort.signal };
  };
  const current = (seq: number) =>
    !disposed && seq === generation && view.status !== 'denied';
  function failed(error: unknown, seq: number) {
    if (!current(seq)) return;
    const code = errorCode(error);
    if (code === 'access_denied' || code === 'authentication_required') {
      abort?.abort();
      generation++;
      view = { ...emptyUsageView(), status: 'denied' };
      changed(view);
      onDenied();
    } else publish({ status: 'error', error: code, reconciling: null });
  }
  async function load() {
    if (disposed || view.status === 'denied' || reconciling) return;
    const { seq, signal } = begin();
    const filter = { ...view.filter };
    const usersOffset = view.usersOffset,
      callsOffset = view.callsOffset;
    publish({
      status: 'loading',
      error: null,
      revisions: [],
      revisionCallID: null,
    });
    try {
      const [report, calls] = await Promise.all([
        api.costs({ ...filter, limit: 50, offset: usersOffset }, token, signal),
        api.calls({ ...filter, limit: 50, offset: callsOffset }, token, signal),
      ]);
      if (current(seq)) publish({ status: 'ready', report, calls });
    } catch (error) {
      failed(error, seq);
    }
  }
  return {
    state: () => view,
    load,
    async filter(patch: Partial<CostFilter>) {
      if (disposed || view.status === 'denied' || reconciling) return;
      publish({
        filter: { ...view.filter, ...patch },
        usersOffset: 0,
        callsOffset: 0,
        report: null,
        calls: null,
      });
      await load();
    },
    async usersPage(offset: number) {
      if (offset < 0 || disposed || reconciling) return;
      publish({ usersOffset: offset });
      await load();
    },
    async callsPage(offset: number) {
      if (offset < 0 || disposed || reconciling) return;
      publish({ callsOffset: offset });
      await load();
    },
    async revisions(id: string) {
      if (disposed || view.status === 'denied' || reconciling) return;
      const { seq, signal } = begin();
      publish({ revisions: [], revisionCallID: null, error: null });
      try {
        const revisions = await api.revisions(id, token, signal);
        if (current(seq)) publish({ revisions, revisionCallID: id });
      } catch (error) {
        failed(error, seq);
      }
    },
    async reconcile(call: Call) {
      if (
        reconciling ||
        disposed ||
        view.status === 'denied' ||
        call.provider !== 'openrouter' ||
        !call.generation_id
      )
        return;
      reconciling = true;
      const { seq, signal } = begin();
      publish({ reconciling: call.id, error: null });
      try {
        await api.reconcile(call.id, token, signal);
        if (current(seq)) {
          reconciling = false;
          publish({ reconciling: null });
          await load();
        }
      } catch (error) {
        failed(error, seq);
      } finally {
        reconciling = false;
      }
    },
    dispose() {
      disposed = true;
      generation++;
      abort?.abort();
      view = { ...emptyUsageView(), status: 'disposed' };
      changed(view);
    },
  };
}
