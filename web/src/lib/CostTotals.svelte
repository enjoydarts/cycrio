<script lang="ts">
  import type { Totals } from './airuntime-api';
  let { totals }: { totals: Totals[] } = $props();
  const calculated = $derived(
    totals.filter(
      (total) => total.currency && total.calls > total.unknown_calls,
    ),
  );
</script>

<ul class="cost-totals">
  {#each calculated as total}
    <li>
      <strong>{total.currency}</strong>
      <span>集計済みコスト {total.amount} {total.currency}</span><span
        class="muted"
        >推定 {total.estimated_amount} / 報告 {total.reported_amount}</span
      >
      <span>集計済み {total.calls - total.unknown_calls} 件</span>
    </li>
  {:else}<li>
      {totals.length
        ? '集計できる金額はありません'
        : '対象の呼出しはありません'}
    </li>{/each}
</ul>
