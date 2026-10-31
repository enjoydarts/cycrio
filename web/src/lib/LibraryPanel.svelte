<script lang="ts">
  import type { Snippet } from 'svelte';
  let { children, padded = false }: { children: Snippet; padded?: boolean } =
    $props();
</script>

<div class="library-panel" class:padded>{@render children()}</div>

<style>
  .library-panel {
    min-width: 0;
    border: 1px solid var(--color-border);
    border-radius: 12px;
    background: var(--color-surface);
    overflow: clip;
  }
  .padded {
    padding: 24px;
  }
  .library-panel :global(.library-tabs) {
    display: flex;
    align-items: center;
    gap: 22px;
    min-width: 0;
    margin: 0;
    padding: 0 22px;
    overflow-x: auto;
    border: 0;
    border-bottom: 1px solid var(--color-border);
    border-radius: 0;
    background: transparent;
  }
  .library-panel :global(.library-toolbar) {
    display: flex;
    align-items: end;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 10px;
    padding: 18px 22px;
    margin: 0;
    border-bottom: 1px solid var(--color-border);
  }
  .library-panel :global(.library-toolbar label) {
    margin: 0;
    font-size: 12px;
    font-weight: 400;
  }
  .library-panel :global(.library-toolbar input),
  .library-panel :global(.library-toolbar select) {
    margin: 0;
  }
  .library-panel :global(.library-content) {
    min-width: 0;
    padding: 22px;
  }
  .library-panel :global(.library-footer) {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
    padding: 16px 22px;
    border-top: 1px solid var(--color-border);
    margin: 0;
    font-size: 12px;
    color: var(--color-muted);
  }
  @media (max-width: 700px) {
    .padded {
      padding: 18px;
    }
    .library-panel :global(.library-tabs) {
      gap: 18px;
      padding-inline: 16px;
    }
    .library-panel :global(.library-toolbar),
    .library-panel :global(.library-content),
    .library-panel :global(.library-footer) {
      padding: 16px;
    }
  }
</style>
