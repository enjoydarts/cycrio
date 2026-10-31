<script lang="ts">
  import ActionGroup from './ActionGroup.svelte';
  import ActionLink from './ActionLink.svelte';
  import Button from './Button.svelte';
  import ProgressMeter from './ProgressMeter.svelte';
  import {
    importPeriod,
    importStateNames,
    importProcessed,
    importProgressText,
    type ImportExecution,
  } from './import-executions-api.ts';
  import { processingDate } from './mail-processing-text.ts';
  let {
    row,
    account,
    onRetry,
    onStop,
    stopping = false,
    error = null,
  }: {
    row: ImportExecution;
    account: string;
    onRetry: (row: ImportExecution) => void;
    onStop: (row: ImportExecution) => void;
    stopping?: boolean;
    error?: string | null;
  } = $props();
  const count = (value: number) => value.toLocaleString('ja-JP');
</script>

<div class="daily execution-detail">
  <p>
    {processingDate(row.created_at)}（日本時間） · {row.kind === 'automatic'
      ? '自動取り込み'
      : '手動解析'}
  </p>
  <p>{importPeriod(row)}</p>
  <span class="badge">{importStateNames[row.state] ?? '状態を確認'}</span>
  <div class="detail-progress">
    <ProgressMeter
      value={importProcessed(row)}
      max={row.discovered_count}
      label="取得済み分の処理進捗"
      description={row.discovered_count
        ? importProgressText(row)
        : '処理対象はまだありません'}
    />
    <p class="muted">
      処理済みには解析完了・失敗・結果不明を含みます。メール探索中は取得済み通数が増えるため、表示は取得済み分に対する進捗です。
    </p>
  </div>
  <dl>
    {#each [['取得済み', row.discovered_count, '通'], ['解析済み', row.completed_count, '通'], ['未処理・処理中', row.pending_count, '通'], ['確認待ち候補', row.candidate_count, '件'], ['登録済みサブスク', row.registered_count, '契約'], ['処理失敗', row.failed_count, '通'], ['結果不明', row.unknown_count, '通']] as [name, value, unit]}<div
      >
        <dt>{name}</dt>
        <dd>{count(Number(value))} {unit}</dd>
      </div>{/each}
  </dl>
  <p class="muted">
    未処理・処理中は、メールの所在を取得済みで、一次判定や必要な二次抽出が未完了の通数です（処理中を含み、失敗・結果不明は含みません）。自動取り込みの停止中は、自動解析も進みません。
  </p>
  <p class="muted">
    確認待ちは抽出された情報の件数、登録済みは紐付いた契約数です。同じ契約から複数のメールが届くため、メール数とは一致しません。
  </p>
  {#if error}<p class="notice" role="alert">{error}</p>{/if}
  <ActionGroup class={['actions', 'part-ImportExecutionDetail-actions']}>
    <ActionLink
      wrap
      variant="primary"
      class={['button primary']}
      href={`/analysis?tab=attention&account_id=${account}`}
      >このメール接続の候補を確認</ActionLink
    >{#if row.failed_count}<Button
        wrap
        disabled={stopping}
        onclick={() => onRetry(row)}>この履歴の失敗分を再試行</Button
      >{/if}{#if row.active_run_id}<Button
        wrap
        disabled={stopping}
        onclick={() => onStop(row)}
        >{stopping ? '停止中…' : '実行中の処理を停止'}</Button
      >{/if}
  </ActionGroup>
  {#if row.active_run_id}<p class="muted">
      停止すると、まだ送信していない処理を止めます。送信済みの処理には料金が発生する場合があります。継続する自動取り込みの停止は「取り込み設定」から行えます。
    </p>{/if}
  {#if row.unknown_count}<p class="muted">
      結果不明のメールは一括再試行に含めません。前回の処理で料金が発生している可能性があります。
    </p>{/if}
</div>

<style>
  .detail-progress {
    margin-top: 16px;
  }
  .detail-progress p {
    font-size: 11px;
    margin-top: 8px;
  }
  .execution-detail dl {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 18px;
    margin: 24px 0;
  }
  .execution-detail dt {
    font-size: 12px;
    color: var(--color-muted);
  }
  .execution-detail dd {
    margin: 6px 0 0;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .execution-detail p {
    overflow-wrap: anywhere;
  }
</style>
