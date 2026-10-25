<script lang="ts">
  import { onMount } from 'svelte';
  import { type TokenProvider } from './api';
  import {
    modelProviders,
    type CatalogAPI,
    type Model,
    type SettingsState,
  } from './catalog-api';
  import {
    createCatalogController,
    emptyCatalogState,
    providerNames,
    catalogErrors,
    perMillion,
    isPrimaryModel,
    type CatalogState,
  } from './model-catalog';
  import ModelList from './ModelList.svelte';
  import ModelSearch from './ModelSearch.svelte';
  import './catalog.css';
  let {
    api,
    token,
    onDenied,
  }: { api: CatalogAPI; token: TokenProvider; onDenied: () => void } = $props();
  let view = $state<CatalogState>(emptyCatalogState());
  let primary = $state<Model | null>(null);
  let secondary = $state<Model | null>(null);
  let stage = $state<'primary' | 'secondary'>('primary');
  let controller: ReturnType<typeof createCatalogController> | undefined;
  let loadedSettings: SettingsState | null = null;
  const usable = (model: Model) => model.enabled && model.available;
  const eligible = (model: Model) =>
    usable(model) &&
    (stage === 'primary' ? isPrimaryModel(model) : model.provider !== 'liquid');
  let busy = $derived(
    ['idle', 'loading', 'saving', 'denied', 'disposed'].includes(view.status),
  );
  let searchLocked = $derived(
    !view.settings || ['saving', 'denied', 'disposed'].includes(view.status),
  );
  onMount(() => {
    controller = createCatalogController(
      api,
      token,
      (next) => {
        view = next;
        if (next.status === 'denied' || next.status === 'disposed') {
          primary = null;
          secondary = null;
          loadedSettings = null;
        } else if (next.settings !== loadedSettings) {
          loadedSettings = next.settings;
          primary = next.primary;
          secondary = next.secondary;
        }
      },
      onDenied,
    );
    void controller.loadSettings();
    return () => {
      primary = null;
      secondary = null;
      controller?.dispose();
    };
  });
  function choose(model: Model) {
    if (busy || !eligible(model)) return;
    if (stage === 'primary') primary = model;
    else secondary = model;
  }
  function changeStage(value: 'primary' | 'secondary') {
    stage = value;
    void controller?.search({
      provider: value === 'primary' ? 'liquid' : 'openrouter',
      search: '',
      offset: 0,
    });
  }
</script>

{#snippet selection(
  model: Model | null,
  label: string,
  savedPrice: string | null,
  savedModel: string | null,
)}
  <article class="box">
    <h4>{label}</h4>
    {#if model}<strong>{model.display_name}</strong>
      <p>
        <code>{model.model_id}</code><br />{providerNames[model.provider]} · {usable(
          model,
        )
          ? '選択可能'
          : '掲載終了または管理者設定が無効'}
      </p>
      {#if !usable(model)}<p class="error">
          保存済みのモデルは現在利用できません。カタログから選び直してください。
        </p>{/if}
      <p>
        現在の価格：{#if model.current_price}{model.current_price.currency} · 入力
          {perMillion(model.current_price.rates.input?.amount)} / 出力 {perMillion(
            model.current_price.rates.output?.amount,
          )}（100万トークン当たり）{:else}不明・変動（無料を意味しません）{/if}
      </p>
      <dl>
        <div>
          <dt>現在の価格 ID</dt>
          <dd><code>{model.current_price?.id ?? '不明'}</code></dd>
        </div>
        <div>
          <dt>保存済みの価格 ID</dt>
          <dd><code>{savedPrice ?? '不明・未保存'}</code></dd>
        </div>
      </dl>
      {#if savedModel === model.id && savedPrice !== (model.current_price?.id ?? null)}<p
          class="muted"
        >
          保存後に参照価格が変更されています。下書きを保存すると現在の価格 ID
          を記録します。
        </p>{/if}
    {:else}<p class="muted">未選択</p>{/if}
  </article>
{/snippet}

<section
  class="catalog-ui"
  aria-labelledby="ai-settings-heading"
  aria-busy={busy}
>
  <h3 id="ai-settings-heading">AI 設定の下書き</h3>
  <p class="muted">
    一次判定は Liquid AI D1、二次処理は OpenRouter / OpenAI / Anthropic
    のカタログから選択します。接続テスト・有効化は次工程です。
  </p>
  <div class="feedback" aria-live="polite" aria-atomic="true">
    {#if busy}<p>読み込み・保存中です。</p>{/if}{#if view.error}<p
        class="error"
        role="alert"
      >
        {catalogErrors[view.error]}
      </p>{/if}{#if view.success}<p class="success">{view.success}</p>{/if}
  </div>
  <div class="selection-summary">
    {@render selection(
      primary,
      '一次判定',
      view.settings?.primary_price_id ?? null,
      view.settings?.primary_model_id ?? null,
    )}
    {@render selection(
      secondary,
      '二次処理',
      view.settings?.secondary_price_id ?? null,
      view.settings?.secondary_model_id ?? null,
    )}
  </div>
  <div class="actions save-settings">
    <button
      class="primary"
      disabled={busy ||
        !primary ||
        !secondary ||
        !isPrimaryModel(primary) ||
        !usable(secondary) ||
        secondary.provider === 'liquid'}
      onclick={() => {
        if (primary && secondary)
          void controller?.saveSettings(primary, secondary);
      }}>下書きを保存する</button
    ><button disabled={busy} onclick={() => controller?.loadSettings()}
      >保存状態を再読み込み</button
    >
  </div>
  <p class="muted">
    保存時の価格 ID
    を記録します。カタログ価格は条件や割引を含む参考情報で、実際の利用原価ではありません。
  </p>
  <h3>カタログからモデルを選択</h3>
  <div class="actions stage-selector">
    <button
      aria-pressed={stage === 'primary'}
      disabled={busy}
      onclick={() => changeStage('primary')}>一次判定を選ぶ</button
    ><button
      aria-pressed={stage === 'secondary'}
      disabled={busy}
      onclick={() => changeStage('secondary')}>二次処理を選ぶ</button
    >
  </div>
  {#key stage}<ModelSearch
      prefix="settings"
      providers={stage === 'primary'
        ? ['liquid']
        : modelProviders.filter((p) => p !== 'liquid')}
      initialProvider={stage === 'primary' ? 'liquid' : 'openrouter'}
      allProviders={false}
      locked={searchLocked}
      searching={view.status === 'loading'}
      onsearch={(filter) => {
        void controller?.search(filter);
      }}
    />{/key}
  <p class="result-count" aria-live="polite" aria-atomic="true">
    {view.total} 件 · 20 件ずつ表示します。掲載終了・無効のモデルは選択できません。
  </p>
  <ModelList
    models={view.models}
    selectedID={stage === 'primary' ? primary?.id : secondary?.id}
    {busy}
    {eligible}
    actionText="選択"
    actionLabel={(model) =>
      `${stage === 'primary' ? '一次判定' : '二次処理'}に ${model.display_name} を選択`}
    onselect={choose}
  />
  <div class="actions pagination">
    <button
      disabled={busy || !(view.filter.offset ?? 0)}
      onclick={() =>
        controller?.search({
          ...view.filter,
          offset: Math.max(0, (view.filter.offset ?? 0) - 20),
        })}>前の 20 件</button
    ><button
      disabled={busy || (view.filter.offset ?? 0) + 20 >= view.total}
      onclick={() =>
        controller?.search({
          ...view.filter,
          offset: (view.filter.offset ?? 0) + 20,
        })}>次の 20 件</button
    >
  </div>
</section>
