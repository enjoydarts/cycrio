<script lang="ts">
  import ActionGroup from './ActionGroup.svelte';
  import Button from './Button.svelte';
  let {
    selectedCount,
    pageCount,
    allPageSelected,
    partialPageSelected,
    disabled = false,
    onTogglePage,
    onClear,
    totalCount,
    onSelectAll,
    busy = false,
    label = 'メール',
  }: {
    selectedCount: number;
    pageCount: number;
    allPageSelected: boolean;
    partialPageSelected: boolean;
    disabled?: boolean;
    onTogglePage: (checked: boolean) => void;
    onClear: () => void;
    totalCount?: number;
    onSelectAll?: () => void;
    busy?: boolean;
    label?: string;
  } = $props();
  let pageCheckbox: HTMLInputElement | undefined = $state();
  $effect(() => {
    if (pageCheckbox) pageCheckbox.indeterminate = partialPageSelected;
  });
</script>

<div class="selection-toolbar" aria-label={`${label}の選択`} aria-busy={busy}>
  <ActionGroup spacing="both">
    <label class="check">
      <input
        bind:this={pageCheckbox}
        type="checkbox"
        checked={allPageSelected}
        disabled={disabled || busy || pageCount === 0}
        onchange={(event) => onTogglePage(event.currentTarget.checked)}
      />このページをすべて選択
    </label>
    <Button
      type="button"
      wrap
      disabled={disabled || busy || selectedCount === 0}
      onclick={onClear}>選択解除</Button
    >
    {#if onSelectAll}
      <Button
        type="button"
        wrap
        disabled={disabled || busy || totalCount === 0}
        onclick={onSelectAll}>すべて選択</Button
      >
    {/if}
  </ActionGroup>
  <p role="status">
    {busy ? `${label}を選択中…` : `選択中 ${selectedCount} 件`}
  </p>
</div>
