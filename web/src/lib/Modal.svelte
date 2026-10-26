<script lang="ts">
  import type { Snippet } from 'svelte';
  let {
    open,
    title,
    onclose,
    busy = false,
    children,
  }: {
    open: boolean;
    title: string;
    onclose: () => void;
    busy?: boolean;
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
    // Repeated native close requests can become noncancelable. Block the
    // keyboard default while sending, even when a disabled button loses focus.
    if (dialog?.open && busy && event.key === 'Escape') event.preventDefault();
  }}
/>

<dialog
  bind:this={dialog}
  aria-labelledby={titleId}
  oncancel={(event) => {
    if (busy) event.preventDefault();
  }}
  {onclose}
>
  <header>
    <h2 id={titleId} bind:this={heading} tabindex="-1">{title}</h2>
    <button
      type="button"
      disabled={busy}
      aria-label="閉じる"
      onclick={() => dialog?.close()}><span aria-hidden="true">×</span></button
    >
  </header>
  {@render children()}
</dialog>

<style>
  dialog {
    width: calc(100% - 32px);
    max-width: 900px;
    max-height: calc(100svh - 32px);
    overflow: auto;
    overscroll-behavior: contain;
    border: 1px solid var(--color-border);
    border-radius: 10px;
    padding: 24px;
    background: var(--color-surface);
    color: var(--color-text);
    box-shadow: 0 24px 100px #0003;
  }
  dialog::backdrop {
    background: #142f2966;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--color-border);
    margin-bottom: 20px;
  }
  h2 {
    font-size: 18px;
    margin: 0;
    overflow-wrap: anywhere;
  }
  button {
    display: grid;
    place-items: center;
    min-width: 44px;
    min-height: 44px;
    flex-shrink: 0;
    border: 1px solid var(--color-border);
    border-radius: 6px;
    background: var(--color-surface);
    color: var(--color-text);
    font-size: 24px;
  }
  @media (max-width: 500px) {
    dialog {
      padding: 16px;
    }
  }
</style>
