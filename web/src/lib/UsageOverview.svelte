<script lang="ts">
  import type { CostReport } from './airuntime-api';
  import { scopeNames } from './ai-runtime-controller';
  import { datetime, providerNames } from './model-catalog';
  import { providerTotals, usageCounts } from './ai-usage-summary';
  import UsageAmounts from './UsageAmounts.svelte';
  import CostTotals from './CostTotals.svelte';
  import UsageBreakdown from './UsageBreakdown.svelte';
  let { report }: { report: CostReport } = $props();
  const counts = $derived(usageCounts(report.totals));
  const providers = $derived(
    providerTotals(report.breakdown).filter(
      (provider) => provider.totals.length,
    ),
  );
</script>

<div class="usage-summary">
  <h2>{report.month} の集計済みコスト</h2>
  <p class="muted">
    {report.provisional
      ? '暫定月（後から更新される可能性があります）'
      : '月次集計'} · 更新 {datetime(report.updated_at)} JST
  </p>
  <div class="usage-metrics">
    <article class="metric cost">
      <h3>コスト</h3>
      <UsageAmounts totals={report.totals} counts={false} />
    </article>
    <article class="metric">
      <h3>呼出し</h3>
      <p class="metric-value">{counts.calls} 件</p>
    </article>
    <article class="metric">
      <h3>失敗・結果不明</h3>
      <p class="metric-value">{counts.failures} 件</p>
    </article>
  </div>
  <details class="calculation">
    <summary>集計の対象・計算方法</summary>
    <p>
      対象期間：{datetime(report.from)} 以上 / {datetime(report.to)} 未満（JST）。現在の絞り込み条件に一致する呼出しを集計しています。
    </p>
    <p>
      通貨ごとに表示し、不明額は合計に含めません。呼出し件数には原価不明の呼出しも含みます。失敗・結果不明は呼出しの結果が失敗または不明の件数です。原価は採用済みの最新記録です。配信先報告への置換後は推定額を重ねて加算しません。
    </p>
    <CostTotals totals={report.totals} />
  </details>
</div>

<h2>提供元別</h2>
<div class="provider-grid">
  {#each providers as provider}
    <article class="provider-card">
      <h3>{providerNames[provider.provider]}</h3>
      <UsageAmounts totals={provider.totals} counts={false} />
      <p class="muted">
        呼出し {provider.calls} 件 · 失敗・結果不明 {provider.failures} 件
      </p>
    </article>
  {:else}<p class="muted">
      {counts.calls
        ? '集計済みの提供元コストはありません。'
        : '対象の呼出しはありません。'}
    </p>{/each}
</div>

<UsageBreakdown breakdown={report.breakdown} />

<details class="administration">
  <summary>管理者テスト・システム処理</summary>
  <div class="provider-grid">
    {#each report.administration as item}
      {@const counts = usageCounts(item.totals)}
      <article class="provider-card">
        <h3>{scopeNames[item.scope]}</h3>
        <UsageAmounts totals={item.totals} counts={false} />
        <p class="muted">
          呼出し {counts.calls} 件 · 失敗・結果不明 {counts.failures} 件
        </p>
        <details>
          <summary>推定・報告の内訳</summary><CostTotals totals={item.totals} />
        </details>
      </article>
    {:else}<p class="muted">対象の呼出しはありません。</p>{/each}
  </div>
</details>

<style>
  h2 {
    font-size: 1rem;
    margin: 0 0 0.75rem;
  }
  h3 {
    font-size: 0.8rem;
    margin: 0;
    font-weight: 600;
  }
  .usage-summary {
    margin-bottom: 2rem;
  }
  .usage-metrics {
    display: grid;
    grid-template-columns: minmax(0, 2fr) repeat(2, minmax(0, 1fr));
    gap: 0.75rem;
    margin: 1rem 0;
  }
  .metric,
  .provider-card {
    min-width: 0;
    border: 1px solid var(--color-border);
    border-radius: 0.7rem;
    padding: 1rem;
    background: var(--color-surface);
    overflow-wrap: anywhere;
  }
  .cost {
    background: var(--color-surface-soft);
    --usage-amount-size: 1.65rem;
  }
  .metric-value {
    font-size: 1.5rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    margin: 1rem 0 0;
  }
  .provider-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 13rem), 1fr));
    gap: 0.75rem;
    margin-bottom: 2rem;
  }
  .provider-card p {
    font-size: 0.8rem;
    margin-bottom: 0;
  }
  .calculation {
    font-size: 0.8rem;
  }
  .administration {
    border-top: 1px solid var(--color-border);
    margin-top: 1.5rem;
    padding-top: 1rem;
  }
  .administration .provider-grid {
    margin-top: 1rem;
  }
  @media (max-width: 600px) {
    .usage-metrics {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    .cost {
      grid-column: 1 / -1;
    }
  }
</style>
