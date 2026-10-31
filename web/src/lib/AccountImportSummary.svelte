<script lang="ts">
  import type { ImportSummary } from './approval-api.ts';
  let { summary }: { summary: ImportSummary } = $props();
  const count = (v: number) => v.toLocaleString('ja-JP');
</script>

<dl class="import-summary" aria-label="このメール接続の取り込み集計">
  <div>
    <dt>取得済み</dt>
    <dd>{count(summary.mail_count)}<small>通</small></dd>
  </div>
  <div>
    <dt>解析済み</dt>
    <dd>
      {count(summary.succeeded_count + summary.unrelated_count)}<small>通</small
      >
    </dd>
  </div>
  <div>
    <dt>解析待ち・処理中</dt>
    <dd>{count(summary.pending_count)}<small>通</small></dd>
  </div>
  <div>
    <dt>確認待ち候補</dt>
    <dd>{count(summary.candidate_count)}<small>件</small></dd>
  </div>
  <div>
    <dt>登録済みサブスク</dt>
    <dd>{count(summary.subscription_count)}<small>契約</small></dd>
  </div>
  <div class:failed={summary.failed_count > 0}>
    <dt>処理失敗</dt>
    <dd>{count(summary.failed_count)}<small>通</small></dd>
  </div>
  {#if summary.unknown_count}<div class="failed">
      <dt>結果不明</dt>
      <dd>{count(summary.unknown_count)}<small>通</small></dd>
    </div>{/if}
</dl>
<p class="summary-note">
  メール数と契約数は別々に集計しています。関連しないメール {count(
    summary.unrelated_count,
  )} 通は登録対象に含みません。
</p>

<style>
  .import-summary {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 24px 16px;
    margin: 0;
    padding: 24px 0;
  }
  .import-summary div {
    min-width: 0;
  }
  .import-summary dt {
    font-size: 12px;
    color: var(--color-muted);
  }
  .import-summary dd {
    font-size: 26px;
    font-weight: 650;
    margin: 6px 0 0;
    overflow-wrap: anywhere;
    font-variant-numeric: tabular-nums;
  }
  .import-summary small {
    font-size: 12px;
    font-weight: 400;
    color: var(--color-muted);
    margin-left: 6px;
  }
  .failed dd {
    color: var(--color-danger);
  }
  .summary-note {
    font-size: 12px;
    color: var(--color-muted);
    padding-bottom: 20px;
  }
  @media (max-width: 550px) {
    .import-summary {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 20px 12px;
    }
    .import-summary dd {
      font-size: 24px;
    }
  }
</style>
