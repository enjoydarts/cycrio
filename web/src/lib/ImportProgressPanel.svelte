<script lang="ts">
  import type { MailAutomationState } from './mail-automation-api.ts';
  import { inclusiveEnd } from './mail-automation-api.ts';
  import { processingDate, processingReasons } from './mail-processing-text.ts';
  let {
    entries,
  }: { entries: { label: string; state: MailAutomationState }[] } = $props();
  const reason = (code: string) =>
    (processingReasons as Record<string, string>)[code] ??
    '接続や送信条件を確認してください。';
  const status = (s: MailAutomationState) =>
    s.state === 'stopped'
      ? '停止中'
      : s.state === 'needs_confirmation'
        ? '条件の再確認が必要'
        : s.retry_at
          ? '接続の再試行待ち'
          : !s.progress.bootstrap_complete
            ? '取り込み準備中'
            : !s.progress.historical_complete
              ? 'メール探索中'
              : s.progress.pending_count
                ? '解析中'
                : '新着メールを確認中';
  const runNames: Record<string, string> = {
    queued: '待機中',
    leased: '開始待ち',
    running: '探索中',
    completed: '完了',
    limited: '続きの処理へ',
    failed: '失敗',
    cancelled: '取消済み',
  };
</script>

<section aria-label="取り込みの進行状況">
  <h2>取り込みの進行状況</h2>
  <p class="secondary">
    5秒ごとに更新します。指定期間を分割して処理するため、100件は1回の処理単位で、期間全体の件数制限ではありません。
  </p>
  <ul class="compact-list">
    {#each entries as { label, state: s } (s.account_id)}<li>
        <div class="heading">
          <h3>{label}</h3>
          <span class="badge">{status(s)}</span>
        </div>
        {#if s.scope}<p class="secondary">
            対象期間：{s.scope.since} 〜 {inclusiveEnd(
              s.scope.before,
            )}（日本時間・終了日を含む）
          </p>{/if}
        {#if s.reason && s.state !== 'enabled'}<p class="warning" role="status">
            {reason(s.reason)}
          </p>{/if}
        {#if s.retry_at}<p>
            次の接続確認：{processingDate(s.retry_at)}（日本時間）
          </p>{/if}
        <dl>
          {#each [['発見', s.progress.discovered_count], ['解析待ち', s.progress.pending_count], ['解析済み', s.progress.completed_count], ['解析失敗', s.progress.failed_count], ['結果不明', s.progress.unknown_count]] as [name, count]}<div
            >
              <dt>{name}</dt>
              <dd>{count} 件</dd>
            </div>{/each}
        </dl>
        <p class="secondary">
          {s.progress.historical_complete
            ? '指定期間の探索は完了しました。'
            : '指定期間の探索は未完了です。総件数は探索が終わるまで確定しません。'}
        </p>
        <a href={'/import?account_id=' + s.account_id}
          >取り込みの設定・再開を確認</a
        >
        {#if s.recent_sync_runs.length}<details>
            <summary>探索履歴</summary>
            <ul>
              {#each s.recent_sync_runs as run (run.id)}<li>
                  <p>
                    {processingDate(run.created_at)}（日本時間） · {runNames[
                      run.state
                    ] ?? '状態を確認'}
                  </p>
                  <p>
                    確認 {run.examined_count} 件 · 発見 {run.added_count} 件
                  </p>
                  {#if run.error_code}<p class="warning">
                      {reason(run.error_code)}
                    </p>{/if}
                </li>{/each}
            </ul>
          </details>{/if}
      </li>{:else}<li class="empty">
        開始済みの自動取り込みはありません。
      </li>{/each}
  </ul>
</section>

<style>
  h2 {
    font-size: 1rem;
    margin: 0 0 12px;
  }
  h3 {
    font-size: 0.95rem;
    margin: 0;
  }
  .heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }
  dl {
    display: flex;
    gap: 20px;
    flex-wrap: wrap;
    margin: 16px 0;
  }
  dt {
    font-size: 12px;
    color: var(--color-muted);
  }
  dd {
    margin: 5px 0 0;
    font-weight: 600;
  }
  p {
    font-size: 12px;
    margin: 10px 0;
    overflow-wrap: anywhere;
  }
  .warning {
    color: var(--color-danger);
  }
  a,
  summary {
    font-size: 12px;
    min-height: 32px;
    display: inline-flex;
    align-items: center;
  }
  details {
    margin-top: 10px;
  }
  details ul {
    padding-left: 18px;
  }
</style>
