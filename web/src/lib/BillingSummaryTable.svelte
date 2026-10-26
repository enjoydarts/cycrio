<script lang="ts">
  import type { BillingSummary } from './billing-api.ts';
  let { summary }: { summary: BillingSummary } = $props();
</script>

<div class="billing-population">
  <p>
    対象期間: {summary.start_date} 以上、{summary.end_date} 未満（{summary.timezone}）
  </p>
  <p class="muted">
    確認済みの有効な支払い・返金だけを、明細の日付で集計します。通貨の換算・月額換算は行いません。請求と無効な明細は金額に含みません。
  </p>
</div>
{#if summary.currencies.length}
  <table class="billing-summary-table">
    <thead
      ><tr
        ><th>通貨</th><th>支払い</th><th>返金</th><th>差引</th><th
          >支払い件数</th
        ><th>返金件数</th></tr
      ></thead
    ><tbody>
      {#each summary.currencies as row (row.currency)}<tr
          ><td data-label="通貨"><strong>{row.currency}</strong></td><td
            data-label="支払い"
            class="money">{row.payment_amount}</td
          ><td data-label="返金" class="money">{row.refund_amount}</td><td
            data-label="差引"
            class="money">{row.net_amount}</td
          ><td data-label="支払い件数">{row.payment_count} 件</td><td
            data-label="返金件数">{row.refund_count} 件</td
          ></tr
        >{/each}
    </tbody>
  </table>
{:else}<p class="empty">集計対象の支払い・返金はありません。</p>{/if}
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
