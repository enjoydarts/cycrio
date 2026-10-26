<script lang="ts">
  import type { ScheduleView } from './schedule-api.ts';
  import { cycle } from './daily-text.ts';
  let { view }: { view: ScheduleView | null } = $props();
</script>

{#if view?.settings}{@const settings = view.settings}
  <details class="schedule-conditions">
    <summary>保存済みの計算条件 · 本人確認済み</summary>
    <p>
      前回確認した条件: {cycle(
        settings.rule.interval_unit,
        settings.rule.interval_count,
      )} · 基準日 {settings.rule.anchor_date} · {settings.rule.month_end
        ? '月末固定'
        : '日付指定'}
    </p>
    <p class="muted">
      {settings.confirmed_as_of}（日本時間）を基準に、{settings.confirmed_candidate}
      を確認しました。
    </p>
    {#if settings.history_evidence}{@const evidence = settings.history_evidence}
      <h4>前回確認した履歴の根拠</h4>
      <p class="muted">
        {evidence.as_of}（日本時間）時点 · 対象 {evidence.filter
          .start_date}〜{evidence.as_of} · {evidence.filter
          .currency}。保存時の根拠です。現在の履歴の再推定結果ではありません。
      </p>
      <ul class="schedule-history-samples">
        {#each evidence.samples as sample (sample.id)}<li>
            {sample.occurred_on} · {sample.amount}
            {sample.currency}
          </li>{/each}
      </ul>
    {/if}
  </details>
  {#if settings.stale}<p class="schedule-warning">
      契約の周期や状態が変更されています。保存済みの条件を再確認してください。
    </p>{/if}
  {#if view.next_date_source === 'manual'}<p class="schedule-warning">
      現在の次回請求日は手動指定です。保存済みの計算結果へ自動では戻しません。
    </p>{/if}
{/if}
