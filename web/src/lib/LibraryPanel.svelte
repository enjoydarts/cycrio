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
  :where(.library-panel) :global(button),
  :where(.library-panel) :global(.button) {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    min-height: 44px;
    max-width: 100%;
    padding: 9px 15px;
    border: 1px solid var(--color-border);
    border-radius: 6px;
    background: var(--color-surface);
    color: var(--color-text);
    font: inherit;
    font-size: 12px;
    text-decoration: none;
    overflow-wrap: anywhere;
  }
  :where(.library-panel) :global(button.primary),
  :where(.library-panel) :global(.button.primary) {
    border-color: var(--color-action);
    background: var(--color-action);
    color: var(--color-on-action);
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
  .library-panel :global(.library-tabs) > :global(a),
  .library-panel :global(.library-tabs) > :global(button) {
    display: flex;
    align-items: center;
    flex: none;
    gap: 6px;
    width: auto;
    min-height: 58px;
    margin: 0;
    padding: 18px 0 16px;
    border: 0;
    border-bottom: 2px solid transparent;
    border-radius: 0;
    background: transparent;
    color: var(--color-muted);
    font: inherit;
    font-size: 12px;
    line-height: 1.5;
    text-decoration: none;
    white-space: nowrap;
  }
  .library-panel :global(.library-tabs) > :global([aria-current]),
  .library-panel :global(.library-tabs) > :global([aria-pressed='true']) {
    color: var(--color-action);
    border-bottom-color: var(--color-action);
    font-weight: 600;
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
  .library-panel :global(.library-toolbar select),
  .library-panel :global(.library-toolbar button) {
    min-height: 40px;
    font-size: 12px;
    margin: 0;
    border-radius: 6px;
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
  :where(.library-panel) :global(table) {
    width: 100%;
    border-collapse: collapse;
    border: 0;
    margin: 0;
    background: var(--color-surface);
  }
  :where(.library-panel) :global(thead) {
    background: var(--color-table-header, var(--color-surface-soft));
  }
  :where(.library-panel) :global(th) {
    color: var(--color-muted);
    font-weight: 500;
    font-size: 11px;
    padding: 16px 22px;
    text-align: left;
  }
  :where(.library-panel) :global(td) {
    font-size: 12px;
    padding: 20px 22px;
    border-top: 1px solid var(--color-border);
    overflow-wrap: anywhere;
  }
  :where(.library-panel) :global(button:disabled) {
    opacity: 0.5;
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
    :where(.library-panel) :global(th),
    :where(.library-panel) :global(td) {
      padding: 16px;
    }
  }
</style>
