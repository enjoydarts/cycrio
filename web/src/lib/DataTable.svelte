<script lang="ts">
  import type { HTMLTableAttributes } from 'svelte/elements';
  import type { Snippet } from 'svelte';
  let {
    children,
    class: className,
    responsive = 'scroll',
    ...attributes
  }: HTMLTableAttributes & {
    children: Snippet;
    responsive?: 'scroll' | 'cards' | 'compact';
  } = $props();
</script>

<div class="ui-table-scroll">
  <table
    {...attributes}
    class={['ui-table', className]}
    class:cards={responsive === 'cards'}
    class:compact={responsive === 'compact'}
  >
    {@render children()}
  </table>
</div>

<style>
  .ui-table-scroll {
    position: relative;
    min-width: 0;
    max-width: 100%;
    overflow-x: auto;
  }
  .ui-table {
    width: 100%;
    border-collapse: collapse;
    border: 0;
    margin: 0;
    background: var(--color-surface);
    font-variant-numeric: tabular-nums;
  }
  .ui-table :global(thead) {
    background: var(--color-table-header, var(--color-surface-soft));
  }
  .ui-table :global(th) {
    padding: 16px 22px;
    text-align: left;
    color: var(--color-muted);
    font-size: 11px;
    font-weight: 500;
  }
  .ui-table :global(td) {
    padding: 20px 22px;
    border-top: 1px solid var(--color-border);
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .ui-table :global([data-actions]) {
    width: 1%;
    min-width: max-content;
    white-space: nowrap;
  }
  .ui-table :global([data-actions] .ui-actions) {
    min-width: max-content;
  }
  @media (max-width: 700px) {
    .ui-table :global(th),
    .ui-table :global(td) {
      padding: 16px;
    }
  }
  @media (max-width: 800px) {
    .cards,
    .cards :global(tbody) {
      display: block;
    }
    .cards :global(thead) {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip-path: inset(50%);
    }
    .cards :global(tr) {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 12px 16px;
      padding: 20px 16px;
      border-bottom: 1px solid var(--color-border);
    }
    .cards :global(td) {
      display: block;
      min-width: 0;
      width: auto;
      padding: 0;
      border: 0;
    }
    .cards :global(td:first-child),
    .cards :global([data-primary]),
    .cards :global([data-actions]) {
      grid-column: 1 / -1;
      width: auto;
    }
    .cards :global(td[data-label])::before {
      content: attr(data-label);
      display: block;
      color: var(--color-muted);
      font-size: 11px;
      margin-bottom: 5px;
    }
    .cards :global([data-actions]),
    .cards :global([data-actions] .ui-actions) {
      min-width: 0;
      white-space: normal;
    }
  }
  @media (max-width: 650px) {
    .compact :global(td) {
      padding: 0;
      border: 0;
    }
  }
</style>
