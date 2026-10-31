<script lang="ts">
  import type { Breakdown } from './airuntime-api';
  import { stageNames } from './ai-runtime-controller';
  import { providerNames } from './model-catalog';
  import { usageCounts } from './ai-usage-summary';
  import DataTable from './DataTable.svelte';
  import UsageAmounts from './UsageAmounts.svelte';
  import CostTotals from './CostTotals.svelte';
  let { breakdown }: { breakdown: Breakdown[] } = $props();
</script>

<h2>モデル・処理段階別</h2>
<div class="usage-table">
  <DataTable responsive="cards">
    <caption class="sr-only">選択した条件での原価内訳</caption>
    <thead
      ><tr
        ><th scope="col">提供元 / モデル</th><th scope="col">処理段階</th><th
          scope="col">コスト</th
        ><th scope="col">呼出し / 失敗・結果不明</th></tr
      ></thead
    >
    <tbody>
      {#each breakdown as row}
        {@const counts = usageCounts(row.totals)}
        <tr>
          <td data-label="提供元 / モデル" data-primary
            ><strong>{row.model_name}</strong><span
              >{providerNames[row.provider]}</span
            ></td
          >
          <td data-label="処理段階">{stageNames[row.stage]}</td>
          <td data-label="コスト"
            ><UsageAmounts totals={row.totals} counts={false} />
            <details>
              <summary>推定・報告の内訳</summary><CostTotals
                totals={row.totals}
              />
            </details></td
          >
          <td data-label="呼出し / 失敗・結果不明"
            >{counts.calls} 件 / {counts.failures} 件</td
          >
        </tr>
      {:else}<tr><td colspan="4">呼出し 0 件</td></tr>{/each}
    </tbody>
  </DataTable>
</div>
