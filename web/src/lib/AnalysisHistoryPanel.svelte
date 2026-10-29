<script lang="ts">
  import type { AnalysisRun, RunPage } from './analysis-api.ts';
  import { processingActive } from './mail-processing-controller.ts';
  import { runNames } from './daily-text.ts';
  import { processingDate } from './mail-processing-text.ts';
  let {
    page,
    saving = null,
    onCancel,
  }: {
    page: RunPage;
    saving?: string | null;
    onCancel: (run: AnalysisRun) => void;
  } = $props();
</script>

<ul class="compact-list history-list">
  {#each page.runs as run (run.id)}<li>
      <div class="run-row">
        <div class="run-main">
          <strong>{run.account_label}</strong><span class="secondary"
            >{processingDate(run.created_at)} · 日本時間</span
          >
        </div>
        <div class="run-progress">
          <span class="badge">{runNames[run.state]}</span><span
            >{run.completed_count} / {run.total_count} 件</span
          >
        </div>
        {#if processingActive(run)}<button
            disabled={!!saving}
            onclick={() => onCancel(run)}
            >{saving === run.id ? '停止中…' : '停止'}</button
          >{/if}
      </div>
      {#if run.failed_count || run.unknown_count}<p class="run-warning">
          {#if run.failed_count}失敗 {run.failed_count} 件{/if}{#if run.unknown_count}
            {#if run.failed_count}
              ·
            {/if}結果不明 {run.unknown_count} 件{/if}
        </p>{/if}
      <details>
        <summary>内訳</summary>
        <p>
          関連 {run.related_count} · 非関連 {run.unrelated_count} · 失敗 {run.failed_count}
          · 結果不明 {run.unknown_count}
        </p>
        <p>指定上限 {run.max_messages} 件</p>
      </details>
    </li>{:else}<li class="empty">実行履歴はありません。</li>{/each}
</ul>

<style>
  .run-row {
    display: flex;
    align-items: center;
    gap: 18px;
    justify-content: space-between;
  }
  .run-main {
    flex: 1;
    min-width: 0;
  }
  .run-progress {
    display: flex;
    gap: 12px;
    align-items: center;
    font-size: 12px;
  }
  .run-warning {
    margin-top: 10px !important;
    color: var(--color-danger);
    font-size: 12px;
  }
  details {
    font-size: 12px;
    color: var(--color-muted);
    margin-top: 10px;
  }
  summary {
    cursor: pointer;
    display: inline-block;
    min-height: 24px;
  }
  details p {
    margin-top: 8px !important;
  }
  @media (max-width: 550px) {
    .run-row {
      flex-wrap: wrap;
      gap: 12px;
    }
    .run-main {
      flex-basis: 100%;
    }
    .run-progress {
      flex: 1;
    }
    .run-progress .badge {
      font-size: 11px;
    }
  }
</style>
