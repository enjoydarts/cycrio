<script lang="ts">
  import type { Totals } from './airuntime-api';
  let { totals, counts = true }: { totals: Totals[]; counts?: boolean } =
    $props();
</script>

<dl class="usage-amounts">
  {#each totals as total}
    <div>
      <dt>{total.currency ?? '通貨不明'}</dt>
      <dd>
        <strong
          >{total.currency
            ? `${total.amount} ${total.currency}`
            : '金額不明（0 ではありません）'}</strong
        >
        {#if counts}<small
            >{total.calls} 件 · 不明 {total.unknown_calls} 件 · 失敗 {total.failures}
            件</small
          >{:else if total.unknown_calls}<small
            >不明原価 {total.unknown_calls} 件は金額に含みません</small
          >{/if}
      </dd>
    </div>
  {:else}<div>
      <dt>利用原価</dt>
      <dd>呼出し 0 件</dd>
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
    font-size: 1.1rem;
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
  }
  small {
    display: block;
    margin-top: 0.3rem;
    color: var(--color-muted);
  }
</style>
