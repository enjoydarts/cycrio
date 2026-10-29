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
    margin: 0 0 0 auto;
    padding: 0;
    width: min(560px, 100%);
    max-width: 100%;
    height: 100svh;
    max-height: 100svh;
    border: 0;
    border-left: 1px solid var(--color-border);
    background: var(--color-surface);
    color: var(--color-text);
    box-shadow: -16px 0 80px #0002;
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
    padding: 18px 26px;
    border-bottom: 1px solid var(--color-border);
  }
  h2 {
    font-size: 15px;
    margin: 0;
  }
  button {
    background: transparent;
    border: 0;
    color: inherit;
    min-width: 40px;
    min-height: 40px;
    font-size: 25px;
    cursor: pointer;
  }
  .body {
    padding: 28px;
  }
  @media (max-width: 650px) {
    .body {
      padding: 22px 18px;
    }
    header {
      padding: 14px 18px;
    }
  }
</style>
