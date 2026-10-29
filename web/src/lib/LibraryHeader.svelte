<script lang="ts">
  import type { Snippet } from 'svelte';
  import './library-ui.css';
  let {
    title,
    description,
    eyebrow,
    id,
    action,
  }: {
    title: string;
    description?: string;
    eyebrow?: string;
    id?: string;
    action?: Snippet;
  } = $props();
</script>

<header class="library-header">
  <div>
    {#if eyebrow}<span class="eyebrow">{eyebrow}</span>{/if}
    <h1 {id}>{title}</h1>
    {#if description}<p>{description}</p>{/if}
  </div>
  {#if action}<div class="header-action">{@render action()}</div>{/if}
</header>

<style>
  .library-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 24px;
    margin-bottom: 28px;
    min-width: 0;
  }
  .library-header > div {
    min-width: 0;
  }
  .eyebrow {
    font-size: 10px;
    letter-spacing: 1.7px;
    color: var(--color-muted);
    font-weight: 600;
  }
  h1 {
    font-size: 28px;
    letter-spacing: -0.7px;
    margin: 9px 0 8px;
    font-weight: 650;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }
  p {
    color: var(--color-muted);
    font-size: 13px;
    margin: 0;
  }
  .header-action {
    display: flex;
    gap: 10px;
    flex: none;
    align-items: center;
  }
  .header-action :global(button),
  .header-action :global(a) {
    display: inline-flex;
    justify-content: center;
    align-items: center;
    min-height: 44px;
    padding: 10px 16px;
    margin: 0;
    border: 1px solid var(--color-border);
    border-radius: 6px;
    color: var(--color-text);
    background: var(--color-surface);
    font: inherit;
    font-size: 12px;
    font-weight: 500;
    text-decoration: none;
    white-space: normal;
    max-width: 100%;
  }
  .header-action :global(.primary) {
    background: var(--color-action);
    border-color: var(--color-action);
    color: var(--color-on-action);
  }
  .header-action :global(button:disabled) {
    opacity: 0.5;
  }
  @media (max-width: 700px) {
    .library-header {
      align-items: flex-start;
      flex-wrap: wrap;
      gap: 14px;
      margin-bottom: 22px;
    }
    h1 {
      font-size: 24px;
    }
    .header-action {
      max-width: 100%;
      flex-wrap: wrap;
    }
  }
</style>
