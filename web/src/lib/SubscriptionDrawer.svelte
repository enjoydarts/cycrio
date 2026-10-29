<script lang="ts">
  import type { Snippet } from 'svelte';
  let {
    open,
    busy = false,
    onclose,
    children,
  }: {
    open: boolean;
    busy?: boolean;
    onclose: () => void;
    children: Snippet;
  } = $props();
  const titleId = $props.id();
  let dialog = $state<HTMLDialogElement>();
  let heading = $state<HTMLHeadingElement>();
  $effect(() => {
    if (!dialog || !open) return;
    const element = dialog;
    const invoker =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    if (!element.open) element.showModal();
    heading?.focus();
    return () => {
      if (element.open) element.close();
      if (invoker?.isConnected) invoker.focus();
    };
  });
</script>

<svelte:window
  onkeydown={(event) => {
    if (dialog?.open && busy && event.key === 'Escape') event.preventDefault();
  }}
/>
<dialog
  bind:this={dialog}
  aria-labelledby={titleId}
  oncancel={(event) => {
    event.preventDefault();
    if (!busy) onclose();
  }}
>
  <header>
    <h2 id={titleId} bind:this={heading} tabindex="-1">サブスクの詳細</h2>
    <button
      type="button"
      disabled={busy}
      aria-label="詳細を閉じる"
      onclick={onclose}>×</button
    >
  </header>
  <div class="body">{@render children()}</div>
</dialog>

<style>
  dialog {
    box-sizing: border-box;
    margin: auto;
    padding: 0;
    width: calc(100% - 32px);
    max-width: 900px;
    max-height: calc(100svh - 32px);
    border: 1px solid var(--color-border);
    border-radius: 10px;
    background: var(--color-surface);
    color: var(--color-text);
    box-shadow: 0 24px 100px #0003;
    overflow: auto;
    overscroll-behavior: contain;
  }
  dialog::backdrop {
    background: var(--color-overlay);
  }
  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 16px;
    padding: 16px 24px;
    border-bottom: 1px solid var(--color-border);
  }
  h2 {
    font-size: 18px;
    margin: 0;
    overflow-wrap: anywhere;
  }
  button {
    display: grid;
    place-items: center;
    flex-shrink: 0;
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: 6px;
    color: inherit;
    min-width: 44px;
    min-height: 44px;
    font-size: 24px;
    cursor: pointer;
  }
  .body {
    padding: 24px;
  }
  @media (max-width: 650px) {
    .body {
      padding: 20px 16px;
    }
    header {
      padding: 14px 16px;
    }
  }
</style>
