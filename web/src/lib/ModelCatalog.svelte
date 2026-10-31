<script lang="ts">
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { onMount, tick } from 'svelte';
  import { beforeNavigate } from '$app/navigation';
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
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
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
  let purpose = $state<'catalog' | 'sync' | 'add'>('catalog');
  let priceDirty = $state(false),
    syncDirty = $state(false),
    addDirty = $state(false),
    uncertainWrite = $state(false);
  const editorDirty = $derived(!!modelDraft?.dirty || priceDirty);
  const dirty = $derived(editorDirty || syncDirty || addDirty);
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
    uncertainWrite ||
      ['idle', 'loading', 'saving', 'denied', 'disposed'].includes(view.status),
  );
  let searchLocked = $derived(
    uncertainWrite || ['saving', 'denied', 'disposed'].includes(view.status),
  );
  beforeNavigate((navigation) => {
    if (
      view.status === 'saving' ||
      uncertainWrite ||
      (dirty &&
        !window.confirm('保存していない入力があります。破棄して移動しますか？'))
    )
      navigation.cancel();
  });
  $effect(() => {
    if (view.status === 'denied' || view.status === 'disposed') {
      priceDirty = false;
      syncDirty = false;
      addDirty = false;
      uncertainWrite = false;
    }
  });
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
        !uncertainWrite &&
        view.sync.some((s) => s.status === 'queued' || s.status === 'running')
      )
        void controller?.pollSync();
    }, 3000);
    const leave = (event: BeforeUnloadEvent) => {
      if (dirty || view.status === 'saving' || uncertainWrite) {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', leave);
    return () => {
      window.removeEventListener('beforeunload', leave);
      clearInterval(timer);
      controller?.dispose();
    };
  });
  function filter(value: CatalogFilter) {
    void controller?.loadCatalog(value);
  }
  async function openModel(model: Model | null) {
    if (busy || uncertainWrite) return;
    if (
      editorDirty &&
      !window.confirm(
        'モデル・価格の未保存の入力を破棄して詳細を切り替えますか？',
      )
    )
      return;
    priceDirty = false;
    await controller?.select(model);
    await tick();
    if (model && controller?.state.selected?.id === model.id)
      document.getElementById('model-detail-heading')?.focus({
        preventScroll: window.matchMedia('(min-width: 1200px)').matches,
      });
  }
  async function add(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    const form = event.currentTarget as HTMLFormElement;
    const data = new FormData(form);
    if (
      await succeeded(
        controller?.add({
          provider: data.get('provider') as ModelProvider,
          model_id: String(data.get('model_id') ?? '').trim(),
          display_name: String(data.get('display_name') ?? '').trim(),
          text_input: nullableCapability(data.get('text_input')),
          structured_output: nullableCapability(data.get('structured_output')),
        }),
        true,
      )
    ) {
      form.reset();
      addDirty = false;
    }
  }
  async function succeeded(
    operation: Promise<void> | undefined,
    mutation = false,
  ): Promise<boolean> {
    if (!operation) return false;
    await operation;
    if (
      mutation &&
      controller?.state.status !== 'denied' &&
      controller?.state.status !== 'disposed'
    )
      uncertainWrite = controller?.state.error === 'dependency_unavailable';
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
    if (
      await succeeded(
        controller?.update(model, revisionInput(modelDraft)),
        true,
      )
    )
      modelDraft = null;
  }
  async function recoverWrite() {
    if (view.status === 'saving' || view.status === 'loading') return;
    if (await succeeded(controller?.loadCatalog())) uncertainWrite = false;
  }
</script>

<section
  class="library-page"
  aria-labelledby="catalog-heading"
  aria-busy={busy}
