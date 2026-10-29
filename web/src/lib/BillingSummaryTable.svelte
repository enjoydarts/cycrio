<script lang="ts">
  import type { BillingSummary } from './billing-api.ts';
  let { summary }: { summary: BillingSummary } = $props();
</script>

<p class="period library-content">
  {summary.month} · 日本時間 · 保存済みの明細
</p>
{#if summary.currencies.length}
  <table class="billing-summary-table">
    <thead
      ><tr><th>通貨</th><th>支払い</th><th>返金</th><th>差引</th></tr></thead
    ><tbody>
      {#each summary.currencies as row (row.currency)}<tr
          ><td data-label="通貨"><strong>{row.currency}</strong></td><td
            data-label="支払い"
            class="money"
            >{row.payment_amount}<small>{row.payment_count} 件</small></td
          ><td data-label="返金" class="money"
            >{row.refund_amount}<small>{row.refund_count} 件</small></td
          ><td data-label="差引" class="money">{row.net_amount}</td></tr
        >{/each}
    </tbody>
  </table>
{:else}<p class="empty library-content">
    集計対象の支払い・返金はありません。
  </p>{/if}
<details class="calculation library-content">
  <summary>集計条件・対象外の明細</summary>
  <div class="billing-population">
    <p>
      対象期間: {summary.start_date} 以上、{summary.end_date} 未満（{summary.timezone}）
    </p>
    <p class="muted">
      自動記録と本人が保存した有効な支払い・返金を、明細の日付で集計します。通貨の換算・月額換算は行いません。請求と無効な明細は金額に含みません。
    </p>
  </div>
  <dl class="field-grid billing-counts">
    <div>
      <dt>請求件数（有効・金額集計の対象外）</dt>
      <dd>{summary.invoice_count} 件</dd>
    </div>
    <div>
      <dt>無効な明細（全種別・金額集計の対象外）</dt>
      <dd>{summary.voided_count} 件</dd>
    </div>
  </dl>
</details>

<style>
  .period {
    color: var(--color-muted);
    font-size: 12px;
    margin-bottom: 14px;
  }
  small {
    display: block;
    margin-top: 5px;
    font-size: 11px;
    color: var(--color-muted);
  }
  .calculation {
    padding-top: 18px;
    color: var(--color-muted);
    font-size: 12px;
  }
  summary {
    cursor: pointer;
    min-height: 30px;
  }
  .billing-population {
    margin-top: 12px;
    line-height: 1.8;
  }
  @media (max-width: 740px) {
    table.billing-summary-table tr {
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 14px;
      padding: 18px 16px;
    }
    table.billing-summary-table tr td {
      padding: 0;
      border: 0;
      min-width: 0;
    }
    table.billing-summary-table tr td:first-child {
      grid-column: 1/-1;
      width: 100%;
    }
    table.billing-summary-table tr td:first-child::before {
      display: none;
    }
  }
</style>
