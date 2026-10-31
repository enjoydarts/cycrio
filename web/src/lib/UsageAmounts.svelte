<script lang="ts">
  import type { Totals } from './airuntime-api';
  let { totals, counts = true }: { totals: Totals[]; counts?: boolean } =
    $props();
  const calculated = $derived(
    totals.filter(
      (total) => total.currency && total.calls > total.unknown_calls,
    ),
  );
</script>

<dl class="usage-amounts">
  {#each calculated as total}
    <div>
      <dt>{total.currency}</dt>
      <dd>
        <strong>{total.amount} {total.currency}</strong>
        {#if counts}<small
            >集計済み {total.calls - total.unknown_calls} 件</small
          >{/if}
      </dd>
    </div>
  {:else}<div>
      <dt>集計済みコスト</dt>
      <dd>
        {totals.length
          ? '集計できる金額はありません'
          : '対象の呼出しはありません'}
      </dd>
    </div>{/each}
</dl>

<style>
  .usage-amounts {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 12rem), 1fr));
    gap: 1rem;
    margin: 1rem 0;
  }
  dt {
    font-size: 0.75rem;
    color: var(--color-muted);
  }
  dd {
    margin: 0;
  }
  strong {
    display: block;
    font-size: var(--usage-amount-size, 1.1rem);
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
  }
  small {
    display: block;
    margin-top: 0.3rem;
    color: var(--color-muted);
  }
</style>