>
  <LibraryHeader
    title="モデルカタログ"
    id="catalog-heading"
    eyebrow="SETTINGS"
    description="モデルを選んで、対応機能と参考価格を確認できます。"
  >
    {#snippet action()}
      <Button
        wrap
        type="button"
        disabled={busy}
        onclick={() => controller?.loadCatalog()}>保存状態を再読み込み</Button
      >
    {/snippet}
  </LibraryHeader>
  <LibraryPanel>
    <div class="catalog-ui catalog-body">
      {#if uncertainWrite}<div role="alert" class="error">
          <p>
            保存の結果を確認できません。入力は保持しています。別の保存や詳細の切り替えの前に、保存済みの状態を確認してください。
          </p>
          <Button
            wrap
            disabled={view.status === 'loading' || view.status === 'saving'}
            onclick={recoverWrite}>保存済みの状態を確認</Button
          >
        </div>{/if}
      <div
        class="feedback"
        class:has-feedback={busy || !!view.error || !!view.success}
        aria-live="polite"
        aria-atomic="true"
      >
        {#if busy}<p>読み込み・保存中です。</p>{/if}{#if view.error}<p
            class="error"
            role="alert"
          >
            {catalogErrors[view.error]}
          </p>{/if}{#if view.success}<p class="success">{view.success}</p>{/if}
      </div>
      <nav class="library-tabs" aria-label="カタログの表示">
        {#each [['catalog', 'モデル一覧'], ['sync', '同期・更新'], ['add', 'モデルを追加']] as [value, label]}
          <Button
            wrap
            variant="tab"
            type="button"
            aria-pressed={purpose === value}
            disabled={view.status === 'saving'}
            onclick={() => (purpose = value as typeof purpose)}>{label}</Button
          >
        {/each}
      </nav>
      <section
        class="library-content"
        aria-label="モデル一覧と詳細"
        hidden={purpose !== 'catalog'}
      >
        <ModelSearch
          prefix="catalog"
          providers={modelProviders}
          locked={searchLocked}
          searching={view.status === 'loading'}
          onsearch={filter}
        />
        <div
          class="catalog-layout"
          class:has-selection={view.selected !== null}
        >
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
            <ActionGroup
              class={[
                'actions pagination',
                'part-ModelCatalog-actions',
                'part-ModelCatalog-pagination',
              ]}
            >
              <Button
                wrap
                disabled={busy || !(view.filter.offset ?? 0)}
                onclick={() =>
                  controller?.loadCatalog({
                    ...view.filter,
                    offset: Math.max(0, (view.filter.offset ?? 0) - 20),
                  })}>前の 20 件</Button
              ><Button
                wrap
                disabled={busy || (view.filter.offset ?? 0) + 20 >= view.total}
                onclick={() =>
                  controller?.loadCatalog({
                    ...view.filter,
                    offset: (view.filter.offset ?? 0) + 20,
                  })}>次の 20 件</Button
              >
            </ActionGroup>
          </div>
          {#if view.selected}
            {@const model = view.selected}
            <section
              class="model-detail"
              aria-label={`${model.display_name} の詳細`}
            >
              <h2 id="model-detail-heading" tabindex="-1">
                {model.display_name}
              </h2>
              <p>
                API 掲載：{model.available ? 'あり' : 'なし'} · 情報元：{model.source ===
                'api'
                  ? 'API'
                  : '手入力'}
              </p>
              <details>
                <summary>モデル設定を編集</summary>
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
                    <label for="model-text-override">テキスト入力の上書き</label
                    ><select
                      id="model-text-override"
                      name="text_input"
                      bind:value={draft.values.text_input}
                      oninput={() => {
                        draft.dirty = true;
                      }}
                      disabled={busy}
                      ><option value={null}
                        >提供元の情報を使用（未取得は不明）</option
                      ><option value={true}>対応</option><option value={false}
                        >非対応</option
                      ></select
                    >
                    <label for="model-structured-override"
                      >構造化出力の上書き</label
                    ><select
                      id="model-structured-override"
                      name="structured_output"
                      bind:value={draft.values.structured_output}
                      oninput={() => {
                        draft.dirty = true;
                      }}
                      disabled={busy}
                      ><option value={null}
                        >提供元の情報を使用（未取得は不明）</option
                      ><option value={true}>対応</option><option value={false}
                        >非対応</option
                      ></select
                    >
                    <Button
                      wrap
                      variant="primary"
                      class={['primary']}
                      disabled={busy}>モデル情報を保存</Button
                    >
                    <Button
                      wrap
                      type="button"
                      disabled={busy}
                      onclick={reloadModelDraft}
                      >最新のモデル情報を読み直す（入力を破棄）</Button
                    >
                  </form>{/if}
              </details>
              <details>
                <summary>モデルの仕様・技術情報</summary>
                <p>提供元のモデル ID：<code>{model.model_id}</code></p>
                <p>内部 ID：<code>{model.id}</code></p>
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
              </details>
              {#key model.id}<CatalogPrices
                  {model}
                  prices={view.prices}
                  {busy}
                  save={(input) =>
                    succeeded(controller?.savePrice(model, input), true)}
                  reset={(version) =>
                    succeeded(controller?.resetPrice(model, version), true)}
                  reload={reloadSelected}
                  onDirty={(value) => (priceDirty = value)}
                  invalid={() => controller?.validationError()}
                />{/key}
              <Button wrap disabled={busy} onclick={() => openModel(null)}
                >詳細を閉じる</Button
              >
            </section>
          {/if}
        </div>
      </section>
      <section
        class="library-content"
        aria-label="同期・更新"
        hidden={purpose !== 'sync'}
      >
        <h2>同期・更新</h2>
        <CatalogSync
          states={view.sync}
          {busy}
          configure={(p, input) =>
            succeeded(controller?.configureSync(p, input), true)}
          request={(p) => {
            void succeeded(controller?.requestSync(p), true);
          }}
          reload={() => succeeded(controller?.loadCatalog())}
          onDirty={(value) => (syncDirty = value)}
        />
      </section>
      <section
        class="library-content"
        aria-label="モデルを追加"
        hidden={purpose !== 'add'}
      >
        <h2>モデルを追加</h2>
        <p class="muted">
          未掲載モデルや将来のモデルを保守するための登録です。API
          同期対象のモデルは、同期後に提供元情報が更新されます。
        </p>
        <form onsubmit={add} oninput={() => (addDirty = true)}>
          <label for="add-provider">提供元</label><select
            id="add-provider"
            name="provider"
            disabled={busy}
            >{#each modelProviders as p}<option value={p}
                >{providerNames[p]}</option
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
            ><option value="">不明</option><option value="true">対応</option
            ><option value="false">非対応</option></select
          >
          <label for="add-structured">構造化出力</label><select
            id="add-structured"
            name="structured_output"
            disabled={busy}
            ><option value="">不明</option><option value="true">対応</option
            ><option value="false">非対応</option></select
          >
          <Button wrap variant="primary" class={['primary']} disabled={busy}
            >モデルを追加</Button
          >
        </form>
      </section>
    </div>
  </LibraryPanel>
</section>

<style>
  [hidden] {
    display: none !important;
  }
  .catalog-body {
    margin-top: 0;
  }
  .catalog-body .feedback {
    min-height: 0;
  }
  .catalog-body .has-feedback {
    padding: 1rem 22px;
  }
  .catalog-body :global(.model-list) {
    border: 0;
    border-radius: 0;
  }
  .catalog-body :global(.model-search) {
    border: 0;
    border-radius: 0;
    background: transparent;
    padding: 0 0 1rem;
    margin: 0;
  }
  .model-detail {
    border-top: 1px solid var(--color-border);
    padding-top: 1.5rem;
  }
  @media (max-width: 720px) {
    .catalog-body :global(.model-list td) {
      padding: 0;
    }
  }
</style>
