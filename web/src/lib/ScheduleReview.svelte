<script lang="ts">
  import type { SchedulePreview, ScheduleView } from './schedule-api.ts';
  import { cycle } from './daily-text.ts';
  let {
    baseline,
    preview,
  }: { baseline: ScheduleView; preview: SchedulePreview } = $props();
</script>

<section class="schedule-review" aria-label="次回予定の計算結果">
  <h3>指定条件から算出</h3>
  <p>計算基準日: {preview.as_of}（日本時間 · Asia/Tokyo）</p>
  <p class="muted">
    基準請求日 {preview.rule.anchor_date} · {preview.rule.month_end
      ? '月末固定'
      : '日付指定'}
  </p>
  {#if preview.candidate_date}<dl class="schedule-comparison">
      <div>
        <dt>変更前</dt>
        <dd>
          {cycle(
            baseline.subscription.interval_unit,
            baseline.subscription.interval_count,
          )}
        </dd>
        <dd>次回請求日 {baseline.subscription.next_billing_date ?? '不明'}</dd>
      </div>
      <div>
        <dt>変更後</dt>
        <dd>
          {cycle(preview.rule.interval_unit, preview.rule.interval_count)}
        </dd>
        <dd>
          次回請求日 <strong>{preview.candidate_date}</strong
          >{preview.candidate_date === preview.as_of ? '（本日）' : ''}
        </dd>
      </div>
    </dl>
    <p class="muted">
      候補は未保存です。周期・基準日・日付の指定方法・候補日を本人が確認してから保存してください。
    </p>
  {:else if preview.reason === 'ended'}<p
      class="schedule-warning"
      role="status"
    >
      終了済みの契約には次回予定を保存できません。
    </p>
  {:else}<p class="schedule-warning" role="status">
      指定条件の次回予定が扱える日付の範囲（0001〜9999年）を超えています。条件を確認してください。
    </p>{/if}
  {#if preview.overdue}<p class="schedule-warning">
      登録されている次回請求日が計算基準日より前です。支払済み・未払い・解約済みを意味しません。
    </p>{/if}
</section>
