<script lang="ts">
  import ActionGroup from './ActionGroup.svelte';
  import DataTable from './DataTable.svelte';
  import Button from './Button.svelte';
  import { type Model } from './catalog-api';
  import { modelRate, providerNames } from './model-catalog';
  let {
    models,
    selectedID = null,
    busy = false,
    eligible = () => true,
    actionLabel,
    actionText = '詳細・価格',
    onselect,
  }: {
    models: Model[];
    selectedID?: string | null;
    busy?: boolean;
    eligible?: (model: Model) => boolean;
    actionLabel: (model: Model) => string;
    actionText?: string;
    onselect: (model: Model) => void;
  } = $props();
</script>

<div class="model-list">
  <DataTable
    responsive="cards"
    class={['table-ModelList']}
    aria-label="モデル一覧"
  >
    <thead
      ><tr>
        <th scope="col">モデル</th><th scope="col">提供元</th>
        <th scope="col" class="numeric"
          >入力料金<span> / 100万トークン</span></th
        >
        <th scope="col" class="numeric"
          >出力料金<span> / 100万トークン</span></th
        >
        <th scope="col">コンテキスト</th><th scope="col">状態</th><th
          scope="col"><span class="sr-only">操作</span></th
        >
      </tr></thead
    >
    <tbody>
      {#each models as model (model.id)}
        <tr class:active-row={selectedID === model.id}>
          <td class="model-name" data-label="モデル"
            ><strong>{model.display_name}</strong><code>{model.model_id}</code
            ></td
          >
          <td data-label="提供元">{providerNames[model.provider]}</td>
          <td class="numeric" data-label="入力 / 100万トークン"
            >{modelRate(model, 'input')}</td
          >
          <td class="numeric" data-label="出力 / 100万トークン"
            >{modelRate(model, 'output')}</td
          >
          <td data-label="コンテキスト"
            >{model.context_length?.toLocaleString('ja-JP') ?? '不明'}</td
          >
          <td data-label="状態"
            ><span
              class="model-state"
              class:usable={model.enabled && model.available}
              >{!model.enabled
                ? '無効'
                : !model.available
                  ? '掲載終了'
                  : '有効'}</span
            ></td
          >
          <td data-actions class="model-action"
            ><ActionGroup
              ><Button
                wrap
                variant="tab"
                type="button"
                disabled={busy || !eligible(model)}
                aria-label={actionLabel(model)}
                aria-pressed={selectedID === model.id}
                onclick={() => onselect(model)}
                >{selectedID === model.id ? '選択中' : actionText}</Button
              ></ActionGroup
            ></td
          >
        </tr>
      {/each}
    </tbody>
  </DataTable>
  {#if models.length === 0}<div class="empty-models">
      <strong>該当するモデルがありません</strong>
      <p>検索語や提供元を変更するか、条件をクリアしてください。</p>
    </div>{/if}
</div>
