<script lang="ts">
  import { onMount } from 'svelte';
  import { beforeNavigate } from '$app/navigation';
  import { page } from '$app/state';
  import { getAppSession } from './app-context.ts';
  import {
    createAISetupController,
    emptyAISetupView,
    setupStage,
    preferredRoute,
    canSaveSetup,
    draftNeedsSave,
    type SetupStage,
  } from './ai-setup-controller.ts';
  import {
    isPrimaryModel,
    perMillion,
    providerNames,
    datetime,
  } from './model-catalog.ts';
  import type { Model, Price, ModelProvider } from './catalog-api.ts';
  import {
    pendingTest,
    outcomeText,
    testStateNames,
    runtimeErrors,
    reasonText,
  } from './ai-runtime-controller.ts';
  import { safeAnalysisReturn } from './setup-readiness.ts';
  import StandardMailPolicy from './StandardMailPolicy.svelte';
  import SetupPrice from './SetupPrice.svelte';
  import './setup.css';
  let { initialStage }: { initialStage?: SetupStage } = $props();
  const session = getAppSession();
  let view = $state(emptyAISetupView()),
    controller = $state<ReturnType<typeof createAISetupController>>(),
    pane = $state<SetupStage>('models'),
    initialized = $state(false),
    confirmed = $state(false),
    search = $state(''),
    provider = $state<ModelProvider>('openrouter'),
    priceModelID = $state<string | null>(null),
    priceDirty = $state(false),
    consentDirty = $state(false);
  const busy = $derived(
    ['idle', 'loading', 'saving', 'denied', 'disposed'].includes(view.status),
  );
  const pending = $derived(pendingTest(view.runtime?.latest_test));
  const locked = $derived(busy || pending || view.requestUncertain);
  const selection = $derived(view.selection?.values),
    primary = $derived(
      view.models.find((m) => m.id === selection?.primary) ?? null,
    ),
    secondary = $derived(
      view.models.find((m) => m.id === selection?.secondary) ?? null,
    ),
    route = $derived(
      view.routes?.routes.find((r) => r.id === selection?.route_id) ?? null,
    ),
    priceModel = $derived(
      view.models.find((m) => m.id === priceModelID) ?? null,
    );
  const missingKey = $derived(
    view.credentials.find(
      (k) =>
        (k.provider === 'liquid' || k.provider === secondary?.provider) &&
        k.state !== 'configured',
    ),
  );
  const dirty = $derived(!!view.selection?.dirty || priceDirty || consentDirty);
  const staleBasis = $derived(
    !!view.selection &&
      !!view.settings &&
      (view.selection.baseline_version !== view.settings.version ||
        view.runtimeBaseline !== view.runtime?.version),
  );
  const back = $derived(
    safeAnalysisReturn(page.url.searchParams.get('return_to')),
  );
  const primaryOptions = $derived(
    view.models.filter((m) => m.enabled && m.available && isPrimaryModel(m)),
  );
  const secondaryOptions = $derived(
    view.models.filter(
      (m) =>
        m.enabled &&
        m.available &&
        m.provider === provider &&
        m.provider !== 'liquid' &&
        (provider !== 'openrouter' ||
          !!preferredRoute(view.routes?.routes ?? [], m.id)) &&
        `${m.display_name} ${m.model_id}`
          .toLocaleLowerCase()
          .includes(search.toLocaleLowerCase()),
    ),
  );
  const sameActive = $derived(
    !!view.runtime?.active &&
      view.runtime.active.id === view.runtime.draft?.id &&
      view.runtime.active_ready,
  );
  const canTest = $derived(
    !!view.runtime?.draft &&
      !view.selection?.dirty &&
      view.runtime.draft.catalog_settings_id === view.settings?.id &&
      !locked &&
      !missingKey &&
      !staleBasis &&
      !draftNeedsSave(view),
  );
  const canActivate = $derived(
    !!view.runtime?.draft &&
      view.runtime.latest_test?.profile_id === view.runtime.draft.id &&
      view.runtime.latest_test.state === 'succeeded' &&
      !locked &&
      !view.selection?.dirty &&
      !draftNeedsSave(view),
  );
  const known = (price: Price | null | undefined) =>
    !!price?.rates.input && !!price?.rates.output;
  const quote = (model: Model | null) =>
    model?.manual_price ??
    (model?.provider === 'openrouter' ? route?.price : model?.current_price) ??
    null;
  $effect(() => {
    if (!initialized && view.runtime && view.settings) {
      const requested = page.url.searchParams.get('stage');
      pane =
        initialStage ??
        (['models', 'test', 'permission'].includes(requested ?? '')
          ? (requested as SetupStage)
          : setupStage(view));
      provider =
        view.models.find(
          (model) => model.id === view.settings?.secondary_model_id,
        )?.provider ??
        view.runtime.draft?.secondary.provider ??
        'openrouter';
      initialized = true;
    }
  });
  $effect(() => {
    if (view.selection?.dirty || view.requestUncertain) confirmed = false;
  });
  $effect(() => {
    if (view.status === 'denied' || view.status === 'disposed') {
      priceModelID = null;
      priceDirty = false;
      consentDirty = false;
      confirmed = false;
      search = '';
    }
  });
  beforeNavigate((navigation) => {
    if (view.status === 'saving' || view.requestUncertain || pending) {
      navigation.cancel();
      return;
    }
    if (
      dirty &&
      !window.confirm('保存していない入力があります。このページを離れますか？')
    )
      navigation.cancel();
  });
  onMount(() => {
    controller = createAISetupController(
      {
        catalog: session.catalogAPI,
        runtime: session.runtimeAPI,
        processing: session.mailProcessingAPI,
        routes: session.routesAPI,
        keys: session.api,
      },
      session.token,
      (next) => (view = next),
      session.onDenied,
    );
    void controller.load();
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      if (disposed) return;
      if (pendingTest(view.runtime?.latest_test)) await controller?.poll();
      if (!disposed) timer = setTimeout(poll, 2000);
    }
    timer = setTimeout(poll, 2000);
    const leave = (event: BeforeUnloadEvent) => {
      if (
        dirty ||
        view.status === 'saving' ||
        view.requestUncertain ||
        pending
      ) {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', leave);
    return () => {
      disposed = true;
      clearTimeout(timer);
      window.removeEventListener('beforeunload', leave);
      controller?.dispose();
    };
  });
  async function save() {
    if (!canSaveSetup(view, provider)) return;
    confirmed = false;
    await controller?.save();
    if (
      controller?.state().status === 'ready' &&
      !controller.state().partialSaved
    )
      pane = 'test';
  }
  async function test() {
    const charge = confirmed;
    confirmed = false;
    await controller?.requestTest(charge);
  }
  async function activate() {
    confirmed = false;
    await controller?.activate();
    if (controller?.state().runtime?.active_ready) pane = 'permission';
  }
  function show(value: SetupStage) {
    if (locked || priceDirty) return;
    pane = value;
    confirmed = false;
  }
</script>

{#snippet priceSummary(model: Model | null, label: string)}
  <article class="setup-panel">
    <p class="eyebrow">{label}</p>
    {#if model}<h3>{model.display_name}</h3>
      <p class="muted">{providerNames[model.provider]}</p>
      {@const price = quote(model)}
      <p>
        現在の参照価格：{#if known(price)}入力 {perMillion(
            price!.rates.input.amount,
          )} / 出力 {perMillion(price!.rates.output.amount)}
          {price!.currency}（100万トークン当たり）{:else}<strong
            >不明（無料を意味しません）</strong
          >{/if}
      </p>
      {#if price}<p class="muted">
          取得（日本時間）：{datetime(price.captured_at)}{price.source ===
          'manual'
            ? ' · 手入力の上書き価格'
            : ''}
        </p>{/if}
      <button
        disabled={locked || (priceDirty && priceModelID !== model.id)}
        onclick={() => (priceModelID = model.id)}>価格を設定</button
      >
      {#if view.runtime?.active}{@const frozen =
          label === '一次判定'
            ? view.runtime.active.primary.price
            : view.runtime.active.secondary.price}
        <p class="muted">
          有効版に固定した価格：{known(frozen)
            ? `入力 ${perMillion(frozen!.rates.input.amount)} / 出力 ${perMillion(frozen!.rates.output.amount)} ${frozen!.currency}（100万トークン当たり）`
            : '不明'}。価格の保存だけでは有効版は変わりません。
        </p>{/if}
    {:else}<p>モデルを選択してください。</p>{/if}
  </article>
{/snippet}
<section
  class="setup-ui"
  aria-label="AI の準備と本文送信の許可"
  aria-busy={busy}
>
  <div class="setup-heading">
    <div>
      <p class="eyebrow">AI SETUP</p>
      <h2>AI の準備・送信許可</h2>
      <p>モデルを選び、接続を確かめてからメール本文の送信を許可します。</p>
    </div>
    <button
      disabled={locked || priceDirty}
      onclick={() => controller?.refresh()}>状態を更新</button
    >
  </div>
  <div aria-live="polite">
    {#if busy}<p role="status">
        設定を確認・保存しています…
      </p>{/if}{#if view.error}<div class="setup-feedback error" role="alert">
        <p>{runtimeErrors[view.error]}</p>
        {#if view.error === 'credentials_required'}<a
            href="/settings/credentials">API キーを登録・更新する</a
          >{/if}
        {#if view.partialSaved}<p>
            モデル・価格の選択は保存済みです。実行設定は未完了です。入力と元の保存基準を保持しています。
          </p>{/if}<button
          disabled={busy || view.requestUncertain}
          onclick={() => controller?.refresh()}>最新状態を確認</button
        >
      </div>{/if}{#if view.success}<p class="setup-feedback">
        {view.success}
      </p>{/if}
  </div>
  {#if view.status !== 'denied' && view.status !== 'disposed'}
    {#if view.routeError}<div class="setup-feedback" role="alert">
        <p>
          配信先情報をまとめて確認できませんでした。最新の状態を読み直してください。
        </p>
        <button
          disabled={busy ||
            view.routeLoading ||
            pending ||
            view.requestUncertain}
          onclick={() => controller?.refresh()}>配信先の状態を読み直す</button
        >
      </div>{/if}
    {#if missingKey}<div class="setup-feedback">
        <p>
          {providerNames[missingKey.provider as ModelProvider]} の API キーを登録・有効化してください。
        </p>
        <a href="/settings/credentials">API キーを登録・更新する</a>
      </div>{/if}
    {#if !missingKey && view.runtime?.draft && draftNeedsSave(view)}<div
        class="setup-feedback"
      >
        <p>
          モデル選択または API
          キーが、保存した実行設定の版から更新されています。最新のキーを使うにはモデルと実行設定を保存し直してください。
        </p>
        <button disabled={locked || priceDirty} onclick={() => show('models')}
          >モデルと実行設定を保存し直す</button
        >
      </div>{/if}
    {#if view.runtime?.active && !view.runtime.active_ready}<p
        class="setup-feedback"
      >
        現在の有効版：{reasonText(view.runtime.active_blocked_reason)}
      </p>{/if}
    {#if staleBasis}<p class="setup-feedback" role="status">
        保存状態が更新されています。入力と編集を始めた時点の版を保持しています。「最新の保存値を読み直す」で基準を更新してから保存してください。
      </p>{/if}
    <nav class="setup-steps" aria-label="AI 準備のステップ">
      {#each [{ id: 'models', title: '1 モデル・料金' }, { id: 'test', title: '2 接続確認' }, { id: 'permission', title: '3 送信許可' }] as item}<button
          disabled={locked || priceDirty}
          aria-current={pane === item.id ? 'step' : undefined}
          onclick={() => show(item.id as SetupStage)}>{item.title}</button
        >{/each}
    </nav>
    {#if view.runtime?.active_ready && pane !== 'permission'}<div
        class="setup-complete"
      >
        <span>現在の有効版は接続確認済みです。</span><button
          disabled={locked || priceDirty}
          onclick={() => show('permission')}>本文送信の許可を確認</button
        >
      </div>{/if}
    {#if pane !== 'models' && selection}<div class="setup-complete">
        <span
          >モデル：{primary?.display_name ?? '未選択'} → {secondary?.display_name ??
            '未選択'}</span
        ><button disabled={locked || priceDirty} onclick={() => show('models')}
          >モデル・料金を変更</button
        >
      </div>{/if}
    {#if pane === 'models' && selection}
      <h3>モデルと料金</h3>
      <p class="muted">
        OpenRouter
        は、メール解析の条件に適合する候補から選び、接続テストで利用を確認します。配信先情報の更新では有料の推論を行いません。
      </p>
      <div class="setup-grid">
        <label
          >一次判定<select
            value={selection.primary}
            disabled={locked || priceDirty}
            onchange={(e) =>
              controller?.choose('primary', e.currentTarget.value)}
            ><option value="">Liquid AI D1 を選択</option
            >{#each primaryOptions as model}<option value={model.id}
                >{model.display_name}</option
              >{/each}</select
          ></label
        ><label
          >二次処理の接続先<select
            bind:value={provider}
            disabled={locked || priceDirty}
            ><option value="openrouter">OpenRouter（適合配信先から選択）</option
            ><option value="openai">OpenAI（直接接続）</option><option
              value="anthropic">Anthropic（直接接続）</option
            ></select
          ></label
        >
      </div>
      {#if provider === 'openrouter'}
        {#if view.routes && !view.routes.key_available}<div
            class="setup-feedback"
          >
            <p>OpenRouter の API キーが必要です。</p>
            <a href="/settings/credentials">API キーを登録する</a>
          </div>{:else}<div class="setup-route">
            <p>
              {view.routeLoading
                ? '無料の配信先情報を更新しています…'
                : view.routes?.fresh
                  ? '最新の適合配信先を表示しています。'
                  : '配信先情報を更新してから、新しいモデル設定を保存してください。'}
            </p>
            <button
              disabled={busy ||
                view.routeLoading ||
                pending ||
                view.requestUncertain ||
                !view.routes?.key_available}
              onclick={() => controller?.refreshRoutes()}
              >配信先情報を無料で更新</button
            >{#if view.routes?.snapshot}<p class="muted">
                取得（日本時間）：{datetime(view.routes.snapshot.captured_at)}
              </p>{/if}
          </div>{/if}
      {:else}<p class="setup-feedback">
          直接接続の保持・学習利用条件は、利用中のアカウントの個別条件を確認してください。アカウント固有の
          ZDR を自動で確認したものではありません。
        </p>{/if}
      <label
        >二次モデルを検索<input
          type="search"
          bind:value={search}
          disabled={locked || priceDirty}
          placeholder="モデル名で検索"
        /></label
      >
      <label
        >二次処理のモデル<select
          value={secondary?.provider === provider ? selection.secondary : ''}
          disabled={locked ||
            priceDirty ||
            (provider === 'openrouter' && view.routeLoading)}
          onchange={(e) =>
            controller?.choose('secondary', e.currentTarget.value)}
          ><option value="">モデルを選択</option
          >{#each secondaryOptions as model}<option value={model.id}
              >{model.display_name}</option
            >{/each}</select
        ></label
      >
      <p class="muted">
        {secondaryOptions.length} 件の選択可能なモデル。既知の価格と配信先の名称による表示順で、最安値を保証するものではありません。
      </p>
      {#if secondary && secondary.provider !== provider}<p
          class="setup-feedback"
        >
          選んだ接続先に対応する二次モデルを選択してから保存してください。
        </p>{/if}
      {#if secondary?.provider === 'openrouter' && route}<div
          class="setup-route"
        >
          <strong>実際の配信先：{route.provider_name}</strong
          >{#if route.quantization}<span> · {route.quantization}</span>{/if}
          <p>確認済み ZDR · {route.model_name}</p>
          <details>
            <summary>配信先を詳しく選ぶ</summary><label
              >適合する配信先<select
                value={route.id}
                disabled={locked || priceDirty}
                onchange={(e) => controller?.chooseRoute(e.currentTarget.value)}
                >{#each view.routes?.routes.filter((r) => r.eligible && r.model_id === secondary?.id) ?? [] as option}<option
                    value={option.id}
                    >{option.provider_name}{option.quantization
                      ? ` · ${option.quantization}`
                      : ''}</option
                  >{/each}</select
              ></label
            >
            <p class="muted">
              同じ名前でも配信方式が異なる場合があります。適合する配信先だけを表示します。
            </p>
          </details>
        </div>{/if}
      {#if secondary?.provider === 'openrouter' && !route}<div
          class="setup-feedback"
        >
          <p>
            選択していた配信先の適合を確認できません。最新の情報を確認して選び直してください。
          </p>
          <label
            >適合する配信先<select
              value=""
              disabled={locked || view.routeLoading || priceDirty}
              onchange={(e) => controller?.chooseRoute(e.currentTarget.value)}
              ><option value="">配信先を選び直す</option
              >{#each view.routes?.routes.filter((r) => r.eligible && r.model_id === secondary?.id) ?? [] as option}<option
                  value={option.id}
                  >{option.provider_name}{option.quantization
                    ? ` · ${option.quantization}`
                    : ''}</option
                >{/each}</select
            ></label
          >
        </div>{/if}
      <div class="setup-grid">
        {@render priceSummary(primary, '一次判定')}{@render priceSummary(
          secondary,
          '二次処理',
        )}
      </div>
      {#if priceModel}{#key priceModel.id}<SetupPrice
            model={priceModel}
            {busy}
            save={(model, input) => controller!.savePrice(model, input)}
            onclose={() => (priceModelID = null)}
            onDirty={(value) => (priceDirty = value)}
          />{/key}{/if}
      <details class="setup-details">
        <summary>出力・待機上限と接続テストの技術設定</summary>
        <div class="setup-grid">
          {#each [{ key: 'max_input_bytes', label: '旧方式・接続テストの入力基準（bytes）', min: 1024, max: 65536 }, { key: 'max_output_tokens', label: '出力上限（tokens）', min: 64, max: 16384 }, { key: 'timeout_seconds', label: '待機上限（秒）', min: 1, max: 120 }] as field}<label
              >{field.label}<input
                type="number"
                min={field.min}
                max={field.max}
                value={selection[field.key as 'max_input_bytes']}
                disabled={locked || priceDirty || view.routeLoading}
                onchange={(e) =>
                  controller?.editLimits({
                    [field.key]: Number(e.currentTarget.value),
                  })}
              /></label
            >{/each}
        </div>
        <p class="muted">
          通常の解析では本文全体を送信します。入力基準は旧方式・接続テスト用で、全文の上限やモデルの文脈容量の保証ではありません。基準を変更した場合は、配信先情報も更新して適合を確認してください。
        </p>
        {#if secondary?.provider === 'openrouter' && (view.routeLimits?.max_input_bytes !== selection.max_input_bytes || view.routeLimits?.max_output_tokens !== selection.max_output_tokens)}<button
            disabled={locked ||
              view.routeLoading ||
              !view.routes?.key_available}
            onclick={() => controller?.refreshRoutes()}
            >この上限で配信先を無料で確認</button
          >{/if}
      </details>
      <div class="setup-actions">
        <button
          class="primary"
          disabled={!canSaveSetup(view, provider) || priceDirty}
          onclick={save}>モデルと実行設定を保存</button
        ><button
          disabled={locked || priceDirty}
          onclick={() => controller?.rebase()}
          >最新の保存値を読み直す（入力を破棄）</button
        >
      </div>
      <p class="muted">
        保存で料金は発生しません。参考価格が不明でも保存できます。接続テストと有効化はそれぞれ明示的に行います。
      </p>
    {:else if pane === 'test'}
      <h3>接続テスト・有効化</h3>
      {#if !view.runtime?.draft}<p>先にモデルと実行設定を保存してください。</p>
        <button disabled={locked} onclick={() => show('models')}
          >モデルを選ぶ</button
        >{:else}
        <p>
          固定のテスト入力を一次判定と二次処理へ送信します。料金が発生します。
        </p>
        {#if view.selection?.dirty}<p class="setup-feedback">
            変更したモデル・料金を先に保存してください。
          </p>
          <button disabled={locked} onclick={() => show('models')}
            >変更を保存する</button
          >{/if}
        {#if view.runtime.latest_test}{@const run = view.runtime.latest_test}
          <div class="setup-panel">
            <h3>接続テスト：{testStateNames[run.state]}</h3>
            <div class="setup-grid">
              <div>
                <strong>一次判定</strong>
                <p>{outcomeText(run.primary_outcome)}</p>
                {#if run.primary_failure}<p class="setup-feedback">
                    {run.primary_failure.hint}
                  </p>{/if}
              </div>
              <div>
                <strong>二次処理</strong>
                <p>{outcomeText(run.secondary_outcome)}</p>
                {#if run.secondary_failure}<p class="setup-feedback">
                    {run.secondary_failure.hint}
                  </p>{/if}
              </div>
            </div>
            {#if run.primary_failure?.reason === 'credentials_rejected' || run.secondary_failure?.reason === 'credentials_rejected'}<a
                href="/settings/credentials">API キーを更新する</a
              >{/if}
          </div>{/if}
        {#if view.requestUncertain}<div class="setup-feedback">
            <p>
              受付状況を確認できません。同じ要求で受付を確認します。新しい有料テストは作成しません。
            </p>
            <button
              disabled={busy}
              onclick={() => controller?.requestTest(true)}
              >同じテスト要求の受付を確認</button
            >
          </div>{:else}<label class="check"
            ><input
              type="checkbox"
              checked={confirmed}
              disabled={!canTest}
              onchange={(e) => (confirmed = e.currentTarget.checked)}
            />外部 AI へのテスト送信と料金の発生を確認しました</label
          >
          <div class="setup-actions">
            <button disabled={!confirmed || !canTest} onclick={test}
              >料金を確認して接続テスト</button
            ><button
              class="primary"
              disabled={!canActivate || sameActive}
              onclick={activate}
              >{sameActive
                ? 'この版は有効化済み'
                : '成功したテストの版を有効化'}</button
            >
          </div>{/if}
        {#if sameActive}<button
            class="primary"
            disabled={locked}
            onclick={() => show('permission')}>本文送信の許可へ進む</button
          >{/if}
        {#if view.runtime?.active}<details class="setup-details">
            <summary>AI の実行を停止する</summary>
            <p class="muted">
              保存した下書きは保持し、新しいメール解析への AI
              呼出しを停止します。
            </p>
            <button disabled={locked} onclick={() => controller?.deactivate()}
              >AI の実行を停止</button
            >
          </details>{/if}
      {/if}
    {:else if pane === 'permission' && !view.policy}
      <p>本文送信の準備状況を確認しています。</p>
    {/if}
    {#if view.policy}<div hidden={pane !== 'permission'}>
        <StandardMailPolicy
          state={view.policy}
          {busy}
          reloadRequired={view.policyReloadRequired}
          save={(input) => controller!.saveStandard(input)}
          saveCustom={(input) => controller!.saveCustom(input)}
          onDirty={(value) => (consentDirty = value)}
        />
        {#if view.policyReloadRequired || (!view.policy.standard_terms && view.policy.profile?.secondary.provider === 'openrouter')}<button
            disabled={locked}
            onclick={() => controller?.refresh()}>送信条件を読み直す</button
          >{/if}
        {#if view.policy?.standard_terms && !view.policy.standard_terms.ready && view.policy.profile?.secondary.provider === 'openrouter'}<button
            disabled={locked ||
              view.routeLoading ||
              !view.routes?.key_available}
            onclick={() => controller?.refreshRoutes()}
            >送信条件の配信先情報を無料で更新</button
          >{/if}
      </div>{/if}
    <div class="setup-actions">
      <a class="setup-button" href={back}
        >{page.url.searchParams.has('return_to')
          ? '解析へ戻って条件を確認'
          : '解析へ進む'}</a
      ><a href="/settings">設定の準備状況</a>
    </div>
  {/if}
</section>
