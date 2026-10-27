<script lang="ts">
  import type { AnalysisResult } from './analysis-api.ts';
  let { result }: { result: AnalysisResult } = $props();
  const attention = $derived(
    result.state === 'failed' ||
      result.state === 'unknown' ||
      result.automation?.some((a) =>
        ['needs_review', 'failed'].includes(a.state),
      ) ||
      result.billing_automation?.some((a) =>
        ['needs_review', 'failed'].includes(a.state),
      ),
  );
  const automatic = $derived(
    result.automation?.some(
      (a) =>
        a.state === 'registered' && ['created', 'attached'].includes(a.reason),
    ),
  );
  const recorded = $derived(
    result.billing_automation?.some((a) => a.state === 'recorded'),
  );
  const targets = $derived([
    ...new Set(result.registrations.map((r) => r.subscription_id)),
  ]);
</script>

{#if result.state === 'failed'}<span class="badge">解析失敗・要対応</span>
{:else if result.state === 'unknown'}<span class="badge">結果不明・要対応</span>
{:else if !result.events.length}<span class="secondary"
    >解析は完了しました。登録対象の情報はありません。</span
  >
{:else}<span class="badge"
    >{attention
      ? '要対応'
      : targets.length
        ? automatic
          ? '自動登録済み'
          : '登録済み'
        : result.automation_state === 'pending'
          ? '自動登録待ち'
          : '登録先の確認が必要です'}</span
  >
  {#if recorded}<span class="secondary">支払・請求履歴に記録済み</span>{/if}
  {#each targets as id}<a class="result-link" href={`/subscriptions/${id}`}
      >サブスクを見る</a
    >
    <a class="result-link" href={`/subscriptions/${id}?tab=history`}
      >支払・請求履歴を見る</a
    >{/each}
{/if}

<style>
  .result-link {
    display: block;
    margin-top: 0.35rem;
  }
</style>
