<script lang="ts">
  import BrandIcon from './BrandIcon.svelte';
  import MailReceivedDate from './MailReceivedDate.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import DataTable from './DataTable.svelte';
  import Button from './Button.svelte';
  import SelectionToolbar from './SelectionToolbar.svelte';
  import type { Candidate, CandidatePage } from './approval-api.ts';
  import { money, cycle, eventNames } from './daily-text.ts';
  let {
    page,
    onSelect,
    onDismiss = () => {},
    filtered = false,
    onClear = () => {},
    selectedCandidates = [],
    onSelectionChange,
    onBulkDismiss,
    onSelectAll,
    selectionBusy = false,
  }: {
    page: CandidatePage;
    onSelect: (candidate: Candidate) => void;
    onDismiss?: (candidate: Candidate) => void;
    filtered?: boolean;
    onClear?: () => void;
    selectedCandidates?: Candidate[];
    onSelectionChange?: (candidates: Candidate[]) => void;
    onBulkDismiss?: () => void;
    onSelectAll?: () => void;
    selectionBusy?: boolean;
  } = $props();
  const key = (c: Candidate) => `${c.result_id}/${c.event_index}`;
  const selectedKeys = $derived(new Set(selectedCandidates.map(key)));
  const pageSelected = $derived(
    page.candidates.filter((c) => selectedKeys.has(key(c))).length,
  );
  function selectRows(rows: Candidate[], checked: boolean) {
    if (selectionBusy) return;
    const next = new Map(selectedCandidates.map((c) => [key(c), c]));
    for (const row of rows) {
      if (checked) next.set(key(row), row);
      else next.delete(key(row));
    }
    onSelectionChange?.([...next.values()]);
  }
</script>

{#if page.candidates.length}
  {#if onSelectionChange}<section
      class="library-content"
      aria-label="候補の一括操作"
    >
      <SelectionToolbar
        selectedCount={selectedCandidates.length}
        pageCount={page.candidates.length}
        allPageSelected={pageSelected === page.candidates.length}
        partialPageSelected={pageSelected > 0 &&
          pageSelected < page.candidates.length}
        disabled={selectionBusy}
        busy={selectionBusy}
        totalCount={page.total}
        label="候補"
        onTogglePage={(checked) => selectRows(page.candidates, checked)}
        onClear={() => onSelectionChange?.([])}
        {onSelectAll}
      />
      {#if selectedCandidates.length && onBulkDismiss}<ActionGroup
          spacing="before"
        >
          <Button
            wrap
            type="button"
            variant="danger"
            disabled={selectionBusy}
            onclick={onBulkDismiss}>選択した候補を除外</Button
          >
        </ActionGroup>{/if}
    </section>{/if}
  <DataTable
    responsive="cards"
    class={[
      'candidate-table',
      'table-CandidatesPanel',
      'part-CandidatesPanel-candidate-table',
    ]}
  >
    <thead><tr><th>契約候補</th><th>取引金額・周期</th><th>確認</th></tr></thead
    ><tbody>
      {#each page.candidates as candidate (`${candidate.result_id}/${candidate.event_index}`)}
        <tr
          ><td data-primary
            >{#if onSelectionChange}<label class="candidate-select"
                ><input
                  type="checkbox"
                  checked={selectedKeys.has(key(candidate))}
                  disabled={selectionBusy}
                  aria-label={`${candidate.event.service ?? 'この候補'}を選択`}
                  onchange={(e) =>
                    selectRows([candidate], e.currentTarget.checked)}
                />選択</label
              >{/if}<strong class="service-heading"
              ><BrandIcon
                name={candidate.event.service}
                size="small"
              />{candidate.event.service ?? 'サービス名未確認'}</strong
            ><span class="secondary"
              >{candidate.event.plan ?? 'プラン不明'} · {eventNames[
                candidate.event.event_type
              ]}</span
            ><span class="secondary">{candidate.account_label}</span>
            {#if candidate.mail_count > 1}<span class="secondary"
                >関連メール {candidate.mail_count} 件</span
              >{/if}
            <span class="secondary"
              ><MailReceivedDate receivedAt={candidate.received_at} /></span
            ></td
          >
          <td data-label="取引金額・周期"
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
              ><Button
                wrap
                type="button"
                disabled={selectionBusy}
                onclick={() => onSelect(candidate)}>確認する</Button
              ><Button
                wrap
                type="button"
                disabled={selectionBusy}
                onclick={() => onDismiss(candidate)}>不要</Button
              ></ActionGroup
            ></td
          ></tr
        >
      {/each}</tbody
    >
  </DataTable>
{:else}<div class="empty library-content">
    <h2>
      {filtered
        ? '条件に一致する候補はありません'
        : '確認待ちの候補はありません'}
    </h2>
    {#if filtered}<p>サービス名やプラン名を変更して検索してください。</p>
      <Button wrap type="button" onclick={onClear}>条件を解除する</Button>
    {:else}<p>
        メールから契約情報が見つかると、ここで確認・承認できます。
      </p>{/if}
  </div>{/if}

<style>
  .candidate-select {
    display: inline-flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
    margin-bottom: 12px;
    min-height: 36px;
  }
  .candidate-select input {
    width: 18px;
    height: 18px;
    accent-color: var(--color-action);
  }
  .service-heading {
    display: flex;
    align-items: center;
    gap: 10px;
  }
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
