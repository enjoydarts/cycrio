<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { type TokenProvider } from './api';
  import {
    modelProviders,
    type CatalogAPI,
    type ModelProvider,
    type Model,
    type ModelUpdate,
    type CatalogFilter,
  } from './catalog-api';
  import {
    createCatalogController,
    emptyCatalogState,
    nullableCapability,
    providerNames,
    catalogErrors,
    updateInput,
    reconcileRevisionDraft,
    revisionInput,
    type RevisionDraft,
    type CatalogState,
  } from './model-catalog';
  import CatalogPrices from './CatalogPrices.svelte';
  import CatalogSync from './CatalogSync.svelte';
  import ModelList from './ModelList.svelte';
  import ModelSearch from './ModelSearch.svelte';
  import './catalog.css';
  let {
    api,
    token,
    onDenied,
  }: { api: CatalogAPI; token: TokenProvider; onDenied: () => void } = $props();
  let view = $state<CatalogState>(emptyCatalogState());
  let controller: ReturnType<typeof createCatalogController> | undefined;
  let modelDraft = $state<RevisionDraft<
    Omit<ModelUpdate, 'expected_version'>
  > | null>(null);
  let draftModelID = $state<string | null>(null);
  $effect(() => {
    const model = view.selected;
    if (!model) {
      modelDraft = null;
      draftModelID = null;
      return;
    }
    const { expected_version, ...values } = updateInput(model);
    modelDraft = reconcileRevisionDraft(
      draftModelID === model.id ? modelDraft : null,
      expected_version,
      values,
    );
    draftModelID = model.id;
  });
  let busy = $derived(
    ['idle', 'loading', 'saving', 'denied', 'disposed'].includes(view.status),
  );
  let searchLocked = $derived(
    ['saving', 'denied', 'disposed'].includes(view.status),
  );
  onMount(() => {
    controller = createCatalogController(
      api,
      token,
      (next) => {
        view = next;
      },
      onDenied,
    );
    void controller.loadCatalog();
    const timer = setInterval(() => {
      if (
        view.sync.some((s) => s.status === 'queued' || s.status === 'running')
      )
        void controller?.pollSync();
    }, 3000);
    return () => {
      clearInterval(timer);
      controller?.dispose();
    };
  });
  function filter(value: CatalogFilter) {
    void controller?.loadCatalog(value);
  }
  async function openModel(model: Model) {
    await controller?.select(model);
    await tick();
    if (controller?.state.selected?.id === model.id)
      document.getElementById('model-detail-heading')?.focus({
        preventScroll: window.matchMedia('(min-width: 1200px)').matches,
      });
  }
  function add(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    const data = new FormData(event.currentTarget as HTMLFormElement);
    void controller?.add({
      provider: data.get('provider') as ModelProvider,
      model_id: String(data.get('model_id') ?? '').trim(),
      display_name: String(data.get('display_name') ?? '').trim(),
      text_input: nullableCapability(data.get('text_input')),
      structured_output: nullableCapability(data.get('structured_output')),
    });
  }
  async function succeeded(
    operation: Promise<void> | undefined,
  ): Promise<boolean> {
    if (!operation) return false;
    await operation;
    return (
      controller?.state.status === 'ready' && controller.state.error === null
    );
  }
  const reloadSelected = () =>
    view.selected
      ? succeeded(controller?.select(view.selected))
      : Promise.resolve(false);
  async function reloadModelDraft() {
    if (await reloadSelected()) modelDraft = null;
  }
  async function update(event: SubmitEvent, model: Model) {
    event.preventDefault();
    if (busy || !modelDraft) return;
    if (await succeeded(controller?.update(model, revisionInput(modelDraft))))
      modelDraft = null;
  }
</script>

