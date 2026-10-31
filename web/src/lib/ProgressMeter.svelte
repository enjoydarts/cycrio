<script lang="ts">
  let {
    value,
    max,
    label,
    description,
  }: { value: number; max: number; label: string; description: string } =
    $props();
  const bounded = $derived(Math.max(0, Math.min(max, value)));
  const percent = $derived(
    max > 0 ? Math.floor((bounded / max) * 10000) / 100 : 0,
  );
</script>

<div class="progress-meter">
  {#if max > 0}<div class="meter-caption">
      <span>{description}</span><span>{percent}%</span>
    </div>
    <progress
      {max}
      value={bounded}
      aria-label={label}
      aria-valuetext={`${description}（${percent}%）`}
    ></progress>
  {:else}<span>{description}</span>{/if}
</div>

<style>
  .progress-meter {
    min-width: 0;
    max-width: 100%;
    font-size: 11px;
    color: var(--color-muted);
    font-variant-numeric: tabular-nums;
  }
  .meter-caption {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 5px;
  }
  progress {
    display: block;
    width: 100%;
    height: 6px;
    border: 0;
    border-radius: 3px;
    overflow: hidden;
    appearance: none;
    background: var(--color-surface-soft);
    accent-color: var(--color-action);
  }
  progress::-webkit-progress-bar {
    background: var(--color-surface-soft);
    border-radius: 3px;
  }
  progress::-webkit-progress-value {
    background: var(--color-action);
    border-radius: 3px;
  }
  progress::-moz-progress-bar {
    background: var(--color-action);
    border-radius: 3px;
  }
</style>
