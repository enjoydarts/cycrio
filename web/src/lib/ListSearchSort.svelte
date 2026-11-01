<script lang="ts">
  import { untrack, type Snippet } from 'svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  let {
    q,
    sort,
    options,
    disabled = false,
    onSearch,
    onSort,
    onClear,
    children,
  }: {
    q: string;
    sort: string;
    options: readonly { value: string; label: string }[];
    disabled?: boolean;
    onSearch: (query: string) => void;
    onSort: (sort: string) => void;
    onClear: () => void;
    children?: Snippet;
  } = $props();
  let draft = $state(untrack(() => q));
  let issue = $state('');
  const id = $props.id();
  const committedQuery = $derived(q);
  $effect(() => {
    draft = committedQuery;
    issue = '';
  });
  function submit(event: SubmitEvent) {
    event.preventDefault();
    if ([...draft.trim()].length > 200) {
      issue = '検索は200文字以内で入力してください。';
      return;
    }
    issue = '';
    onSearch(draft.trim());
  }
</script>

<form class="list-search-sort" onsubmit={submit}>
  <label class="search"
    >サービス・プランを検索
    <input
      type="search"
      name="q"
      value={draft}
      oninput={(event) => {
        draft = event.currentTarget.value;
        issue = '';
      }}
      placeholder="サービス名やプランを検索"
      {disabled}
      aria-invalid={issue ? 'true' : undefined}
      aria-describedby={issue ? `${id}-issue` : undefined}
    />
  </label>
  <ActionGroup>
    <Button wrap type="submit" {disabled}>検索</Button>
    <Button
      wrap
      type="button"
      {disabled}
      onclick={() => {
        draft = '';
        issue = '';
        onClear();
      }}>条件を解除</Button
    >
  </ActionGroup>
  <label class="sort"
    >並び順
    <select
      name="sort"
      value={sort}
      {disabled}
      onchange={(event) => onSort(event.currentTarget.value)}
    >
      {#each options as option}<option value={option.value}
          >{option.label}</option
        >{/each}
    </select>
  </label>
  {#if children}<div class="extra">{@render children()}</div>{/if}
  {#if issue}<p id={`${id}-issue`} role="alert">{issue}</p>{/if}
</form>

<style>
  .list-search-sort {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 12px;
    padding: 18px 22px;
  }
  label {
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-size: 12px;
    min-width: 0;
  }
  .search {
    flex: 1 1 230px;
    max-width: 390px;
  }
  input,
  select {
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
  }
  .sort {
    flex: 0 1 240px;
  }
  .extra {
    margin-left: auto;
  }
  p {
    width: 100%;
    margin: 0;
    color: var(--color-muted);
    font-size: 12px;
  }
  @media (max-width: 650px) {
    .list-search-sort {
      padding: 14px;
      gap: 10px;
    }
    .search {
      flex-basis: 100%;
      max-width: none;
    }
    .sort {
      flex: 1 1 200px;
    }
  }
</style>