<section class="catalog-ui" aria-labelledby="catalog-heading" aria-busy={busy}>
  <div class="catalog-title">
    <h3 id="catalog-heading">モデルカタログ</h3>
    <button
      type="button"
      disabled={busy}
      onclick={() => controller?.loadCatalog()}>保存状態を再読み込み</button
    >
  </div>
  <p class="muted">
    提供元の情報と管理者の上書きを管理します。利用するモデルは「AI
    設定」で、このカタログから選択します。
  </p>
  <div class="feedback" aria-live="polite" aria-atomic="true">
    {#if busy}<p>読み込み・保存中です。</p>{/if}{#if view.error}<p
        class="error"
        role="alert"
      >
        {catalogErrors[view.error]}
      </p>{/if}{#if view.success}<p class="success">{view.success}</p>{/if}
  </div>
  <ModelSearch
    prefix="catalog"
    providers={modelProviders}
    locked={searchLocked}
    searching={view.status === 'loading'}
    onsearch={filter}
  />
  <div class="catalog-layout" class:has-selection={view.selected !== null}>
    <div class="model-browser">
      <p class="result-count" aria-live="polite" aria-atomic="true">
        {view.total} 件 · {view.total
          ? (view.filter.offset ?? 0) + 1
          : 0}〜{Math.min(
          (view.filter.offset ?? 0) + view.models.length,
          view.total,
        )} 件を表示（20 件ずつ）
      </p>
      <ModelList
        models={view.models}
        selectedID={view.selected?.id}
        {busy}
        actionLabel={(model) => `${model.display_name} の詳細・価格`}
        onselect={openModel}
      />
      <p class="muted price-note">
        料金は参考値です。条件別料金・割引は詳細で確認できます。「不明」は無料を意味しません。
      </p>
      <div class="actions pagination">
        <button
          disabled={busy || !(view.filter.offset ?? 0)}
          onclick={() =>
            controller?.loadCatalog({
              ...view.filter,
              offset: Math.max(0, (view.filter.offset ?? 0) - 20),
            })}>前の 20 件</button
        ><button
          disabled={busy || (view.filter.offset ?? 0) + 20 >= view.total}
          onclick={() =>
            controller?.loadCatalog({
              ...view.filter,
              offset: (view.filter.offset ?? 0) + 20,
            })}>次の 20 件</button
        >
      </div>
    </div>
    {#if view.selected}
      {@const model = view.selected}
      <section
        class="box selected model-detail"
        aria-label={`${model.display_name} の詳細`}
      >
        <h3 id="model-detail-heading" tabindex="-1">{model.display_name}</h3>
        <code>{model.id}</code>
        <p>
          API 掲載：{model.available ? 'あり' : 'なし'} · 情報元：{model.source ===
          'api'
            ? 'API'
            : '手入力'}
        </p>
        {#if modelDraft}
          {@const draft = modelDraft}
          {#if draft.dirty && draft.baseline_version !== model.version}<p
              class="error"
              role="status"
            >
              編集中にモデル情報が更新されました。入力は保持しています。最新の保存値を読み直してから、もう一度編集してください。
            </p>{/if}
          <form onsubmit={(event) => update(event, model)}>
            <label for="model-name-override"
              >表示名の上書き（空欄で提供元の名前に戻す）</label
            ><input
              id="model-name-override"
              name="display_name"
              bind:value={draft.values.display_name}
              oninput={() => {
                draft.dirty = true;
              }}
              disabled={busy}
            />
            <label
              ><input
                type="checkbox"
                name="enabled"
                bind:checked={draft.values.enabled}
                oninput={() => {
                  draft.dirty = true;
                }}
                disabled={busy}
              />管理者設定を有効にする</label
            >
            <label for="model-text-override">テキスト入力の上書き</label><select
              id="model-text-override"
              name="text_input"
              bind:value={draft.values.text_input}
              oninput={() => {
                draft.dirty = true;
              }}
              disabled={busy}
              ><option value={null}>提供元の情報を使用（未取得は不明）</option
              ><option value={true}>対応</option><option value={false}
                >非対応</option
              ></select
            >
            <label for="model-structured-override">構造化出力の上書き</label
            ><select
              id="model-structured-override"
              name="structured_output"
              bind:value={draft.values.structured_output}
              oninput={() => {
                draft.dirty = true;
              }}
              disabled={busy}
              ><option value={null}>提供元の情報を使用（未取得は不明）</option
              ><option value={true}>対応</option><option value={false}
                >非対応</option
              ></select
            >
            <button class="primary" disabled={busy}>モデル情報を保存</button>
            <button type="button" disabled={busy} onclick={reloadModelDraft}
              >最新のモデル情報を読み直す（入力を破棄）</button
            >
          </form>{/if}
        <dl>
          <div>
            <dt>コンテキスト長</dt>
            <dd>{model.context_length ?? '不明'}</dd>
          </div>
          <div>
            <dt>最大入力 / 出力トークン</dt>
            <dd>
              {model.max_input_tokens ?? '不明'} / {model.max_output_tokens ??
                '不明'}
            </dd>
          </div>
        </dl>
        {#if model.raw_metadata}<details>
            <summary>提供元のモデル情報</summary>
            <pre>{JSON.stringify(model.raw_metadata, null, 2)}</pre>
          </details>{/if}
        {#key model.id}<CatalogPrices
            {model}
            prices={view.prices}
            {busy}
            save={(input) => succeeded(controller?.savePrice(model, input))}
            reset={(version) =>
              succeeded(controller?.resetPrice(model, version))}
            reload={reloadSelected}
            invalid={() => controller?.validationError()}
          />{/key}
        <button disabled={busy} onclick={() => controller?.select(null)}
          >詳細を閉じる</button
        >
      </section>
    {/if}
  </div>
  <CatalogSync
    states={view.sync}
    {busy}
    configure={(p, input) => succeeded(controller?.configureSync(p, input))}
    request={(p) => {
      void controller?.requestSync(p);
    }}
    reload={() => succeeded(controller?.loadCatalog())}
  />
  <details>
    <summary>モデルを手入力で追加</summary>
    <p class="muted">
      未掲載モデルや将来のモデルを保守するための登録です。API
      同期対象のモデルは、同期後に提供元情報が更新されます。
    </p>
    <form onsubmit={add}>
      <label for="add-provider">提供元</label><select
        id="add-provider"
        name="provider"
        disabled={busy}
        >{#each modelProviders as p}<option value={p}>{providerNames[p]}</option
          >{/each}</select
      >
      <label for="add-model-id">提供元のモデル ID</label><input
        id="add-model-id"
        name="model_id"
        required
        disabled={busy}
      />
      <label for="add-model-name">表示名</label><input
        id="add-model-name"
        name="display_name"
        required
        disabled={busy}
      />
      <label for="add-text">テキスト入力</label><select
        id="add-text"
        name="text_input"
        disabled={busy}
        ><option value="">不明</option><option value="true">対応</option><option
          value="false">非対応</option
        ></select
      >
      <label for="add-structured">構造化出力</label><select
        id="add-structured"
        name="structured_output"
        disabled={busy}
        ><option value="">不明</option><option value="true">対応</option><option
          value="false">非対応</option
        ></select
      >
      <button class="primary" disabled={busy}>モデルを追加</button>
    </form>
  </details>
</section>
