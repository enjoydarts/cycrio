<script lang="ts">
  import type {
    ImportSyncActivity,
    ImportAnalysisActivity,
    ImportAutomationActivity,
  } from './import-executions-api.ts';
  let {
    syncStartedAt,
    analysisStartedAt,
    syncActivity,
    analysisActivity,
    automationActivity,
  }: {
    syncStartedAt?: string | null;
    analysisStartedAt?: string | null;
    syncActivity?: ImportSyncActivity | null;
    analysisActivity?: ImportAnalysisActivity | null;
    automationActivity?: ImportAutomationActivity | null;
  } = $props();
  const date = new Intl.DateTimeFormat('ja-JP', {
    timeZone: 'Asia/Tokyo',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  });
  const activity = $derived([
    {
      label: '最終メール確認（取り込み）',
      at: syncStartedAt,
      detail: syncActivity,
      addedCount: syncActivity?.added_count,
    },
    {
      label: '最終解析',
      at: analysisStartedAt,
      detail: analysisActivity,
      addedCount: undefined,
    },
  ]);
  const states: Record<string, string> = {
    completed: '完了',
    completed_with_failures: '完了（失敗あり）',
    limited: '上限で終了',
    running: '実行中',
    failed: '失敗',
    cancelled: '停止済み',
    unknown: '結果確認中',
    queued: '待機中',
    leased: '待機中',
  };
  const next = $derived(
    automationActivity?.retry_at ?? automationActivity?.next_check_at,
  );
</script>

<section class="last-activity" aria-label="このメール接続の最終実行日時">
  <p>実行開始日時（日本時間）</p>
  <dl>
    {#each activity as item (item.label)}
      <div>
        <dt>{item.label}</dt>
        <dd>
          {#if item.at}<time datetime={item.at}
              >{date.format(new Date(item.at))}</time
            >
          {:else}{item.at === null ? '未実行' : '記録なし'}{/if}
        </dd>
        {#if item.detail}<dd class="result">
            {states[item.detail.state] ??
              '状態不明'}{#if item.addedCount !== undefined}・追加 {item.addedCount.toLocaleString(
                'ja-JP',
              )} 通{/if}
          </dd>{/if}
      </div>
    {/each}
    {#if automationActivity !== undefined}
      <div>
        <dt>次回確認（日本時間）</dt>
        <dd>
          {#if automationActivity === null}自動確認は未設定
          {:else if automationActivity.state === 'stopped'}自動確認は停止中
          {:else if automationActivity.state !== 'enabled'}設定の確認が必要
          {:else if automationActivity.busy}現在の処理の完了後に確認
          {:else if next}{automationActivity.retry_at
              ? '再試行予定：'
              : '確認予定：'}<time datetime={next}
              >{date.format(new Date(next))}</time
            > 以降
          {:else}予定は未確定{/if}
        </dd>
      </div>
    {/if}
  </dl>
  {#if syncActivity?.state === 'completed' && syncActivity.added_count === 0}<p
      class="explanation"
    >
      今回の確認で新しく追加されたメールはありません。
    </p>{/if}
  <p class="explanation">
    メール確認は追加 0
    通でも日時が更新されます。解析は未解析メールや再解析などの対象があるときに実行されるため、最終確認と最終解析の日時は異なることがあります。
  </p>
  {#if automationActivity?.state === 'enabled'}<p class="explanation">
      次回確認は目安です。実行中の処理や再試行により遅れることがあります。
    </p>{/if}
</section>

<style>
  .last-activity {
    padding: 4px 0 20px;
  }
  p {
    margin: 0 0 10px;
    font-size: 12px;
    color: var(--color-muted);
  }
  dl {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
    gap: 12px 24px;
    margin: 0;
  }
  dl > div {
    min-width: 0;
  }
  dt {
    font-size: 12px;
    color: var(--color-muted);
  }
  dd {
    margin: 5px 0 0;
    font-size: 14px;
    font-weight: 500;
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
  }
  .result {
    font-size: 12px;
    font-weight: 400;
  }
  .explanation {
    margin: 12px 0 0;
    line-height: 1.7;
  }
</style>
