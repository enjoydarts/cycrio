<script lang="ts">
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { shiftMonth, replaceMonthPart } from './month-selection.ts';
  let {
    value,
    currentMonth,
    disabled = false,
    onchange,
  }: {
    value: string;
    currentMonth: string;
    disabled?: boolean;
    onchange: (value: string) => void;
  } = $props();
  const years = Array.from({ length: 200 }, (_, index) => String(2199 - index));
  const months = Array.from({ length: 12 }, (_, index) =>
    String(index + 1).padStart(2, '0'),
  );
  const previous = $derived(shiftMonth(value, -1));
  const next = $derived(shiftMonth(value, 1));
  function select(next: string | null) {
    if (!disabled && next && next !== value) onchange(next);
  }
</script>

<fieldset class="month-selector" {disabled}>
  <legend>集計月（日本時間）</legend>
  <div class="controls">
    <div class="selection">
      <select
        aria-label="集計年"
        value={value.slice(0, 4)}
        {disabled}
        onchange={(event) =>
          select(replaceMonthPart(value, 'year', event.currentTarget.value))}
      >
        {#each years as year}<option value={year}>{year}年</option>{/each}
      </select>
      <select
        aria-label="集計月"
        value={value.slice(5, 7)}
        {disabled}
        onchange={(event) =>
          select(replaceMonthPart(value, 'month', event.currentTarget.value))}
      >
        {#each months as month}<option value={month}>{Number(month)}月</option
          >{/each}
      </select>
    </div>
    <ActionGroup>
      <Button
        type="button"
        disabled={disabled || !previous}
        onclick={() => select(previous)}>前月</Button
      >
      <Button
        type="button"
        disabled={disabled || !next}
        onclick={() => select(next)}>翌月</Button
      >
      <Button
        type="button"
        disabled={disabled || value === currentMonth}
        onclick={() => select(currentMonth)}>今月</Button
      >
    </ActionGroup>
  </div>
</fieldset>

<style>
  .month-selector {
    border: 0;
    padding: 0;
    margin: 0;
    min-width: 0;
  }
  legend {
    padding: 0;
    margin-bottom: 8px;
    font-size: 12px;
  }
  .controls {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
  }
  .selection {
    display: flex;
    gap: 8px;
    min-width: 0;
  }
  select {
    min-width: 0;
    width: auto;
    margin: 0;
  }
</style>
