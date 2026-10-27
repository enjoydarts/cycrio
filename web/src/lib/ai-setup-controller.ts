import {
  ApiError,
  type CredentialSummary,
  type createAPI,
  type TokenProvider,
} from './api.ts';
import type {
  CatalogAPI,
  Model,
  PriceInput,
  ModelProvider,
  SettingsState,
} from './catalog-api.ts';
import type {
  AIRuntimeAPI,
  RuntimeState,
  ConfigValues,
  RuntimeErrorCode,
} from './airuntime-api.ts';
import type { AIRoutesAPI, AIRoute, RoutesResult } from './ai-routes-api.ts';
import type {
  MailProcessingAPI,
  PolicyState,
  StandardPolicyInput,
} from './mail-processing-api.ts';
import {
  createRevisionDraft,
  type RevisionDraft,
  isPrimaryModel,
} from './model-catalog.ts';
import { pendingTest } from './ai-runtime-controller.ts';
export type SetupStage = 'models' | 'test' | 'permission';
type Selection = {
  primary: string;
  secondary: string;
  route_id: string;
  route_tag: string;
  max_input_bytes: number;
  max_output_tokens: number;
  timeout_seconds: number;
};
type Dependencies = {
  keys?: Pick<ReturnType<typeof createAPI>, 'credentials'>;
  catalog: CatalogAPI;
  runtime: AIRuntimeAPI;
  processing: MailProcessingAPI;
  routes: AIRoutesAPI;
};
export type AISetupView = {
  status:
    'idle' | 'loading' | 'ready' | 'saving' | 'error' | 'denied' | 'disposed';
  runtime: RuntimeState | null;
  settings: SettingsState | null;
  policy: PolicyState | null;
  policyReloadRequired: boolean;
  models: Model[];
  credentials: CredentialSummary[];
  routes: RoutesResult | null;
  routeLimits: Pick<Selection, 'max_input_bytes' | 'max_output_tokens'> | null;
  selection: RevisionDraft<Selection> | null;
  runtimeBaseline: number | null;
  error: RuntimeErrorCode | null;
  success: string | null;
  routeLoading: boolean;
  routeError: boolean;
  requestUncertain: boolean;
  partialSaved: boolean;
};
export const emptyAISetupView = (): AISetupView => ({
  status: 'idle',
  runtime: null,
  settings: null,
  policy: null,
  policyReloadRequired: false,
  models: [],
  credentials: [],
  routes: null,
  routeLimits: null,
  selection: null,
  runtimeBaseline: null,
  error: null,
  success: null,
  routeLoading: false,
  routeError: false,
  requestUncertain: false,
  partialSaved: false,
});
export function preferredRoute(
  routes: AIRoute[],
  modelID: string,
  tag = '',
): AIRoute | null {
  const options = routes.filter((r) => r.eligible && r.model_id === modelID);
  const known = (r: AIRoute) =>
    !!r.price?.rates.input && !!r.price?.rates.output;
  return (
    options.find((r) => r.tag === tag) ??
    options.sort(
      (a, b) =>
        Number(!known(a)) - Number(!known(b)) ||
        a.provider_name.localeCompare(b.provider_name) ||
        a.tag.localeCompare(b.tag),
    )[0] ??
    null
  );
}
export function draftNeedsSave(view: AISetupView): boolean {
  const draft = view.runtime?.draft;
  if (!draft || draft.catalog_settings_id !== view.settings?.id) return true;
  return [draft.primary, draft.secondary].some((ref) => {
    const key = view.credentials.find((k) => k.provider === ref.provider);
    return (
      !!key &&
      (key.state !== 'configured' || key.version !== ref.credential_version)
    );
  });
}
export function setupStage(view: AISetupView): SetupStage {
  if (draftNeedsSave(view)) return 'models';
  if (
    view.runtime?.draft &&
    view.runtime.draft.catalog_settings_id === view.settings?.id &&
    view.runtime.draft.id !== view.runtime.active?.id
  )
    return 'test';
  if (view.runtime?.active_ready) return 'permission';
  if (
    view.runtime?.draft &&
    view.runtime.draft.catalog_settings_id === view.settings?.id
  )
    return 'test';
  return 'models';
}
export function canSaveSetup(
  view: AISetupView,
  provider?: ModelProvider,
): boolean {
  const form = view.selection,
    runtime = view.runtime,
    settings = view.settings;
  if (
    !form ||
    !runtime ||
    !settings ||
    ['idle', 'loading', 'saving', 'denied', 'disposed'].includes(view.status) ||
    view.requestUncertain ||
    view.routeLoading ||
    pendingTest(runtime.latest_test) ||
    form.baseline_version !== settings.version ||
    view.runtimeBaseline !== runtime.version
  )
    return false;
  const value = form.values,
    primary = view.models.find((m) => m.id === value.primary),
    secondary = view.models.find((m) => m.id === value.secondary);
  const bounded = (n: number, min: number, max: number) =>
    Number.isSafeInteger(n) && n >= min && n <= max;
  if (
    !primary ||
    !isPrimaryModel(primary) ||
    !primary.enabled ||
    !primary.available ||
    !secondary ||
    (provider !== undefined && secondary.provider !== provider) ||
    secondary.provider === 'liquid' ||
    !secondary.enabled ||
    !secondary.available ||
    !bounded(value.max_input_bytes, 1024, 65536) ||
    !bounded(value.max_output_tokens, 64, 16384) ||
    !bounded(value.timeout_seconds, 1, 120) ||
    view.credentials.some(
      (k) =>
        (k.provider === 'liquid' || k.provider === secondary.provider) &&
        k.state !== 'configured',
    )
  )
    return false;
  return (
    secondary.provider !== 'openrouter' ||
    (!!view.routes?.fresh &&
      view.routeLimits?.max_input_bytes === value.max_input_bytes &&
      view.routeLimits.max_output_tokens === value.max_output_tokens &&
      view.routes.routes.some(
        (r) =>
          r.id === value.route_id && r.model_id === secondary.id && r.eligible,
      ))
  );
}
class RouteSnapshotChanged extends Error {}
export function createAISetupController(
  api: Dependencies,
  token: TokenProvider,
  changed: (view: AISetupView) => void,
  onDenied: () => void,
  randomUUID = () => crypto.randomUUID(),
) {
  let view = emptyAISetupView(),
    generation = 0,
    disposed = false,
    mutating = false,
    reading = false,
    autoRefreshed = false;
  let abort = new AbortController();
  let routePending: { promise: Promise<void> } | null = null;
  let pendingRequest: {
    expected_version: number;
    profile_id: string;
    request_id: string;
  } | null = null;
  const emit = (patch: Partial<AISetupView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  const allowed = () => !disposed && view.status !== 'denied';
  const current = (seq: number) => allowed() && seq === generation;
  const begin = () => {
    // A new read or write supersedes optional metadata work. Release its
    // loading state now; its late finally must not release a newer owner.
    routePending = null;
    view = { ...view, routeLoading: false };
    abort.abort();
    abort = new AbortController();
    return { seq: ++generation, signal: abort.signal };
  };
  function fail(error: unknown, seq: number) {
    if (!current(seq)) return;
    const code: RuntimeErrorCode =
      error instanceof ApiError
        ? error.code
        : typeof error === 'object' &&
            error !== null &&
            'code' in error &&
            error.code === 'credentials_required'
          ? 'credentials_required'
          : 'dependency_unavailable';
    if (code === 'authentication_required' || code === 'access_denied') {
      abort.abort();
      generation++;
      pendingRequest = null;
      view = { ...emptyAISetupView(), status: 'denied' };
      changed(view);
      onDenied();
    } else emit({ status: 'error', error: code });
  }
  async function allModels(signal: AbortSignal) {
    const models: Model[] = [];
    let total = 1;
    while (models.length < total) {
      const page = await api.catalog.list(
        { offset: models.length },
        token,
        signal,
      );
      models.push(...page.models);
      total = page.total;
      if (!page.models.length) break;
    }
    return models;
  }
  async function allRoutes(
    signal: AbortSignal,
    values?: Pick<Selection, 'max_input_bytes' | 'max_output_tokens'>,
  ) {
    const criteria = {
      max_input_bytes: values?.max_input_bytes ?? 16384,
      max_output_tokens: values?.max_output_tokens ?? 1024,
    };
    const read = (offset: number) => {
      if (signal.aborted) throw new DOMException('Aborted', 'AbortError');
      return api.routes.list({ ...criteria, offset }, token, signal);
    };
    const result = await read(0);
    // Every GET reads the server's current snapshot. Never present a mixed
    // generation as a verified set; the user can explicitly retry this read.
    const header = (page: RoutesResult) =>
      JSON.stringify([
        page.snapshot?.id ?? null,
        page.snapshot?.captured_at ?? null,
        page.snapshot?.source_url ?? null,
        page.fresh,
        page.key_available,
        page.total,
        page.refresh.status,
        page.refresh.error_code,
        page.refresh.last_attempt_at,
        page.refresh.last_success_at,
      ]);
    if (result.total > 20000 || result.total < 0)
      throw new RouteSnapshotChanged();
    const expected = header(result),
      routes: AIRoute[] = [],
      ids = new Set<string>();
    const append = (page: RoutesResult) => {
      if (
        header(page) !== expected ||
        page.routes.length > 100 ||
        routes.length + page.routes.length > result.total ||
        (page.routes.length === 0 && routes.length < result.total)
      )
        throw new RouteSnapshotChanged();
      for (const route of page.routes) {
        if (
          !page.snapshot ||
          route.snapshot_id !== page.snapshot.id ||
          route.captured_at !== page.snapshot.captured_at ||
          route.source_url !== page.snapshot.source_url ||
          ids.has(route.id)
        )
          throw new RouteSnapshotChanged();
        ids.add(route.id);
        routes.push(route);
      }
    };
    append(result);
    while (routes.length < result.total) append(await read(routes.length));
    return { ...result, routes };
  }
  function invalidateRoutes(error: unknown, seq: number): unknown {
    if (!(error instanceof RouteSnapshotChanged)) return error;
    if (current(seq))
      emit({
        routes: null,
        routeLimits: null,
        routeError: true,
        routeLoading: false,
      });
    return new ApiError('conflict');
  }
  function values(
    runtime: RuntimeState,
    settings: SettingsState,
    routes: RoutesResult,
  ): Selection {
    const p = runtime.draft ?? runtime.active;
    const secondary = settings.secondary_model_id ?? '';
    const chosen = preferredRoute(
      routes.routes,
      secondary,
      p?.openrouter_provider ?? '',
    );
    return {
      primary: settings.primary_model_id ?? '',
      secondary,
      route_id: chosen?.id ?? '',
      route_tag: chosen?.tag ?? '',
      max_input_bytes: p?.max_input_bytes ?? 16384,
      max_output_tokens: p?.max_output_tokens ?? 1024,
      timeout_seconds: p?.timeout_seconds ?? 30,
    };
  }
  function reconcileRoutes(
    form: RevisionDraft<Selection>,
    routes: RoutesResult,
  ): RevisionDraft<Selection> {
    const exact = routes.routes.find(
      (r) =>
        r.eligible &&
        r.model_id === form.values.secondary &&
        r.tag === form.values.route_tag,
    );
    const chosen = form.dirty
      ? exact
      : preferredRoute(
          routes.routes,
          form.values.secondary,
          form.values.route_tag,
        );
    return {
      ...form,
      values: {
        ...form.values,
        route_id: chosen?.id ?? '',
        route_tag: chosen?.tag ?? (form.dirty ? form.values.route_tag : ''),
      },
    };
  }
  async function load(reset = false) {
    if (!allowed() || mutating || reading) return;
    reading = true;
    const { seq, signal } = begin();
    emit({ status: 'loading', error: null });
    try {
      const [runtime, settings, policy, models, credentials] =
        await Promise.all([
          api.runtime.state(token, signal),
          api.catalog.settings(token, signal),
          api.processing.policy(token, signal),
          allModels(signal),
          api.keys?.credentials(token, signal) ?? Promise.resolve([]),
        ]);
      if (!current(seq)) return;
      const policyReloadRequired =
        view.policyReloadRequired &&
        (policy.runtime_version !== runtime.version ||
          policy.profile?.id !== runtime.active?.id);
      const policyRecovered =
        view.policyReloadRequired && !policyReloadRequired;
      // Route-list availability must not hide an existing permission or
      // prevent its stop action. New route selections still need the list.
      emit({
        runtime,
        settings,
        policy,
        models,
        credentials,
        policyReloadRequired,
        ...(policyRecovered
          ? { success: '最新の送信条件を読み直しました。' }
          : {}),
      });
      const criteria = view.selection?.values ??
        runtime.draft ??
        runtime.active ?? { max_input_bytes: 16384, max_output_tokens: 1024 };
      const routes = await allRoutes(signal, criteria);
      if (!current(seq)) return;
      emit({
        runtime,
        settings,
        policy,
        policyReloadRequired,
        models,
        credentials,
        routes,
        routeLimits: {
          max_input_bytes: criteria.max_input_bytes,
          max_output_tokens: criteria.max_output_tokens,
        },
        status: 'ready',
        routeError: false,
        selection:
          reset || !view.selection
            ? createRevisionDraft(
                settings.version,
                values(runtime, settings, routes),
              )
            : reconcileRoutes(view.selection, routes),
        runtimeBaseline:
          reset || view.runtimeBaseline === null
            ? runtime.version
            : view.runtimeBaseline,
      });
      if (!routes.fresh && routes.key_available && !autoRefreshed) {
        autoRefreshed = true;
        reading = false;
        await refreshRoutes();
      }
    } catch (error) {
      fail(invalidateRoutes(error, seq), seq);
    } finally {
      if (seq === generation) reading = false;
    }
  }
  async function refreshRoutes() {
    if (routePending) return routePending.promise;
    if (!allowed() || mutating || reading || !view.routes?.key_available)
      return;
    const seq = generation,
      signal = abort.signal;
    const criteria = view.selection?.values ?? {
      max_input_bytes: 16384,
      max_output_tokens: 1024,
    };
    emit({ routeLoading: true, routeError: false });
    const request = { promise: Promise.resolve() };
    routePending = request;
    request.promise = (async () => {
      try {
        await api.routes.refresh(token, signal);
        const [routes, policy] = await Promise.all([
          allRoutes(signal, criteria),
          api.processing.policy(token, signal),
        ]);
        if (!current(seq)) return;
        let selection = view.selection;
        if (selection) {
          selection = reconcileRoutes(selection, routes);
        }
        emit({
          routes,
          routeLimits: {
            max_input_bytes: criteria.max_input_bytes,
            max_output_tokens: criteria.max_output_tokens,
          },
          policy,
          ...(policy.runtime_version === view.runtime?.version &&
          policy.profile?.id === view.runtime.active?.id
            ? {
                policyReloadRequired: false,
                ...(view.policyReloadRequired
                  ? { success: '最新の送信条件を読み直しました。' }
                  : {}),
              }
            : {}),
          selection,
          routeLoading: false,
          routeError: !routes.fresh,
        });
      } catch (error) {
        if (error instanceof RouteSnapshotChanged)
          fail(invalidateRoutes(error, seq), seq);
        else if (
          error instanceof ApiError &&
          ['authentication_required', 'access_denied'].includes(error.code)
        )
          fail(error, seq);
        else if (current(seq)) emit({ routeLoading: false, routeError: true });
      } finally {
        if (routePending === request) {
          routePending = null;
          if (current(seq) && view.routeLoading) emit({ routeLoading: false });
        }
      }
    })();
    return request.promise;
  }
  async function mutation(
    operation: (signal: AbortSignal, seq: number) => Promise<void>,
  ) {
    if (!allowed() || mutating || reading) return;
    mutating = true;
    const { seq, signal } = begin();
    emit({ status: 'saving', error: null, success: null });
    try {
      await operation(signal, seq);
      if (current(seq)) emit({ status: 'ready' });
    } catch (error) {
      fail(error, seq);
    } finally {
      mutating = false;
    }
  }
  return {
    state: () => view,
    load: () => load(false),
    refresh: () => load(false),
    async rebase() {
      if (!allowed() || mutating || view.requestUncertain) return;
      await load(true);
    },
    refreshRoutes,
    choose(stage: 'primary' | 'secondary', id: string) {
      if (!allowed() || mutating || view.requestUncertain || !view.selection)
        return;
      const model = view.models.find((m) => m.id === id);
      if (
        !model?.enabled ||
        !model.available ||
        (stage === 'primary' && !isPrimaryModel(model)) ||
        (stage === 'secondary' && model.provider === 'liquid')
      )
        return;
      const r =
        stage === 'secondary'
          ? preferredRoute(
              view.routes?.routes ?? [],
              id,
              view.selection.values.route_tag,
            )
          : null;
      if (stage === 'secondary' && model.provider === 'openrouter' && !r)
        return;
      emit({
        selection: {
          ...view.selection,
          dirty: true,
          values: {
            ...view.selection.values,
            [stage]: id,
            ...(stage === 'secondary'
              ? { route_id: r?.id ?? '', route_tag: r?.tag ?? '' }
              : {}),
          },
        },
        success: null,
      });
    },
    chooseRoute(id: string) {
      if (!allowed() || mutating || view.requestUncertain || !view.selection)
        return;
      const route = view.routes?.routes.find(
        (r) =>
          r.id === id &&
          r.eligible &&
          r.model_id === view.selection!.values.secondary,
      );
      if (route)
        emit({
          selection: {
            ...view.selection,
            dirty: true,
            values: {
              ...view.selection.values,
              route_id: id,
              route_tag: route.tag,
            },
          },
        });
    },
    editLimits(
      patch: Partial<
        Pick<
          Selection,
          'max_input_bytes' | 'max_output_tokens' | 'timeout_seconds'
        >
      >,
    ) {
      if (
        allowed() &&
        !mutating &&
        !view.requestUncertain &&
        !view.routeLoading &&
        view.selection
      )
        emit({
          selection: {
            ...view.selection,
            dirty: true,
            values: { ...view.selection.values, ...patch },
          },
        });
    },
    async save() {
      if (!canSaveSetup(view)) return;
      const form = view.selection;
      if (
        !form ||
        !view.settings ||
        !view.runtime ||
        view.requestUncertain ||
        pendingTest(view.runtime.latest_test) ||
        view.routeLoading
      )
        return;
      const selected = form.values,
        secondary = view.models.find((m) => m.id === selected.secondary);
      if (
        !selected.primary ||
        !secondary ||
        (secondary.provider === 'openrouter' &&
          (!view.routes?.fresh ||
            !view.routes.routes.some(
              (r) => r.id === selected.route_id && r.eligible,
            )))
      )
        return;
      await mutation(async (signal, seq) => {
        const settings = await api.catalog.saveSettings(
          form.baseline_version,
          selected.primary,
          selected.secondary,
          token,
          signal,
        );
        if (!current(seq)) return;
        emit({
          settings,
          partialSaved: true,
          success: 'モデルと価格を保存しました。実行設定の保存を続けています。',
        });
        const config: ConfigValues = {
          catalog_settings_id: settings.id!,
          max_input_bytes: selected.max_input_bytes,
          max_output_tokens: selected.max_output_tokens,
          timeout_seconds: selected.timeout_seconds,
          openrouter_provider: '',
        };
        const runtime = await api.runtime.save(
          {
            ...config,
            expected_version: view.runtimeBaseline ?? view.runtime!.version,
            ...(secondary.provider === 'openrouter'
              ? { route_id: selected.route_id }
              : {}),
          },
          token,
          signal,
        );
        if (current(seq))
          emit({
            runtime,
            runtimeBaseline: runtime.version,
            selection: createRevisionDraft(settings.version, selected),
            partialSaved: false,
            success:
              'モデル・料金と実行設定を保存しました。次に接続テストを行ってください。',
          });
      });
    },
    async savePrice(model: Model, input: PriceInput) {
      let saved = false;
      await mutation(async (signal, seq) => {
        const updated = await api.catalog.savePrice(
          model.id,
          input,
          token,
          signal,
        );
        if (current(seq)) {
          emit({
            models: view.models.map((m) => (m.id === model.id ? updated : m)),
            selection: view.selection
              ? { ...view.selection, dirty: true }
              : null,
            success:
              '価格を保存しました。「モデルと実行設定を保存」で選択中の設定へ反映してください。有効版の固定価格はまだ変わりません。',
          });
          saved = true;
        }
      });
      return saved;
    },
    async requestTest(confirmed: boolean) {
      if (!pendingRequest && draftNeedsSave(view)) return;
      if (
        !confirmed ||
        !view.runtime?.draft ||
        view.selection?.dirty ||
        pendingTest(view.runtime.latest_test)
      )
        return;
      if (
        !pendingRequest &&
        view.runtime.draft.catalog_settings_id !== view.settings?.id
      )
        return;
      pendingRequest ??= {
        expected_version: view.runtime.version,
        profile_id: view.runtime.draft.id,
        request_id: randomUUID(),
      };
      await mutation(async (signal, seq) => {
        try {
          const run = await api.runtime.requestTest(
            pendingRequest!,
            token,
            signal,
          );
          if (current(seq) && view.runtime) {
            pendingRequest = null;
            emit({
              runtime: { ...view.runtime, latest_test: run },
              requestUncertain: false,
              success: '接続テストの受付を確認しました。',
            });
          }
        } catch (error) {
          if (current(seq)) {
            const uncertain =
              !(error instanceof ApiError) ||
              error.code === 'dependency_unavailable';
            if (!uncertain) pendingRequest = null;
            emit({ requestUncertain: uncertain });
          }
          throw error;
        }
      });
    },
    async poll() {
      const run = view.runtime?.latest_test;
      if (!run || !pendingTest(run) || mutating || reading || !allowed())
        return;
      const seq = generation;
      try {
        const result = await api.runtime.test(run.id, token, abort.signal);
        if (current(seq) && view.runtime)
          emit({ runtime: { ...view.runtime, latest_test: result } });
      } catch (error) {
        fail(error, seq);
      }
    },
    async activate() {
      if (draftNeedsSave(view)) return;
      const runtime = view.runtime;
      if (
        !runtime?.draft ||
        runtime.latest_test?.state !== 'succeeded' ||
        runtime.latest_test.profile_id !== runtime.draft.id ||
        view.selection?.dirty ||
        view.requestUncertain
      )
        return;
      await mutation(async (signal, seq) => {
        const next = await api.runtime.activate(
          {
            expected_version: runtime.version,
            profile_id: runtime.draft!.id,
            test_id: runtime.latest_test!.id,
          },
          token,
          signal,
        );
        if (!current(seq)) return;
        emit({ runtime: next, runtimeBaseline: next.version });
        const policy = await api.processing.policy(token, signal);
        if (current(seq))
          emit({
            policy,
            success:
              '接続テスト成功版を有効化しました。本文の送信条件を確認してください。',
          });
      });
    },
    async deactivate() {
      const runtime = view.runtime;
      if (
        !runtime?.active ||
        view.requestUncertain ||
        pendingTest(runtime.latest_test)
      )
        return;
      await mutation(async (signal, seq) => {
        const next = await api.runtime.deactivate(
          runtime.version,
          token,
          signal,
        );
        if (!current(seq)) return;
        emit({
          runtime: next,
          runtimeBaseline: view.selection?.dirty
            ? view.runtimeBaseline
            : next.version,
          success:
            'AI の実行を停止しました。保存していないモデル・料金の入力は保持しています。',
        });
        const policy = await api.processing.policy(token, signal);
        if (current(seq)) emit({ policy });
      });
    },
    async saveStandard(input: StandardPolicyInput) {
      if (view.policyReloadRequired) return false;
      let saved = false;
      await mutation(async (signal, seq) => {
        const policy = await api.processing.saveStandardPolicy(
          input,
          token,
          signal,
        );
        if (current(seq)) {
          emit({
            policy,
            success: '確認した条件でメール本文の送信を許可しました。',
          });
          saved = true;
        }
      });
      return saved;
    },
    async saveCustom(input: Parameters<MailProcessingAPI['savePolicy']>[0]) {
      if (input.enabled && view.policyReloadRequired) return false;
      let saved = false;
      await mutation(async (signal, seq) => {
        const policy = await api.processing.savePolicy(input, token, signal);
        if (current(seq)) {
          const message = input.enabled
            ? '対象のメール本文の送信を個別条件で許可しました。'
            : '対象のメール本文の送信許可を停止しました。';
          emit({
            policy,
            policyReloadRequired: true,
            success: message,
          });
          saved = true;
          try {
            const latest = await api.processing.policy(token, signal);
            if (!current(seq)) return;
            const sameContext =
              latest.runtime_version === policy.runtime_version &&
              latest.profile?.id === policy.profile?.id;
            emit({
              policy: latest,
              policyReloadRequired: !sameContext,
              success: sameContext
                ? message
                : `${message} 有効版が変わったため、最新の状態を読み直して送信条件を確認してください。`,
            });
          } catch (error) {
            if (!current(seq)) return;
            if (
              error instanceof ApiError &&
              ['authentication_required', 'access_denied'].includes(error.code)
            )
              fail(error, seq);
            else
              emit({
                policyReloadRequired: true,
                success: `${message} 送信条件の再取得を確認できませんでした。最新の状態を読み直してください。`,
              });
          }
        }
      });
      return saved;
    },
    dispose() {
      disposed = true;
      generation++;
      abort.abort();
      routePending = null;
      pendingRequest = null;
      view = { ...emptyAISetupView(), status: 'disposed' };
      changed(view);
    },
  };
}
