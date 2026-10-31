<script lang="ts">
  import ActionGroup from './ActionGroup.svelte';
  import DataTable from './DataTable.svelte';
  import Button from './Button.svelte';
  import ProgressMeter from './ProgressMeter.svelte';
  import {
    importPeriod,
    importStateNames,
    importProcessed,
    importProgressText,
    type ImportExecution,
    type ImportExecutionPage,
  } from './import-executions-api.ts';
  import { processingDate } from './mail-processing-text.ts';
  let {
    page,
    onSelect,
    onRetry,
  }: {
    page: ImportExecutionPage;
    onSelect: (row: ImportExecution) => void;
    onRetry: (row: ImportExecution) => void;
  } = $props();
  const count = (value: number) => value.toLocaleString('ja-JP');
</script>

{#if page.history.length}
  <DataTable
    responsive="cards"
    class={[
      'execution-table',
      'table-ImportExecutionsPanel',
      'part-ImportExecutionsPanel-execution-table',
    ]}
    aria-label="取り込み実行の履歴一覧"
  >
    <thead
      ><tr
        ><th>実行日時・対象期間</th><th>状態</th><th>取得済み</th><th
          >確認待ち</th
        ><th>登録済み</th><th>失敗</th><th>操作</th></tr
      ></thead
    >
    <tbody
      >{#each page.history as row (row.id)}<tr>
          <td class="execution-name"
            ><strong>{processingDate(row.created_at)}</strong><span
              class="secondary"
              >{row.kind === 'automatic'
                ? '自動取り込み'
                : row.kind === 'reanalysis'
                  ? '再解析'
                  : '手動解析'}</span
            ><span class="secondary">{importPeriod(row)}</span></td
          >
          <td data-label="状態"
            ><span class="badge"
              >{importStateNames[row.state] ?? '状態を確認'}</span
            >{#if row.pending_count}<span class="secondary"
                >未処理・処理中 {count(row.pending_count)} 通</span
              >{/if}
            <div class="row-progress">
              <ProgressMeter
                value={importProcessed(row)}
                max={row.discovered_count}
                label="取得済み分の処理進捗"
                description={row.discovered_count
                  ? importProgressText(row)
                  : '処理対象はまだありません'}
              />
            </div></td
          >
          <td data-label="取得済み"
            ><strong>{count(row.discovered_count)} 通</strong><span
              class="secondary">解析済み {count(row.completed_count)} 通</span
            ></td
          >
          <td data-label="確認待ち">{count(row.candidate_count)} 件</td>
          <td data-label="登録済み">{count(row.registered_count)} 契約</td>
          <td data-label="失敗" class:failed={row.failed_count > 0}
            >{count(row.failed_count)} 通{#if row.unknown_count}<span
                class="secondary">結果不明 {count(row.unknown_count)} 通</span
              >{/if}</td
          >
          <td data-actions class="execution-actions"
            ><ActionGroup
              ><Button
                wrap
                onclick={() => onSelect(row)}
                aria-label={`${processingDate(row.created_at)}の取り込み詳細`}
                >詳細</Button
              >{#if row.failed_count}<Button
                  wrap
                  onclick={() => onRetry(row)}
                  aria-label={`${processingDate(row.created_at)}の失敗分を再試行`}
                  >再試行</Button
                >{/if}</ActionGroup
            ></td
          >
        </tr>{/each}</tbody
    >
  </DataTable>
{:else}<div class="empty library-content">
    <h2>取り込み履歴はありません</h2>
    <p>「取り込み設定」から対象期間を確認して開始できます。</p>
  </div>{/if}

<style>
  .execution-name {
    overflow-wrap: anywhere;
  }
  .failed {
    color: var(--color-danger);
  }
  .row-progress {
    margin-top: 8px;
    min-width: 0;
    max-width: 100%;
  }
</style>
