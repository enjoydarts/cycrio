<script lang="ts">
  import Button from './Button.svelte';
  import ActionLink from './ActionLink.svelte';
  import { paginationState } from './pagination.ts';

  let {
    total,
    offset,
    limit = 20,
    disabled = false,
    label = 'ページ切り替え',
    href,
    onchange,
  }: {
    total: number;
    offset: number;
    limit?: number;
    disabled?: boolean;
    label?: string;
    href?: (offset: number) => string;
    onchange?: (offset: number) => void;
  } = $props();
  const state = $derived(paginationState(total, offset, limit));
  function select(page: number) {
    if (
      !disabled &&
      page !== state.current &&
      page >= 1 &&
      page <= state.totalPages
    )
      onchange?.((page - 1) * limit);
  }
</script>

{#snippet control(
  page: number,
  text: string,
  unavailable = false,
  current = false,
)}
  {#if href && !current}
    <ActionLink
      href={href((page - 1) * limit)}
      disabled={disabled || unavailable}
      aria-label={text === '前へ' || text === '次へ'
        ? `${text}（${page}ページ）`
        : `${page}ページへ`}
      class="page-control">{text}</ActionLink
    >
  {:else}
    <Button
      type="button"
      disabled={disabled || unavailable}
      variant={current ? 'primary' : 'default'}
      aria-current={current ? 'page' : undefined}
      aria-label={current
        ? `${page}ページ（現在のページ）`
        : text === '前へ' || text === '次へ'
          ? `${text}（${page}ページ）`
          : `${page}ページへ`}
      onclick={() => select(page)}
      class="page-control">{text}</Button
    >
  {/if}
{/snippet}

{#if state.totalPages > 1 || offset > 0}
  <nav class="page-navigation" aria-label={label}>
    <ul class="page-items">
      <li>{@render control(state.previous, '前へ', state.current <= 1)}</li>
      {#each state.items as item, index (item === 'ellipsis' ? `gap-${index}` : item)}
        <li>
          {#if item === 'ellipsis'}
            <span class="page-gap" aria-hidden="true">…</span>
          {:else}
            {@render control(item, String(item), false, item === state.current)}
          {/if}
        </li>
      {/each}
      <li>
        {@render control(state.next, '次へ', state.current >= state.totalPages)}
      </li>
    </ul>
  </nav>
{/if}

<style>
  .page-navigation {
    min-width: 0;
    max-width: 100%;
  }
  .page-items {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px;
    padding: 0;
    margin: 0;
    list-style: none;
  }
  .page-items li {
    display: flex;
    flex: 0 0 auto;
  }
  .page-gap {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    min-height: 44px;
    color: var(--color-muted);
  }
  .page-items :global(.page-control) {
    padding-inline: 10px;
    font-variant-numeric: tabular-nums;
  }
</style>
