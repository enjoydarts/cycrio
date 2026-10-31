<script lang="ts">
  import ActionGroup from './ActionGroup.svelte';
  import Button from './Button.svelte';
  import { onDestroy, untrack } from 'svelte';
  import { type ModelProvider, type CatalogFilter } from './catalog-api';
  import { providerNames } from './model-catalog';
  let {
    prefix,
    providers,
    initialProvider = '',
    allProviders = true,
    locked = false,
    searching = false,
    onsearch,
  }: {
    prefix: string;
    providers: readonly ModelProvider[];
    initialProvider?: ModelProvider | '';
    allProviders?: boolean;
    locked?: boolean;
    searching?: boolean;
    onsearch: (filter: CatalogFilter) => void;
  } = $props();
  let provider = $state<ModelProvider | ''>(untrack(() => initialProvider));
  let search = $state('');
  let composing = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const tooLong = $derived([...search].length > 100);
  function cancel() {
    clearTimeout(timer);
  }
  function submit(event?: SubmitEvent) {
    event?.preventDefault();
    cancel();
    if (locked || composing || tooLong) return;
    onsearch({ provider, search: search.trim(), offset: 0 });
  }
  function schedule() {
    cancel();
    if (!composing) timer = setTimeout(() => submit(), 300);
  }
  function clear() {
    provider = initialProvider;
    search = '';
    submit();
  }
  onDestroy(cancel);
</script>

<form class="model-search" onsubmit={submit} aria-label="モデルの絞り込み">
  <div class="search-query">
    <label for={`${prefix}-search`}>モデル名・ID を検索</label>
    <div class="search-input">
      <span aria-hidden="true">⌕</span>
      <input
        id={`${prefix}-search`}
        type="search"
        bind:value={search}
        placeholder="例：Claude、GPT、モデル ID"
        autocomplete="off"
        disabled={locked}
        aria-invalid={tooLong}
        aria-describedby={`${prefix}-search-help`}
        oninput={schedule}
        oncompositionstart={() => {
          composing = true;
          cancel();
        }}
        oncompositionend={() => {
          composing = false;
          schedule();
        }}
      />
    </div>
  </div>
  {#if providers.length > 1 || allProviders}
    <div class="search-provider">
      <label for={`${prefix}-provider`}>提供元</label>
      <select
        id={`${prefix}-provider`}
        bind:value={provider}
        disabled={locked}
        onchange={(event) => {
          provider = event.currentTarget.value as ModelProvider | '';
          submit();
        }}
      >
        {#if allProviders}<option value="">すべての提供元</option>{/if}
        {#each providers as item}<option value={item}
            >{providerNames[item]}</option
          >{/each}
      </select>
    </div>
  {/if}
  <ActionGroup class={['search-buttons', 'part-ModelSearch-search-buttons']}>
    <Button
      wrap
      variant="primary"
      class={['primary']}
      disabled={locked || tooLong}>検索</Button
    >
    <Button
      wrap
      type="button"
      disabled={locked || (!search && provider === initialProvider)}
      onclick={clear}>条件をクリア</Button
    >
  </ActionGroup>
  <p
    id={`${prefix}-search-help`}
    class:search-error={tooLong}
    class="search-help"
  >
    {tooLong
      ? '検索語は100文字以内で入力してください。'
      : searching
        ? '検索中…'
        : '入力すると自動で絞り込みます。Enter ですぐに検索できます。'}
  </p>
</form>
