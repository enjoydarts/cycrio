<script lang="ts">
  import type { Totals } from './airuntime-api';
  let { totals }: { totals: Totals[] } = $props();
</script>

<ul class="cost-totals">
  {#each totals as total}
    <li>
      <strong>{total.currency ?? '通貨不明'}</strong>
      {#if total.currency}<span>既知額 {total.amount} {total.currency}</span
        ><span class="muted"
          >推定 {total.estimated_amount} / 報告 {total.reported_amount}</span
        >{:else}<span>金額不明（0 ではありません）</span>{/if}
      <span
        >{total.calls} 件 · 不明 {total.unknown_calls} 件 · 失敗 {total.failures}
        件</span
      >
    </li>
  {:else}<li>呼出し 0 件</li>{/each}
</ul>
