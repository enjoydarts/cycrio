<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api.ts';
  import { modelProviders, type ModelProvider } from './catalog-api.ts';
  import {
    createCatalogChangesAPI,
    catalogChangeKinds,
    catalogChangeNames,
    describeCatalogChanges,
    safeCatalogSource,
    type CatalogChangeKind,
    type CatalogChangesAPI,
  } from './catalog-changes-api.ts';
  import {
    createCatalogChangesController,
    emptyCatalogChangesView,
  } from './catalog-changes-controller.ts';
  import { providerNames, datetime, catalogErrors } from './model-catalog.ts';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import DataTable from './DataTable.svelte';
  import Pagination from './Pagination.svelte';
  let {
    token,
    onDenied,
    apiBase = '',
    api,
  }: {
    token: TokenProvider;
    onDenied: () => void;
    apiBase?: string;
    api?: CatalogChangesAPI;
  } = $props();
  let view = $state(emptyCatalogChangesView());
  let provider = $state<ModelProvider | ''>(''),
    kind = $state<CatalogChangeKind | ''>('');
  let controller: ReturnType<typeof createCatalogChangesController> | undefined;
  const busy = $derived(
    ['idle', 'loading', 'denied', 'disposed'].includes(view.status),
  );
  onMount(() => {
    controller = createCatalogChangesController(
      api ?? createCatalogChangesAPI(apiBase),
      token,
      (next) => {
        view = next;
      },
      onDenied,
    );
    void controller.load();
    return () => controller?.dispose();
  });
  function filter(event: SubmitEvent) {
    event.preventDefault();
    void controller?.load({ provider, kind });
  }
</script>

<section
  class="catalog-changes"
  aria-labelledby="catalog-changes-heading"
  aria-busy={busy}
>
  <h2 id="catalog-changes-heading">変更履歴</h2>
  <p class="muted">
    モデル一覧の同期や公式価格調査、管理者の操作で検知した変更です。検知日時は公式のリリース日とは異なります。
  </p>
  <form class="change-filters" onsubmit={filter}>
    <label for="change-provider"
      >提供元<select id="change-provider" bind:value={provider} disabled={busy}
        ><option value="">すべて</option>{#each modelProviders as p}<option
            value={p}>{providerNames[p]}</option
          >{/each}</select
      ></label
    >
    <label for="change-kind"
      >変更の種類<select id="change-kind" bind:value={kind} disabled={busy}
        ><option value="">すべて</option>{#each catalogChangeKinds as k}<option
            value={k}>{catalogChangeNames[k]}</option
          >{/each}</select
      ></label
    >
    <ActionGroup
      ><Button wrap type="submit" disabled={busy}>履歴を絞り込む</Button><Button
        wrap
        type="button"
        disabled={busy}
        onclick={() => controller?.load()}>履歴を再読み込み</Button
      ></ActionGroup
    >
  </form>
  <div aria-live="polite" aria-atomic="true">
    {#if busy}<p>変更履歴を読み込み中です。</p>{/if}
    {#if view.error}<p class="error" role="alert">
        {catalogErrors[view.error]}
      </p>{/if}
  </div>
  {#if view.status !== 'denied' && view.status !== 'disposed'}
    {#if view.status === 'ready' && !view.total}<p>
        条件に一致する変更履歴はありません。履歴の記録開始前の変更は表示されません。
      </p>{/if}
    {#if view.total}<p class="muted">
        {view.total} 件 · {view.offset + 1}〜{Math.min(
          view.offset + view.changes.length,
          view.total,
        )} 件を表示
      </p>{/if}
    <ol class="change-list">
      {#each view.changes as change (change.id)}
        {@const source = safeCatalogSource(change.source_url)}
        {@const differences = describeCatalogChanges(change)}
        <li class="box">
          <div class="change-heading">
            <h3>{change.model_name}</h3>
            <span>{catalogChangeNames[change.kind]}</span>
          </div>
          <p>{providerNames[change.provider]} · {change.upstream_model_id}</p>
          <p class="muted">
            検知日時（日本時間）：<time datetime={change.occurred_at}
              >{datetime(change.occurred_at)}</time
            >
          </p>
          {#if source}<p>
              <a href={source} target="_blank" rel="noopener noreferrer"
                >公式の出典を確認</a
              >
            </p>{:else}<p class="muted">
              この変更に公式の出典リンクはありません。
            </p>{/if}
          <details>
            <summary>変更内容（{differences.length} 項目）</summary>
            {#if differences.length}
              <DataTable
                responsive="cards"
                aria-label={`${change.model_name} の変更内容`}
                ><thead
                  ><tr><th>項目</th><th>変更前</th><th>変更後</th></tr></thead
                ><tbody
                  >{#each differences as difference}<tr
                      ><td data-primary data-label="項目">{difference.label}</td
                      ><td data-label="変更前">{difference.before}</td><td
                        data-label="変更後">{difference.after}</td
                      ></tr
                    >{/each}</tbody
                ></DataTable
              >
            {:else}<p>表示対象の項目に差分はありません。</p>{/if}
          </details>
        </li>
      {/each}
    </ol>
    <Pagination
      total={view.total}
      offset={view.offset}
      limit={view.limit}
      disabled={busy}
      label="変更履歴のページ切り替え"
      onchange={(offset) => controller?.load({ ...view.filter, offset })}
    />
  {/if}
</section>

<style>
  .catalog-changes {
    min-width: 0;
  }
  .change-filters {
    display: flex;
    flex-wrap: wrap;
    align-items: end;
    gap: 1rem;
    margin-block: 1rem;
  }
  .change-filters label {
    display: grid;
    gap: 0.4rem;
    flex: 1 1 12rem;
    min-width: 0;
  }
  .change-filters select {
    width: 100%;
    min-width: 0;
  }
  .change-list {
    list-style: none;
    margin: 1rem 0;
    padding: 0;
    display: grid;
    gap: 1rem;
  }
  .change-list .box {
    margin: 0;
    min-width: 0;
  }
  .change-heading {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.5rem 1rem;
  }
  .change-heading h3 {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .change-list p {
    overflow-wrap: anywhere;
  }
  summary {
    cursor: pointer;
    padding-block: 0.75rem;
  }
</style>
