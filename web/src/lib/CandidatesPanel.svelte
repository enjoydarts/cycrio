<script lang="ts">
  import ActionGroup from './ActionGroup.svelte';
  import DataTable from './DataTable.svelte';
  import Button from './Button.svelte';
  import type { Candidate, CandidatePage } from './approval-api.ts';
  import { money, cycle, eventNames } from './daily-text.ts';
  let {
    page,
    onSelect,
    onDismiss = () => {},
  }: {
    page: CandidatePage;
    onSelect: (candidate: Candidate) => void;
    onDismiss?: (candidate: Candidate) => void;
  } = $props();
</script>

{#if page.candidates.length}
  <DataTable
    responsive="cards"
    class={[
      'candidate-table',
      'table-CandidatesPanel',
      'part-CandidatesPanel-candidate-table',
    ]}
  >
    <thead><tr><th>契約候補</th><th>抽出金額・周期</th><th>確認</th></tr></thead
    ><tbody>
      {#each page.candidates as candidate (`${candidate.result_id}/${candidate.event_index}`)}
        <tr
          ><td
            ><strong>{candidate.event.service}</strong><span class="secondary"
              >{candidate.event.plan ?? 'プラン不明'} · {eventNames[
                candidate.event.event_type
              ]}</span
            ><span class="secondary">{candidate.account_label}</span></td
          >
          <td data-label="抽出金額・周期"
            ><span class="money"
              >{money(candidate.event.amount, candidate.event.currency)}</span
            ><span class="secondary"
              >{cycle(
                candidate.event.interval_unit,
                candidate.event.interval_count,
              )}</span
            ></td
          >
          <td data-actions
            ><ActionGroup
              ><Button wrap type="button" onclick={() => onSelect(candidate)}
                >確認する</Button
              ><Button wrap type="button" onclick={() => onDismiss(candidate)}
                >不要</Button
              ></ActionGroup
            ></td
          ></tr
        >
      {/each}</tbody
    >
  </DataTable>
{:else}<div class="empty library-content">
    <h2>確認待ちの候補はありません</h2>
    <p>メールから契約情報が見つかると、ここで確認・承認できます。</p>
  </div>{/if}

<style>
  :global(.part-CandidatesPanel-candidate-table) th:first-child {
    width: 50%;
  }
  :global(.part-CandidatesPanel-candidate-table) td {
    overflow-wrap: anywhere;
  }
  :global(.part-CandidatesPanel-candidate-table) th:last-child {
    width: 140px;
  }
  @media (max-width: 550px) {
    :global(.part-CandidatesPanel-candidate-table) th:first-child {
      width: auto;
    }
    :global(.part-CandidatesPanel-candidate-table) td {
      min-width: 0;
    }
    :global(.part-CandidatesPanel-candidate-table) td:last-child {
      padding-top: 8px;
    }
  }
</style>
