<script lang="ts">
  import type { ProcessingEvent } from './mail-processing-api.ts';
  import { eventNames, cycle, money } from './daily-text.ts';
  let { event }: { event: ProcessingEvent } = $props();
</script>

<dl class="field-grid">
  <div>
    <dt>サービス</dt>
    <dd>{event.service ?? 'サービス名未確認'}</dd>
  </div>
  <div>
    <dt>プラン</dt>
    <dd>{event.plan ?? '不明'}</dd>
  </div>
  <div>
    <dt>検出したイベント</dt>
    <dd>{eventNames[event.event_type]}</dd>
  </div>
  <div>
    <dt>取引金額</dt>
    <dd class="money">{money(event.amount, event.currency)}</dd>
  </div>
  <div>
    <dt>通常継続料金</dt>
    <dd class="money">
      {money(event.recurring_amount ?? null, event.recurring_currency ?? null)}
    </dd>
  </div>
  <div>
    <dt>契約種別</dt>
    <dd>
      {event.contract_kind === 'fixed_term'
        ? '期間限定（自動更新なし）'
        : event.contract_kind === 'recurring'
          ? '継続契約'
          : '不明'}
    </dd>
  </div>
  <div>
    <dt>効力日</dt>
    <dd>{event.effective_date ?? '不明'}</dd>
  </div>
  <div>
    <dt>利用終了日</dt>
    <dd>{event.access_end_date ?? '不明'}</dd>
  </div>
  <div>
    <dt>請求日</dt>
    <dd>{event.billed_at ?? '不明'}</dd>
  </div>
  <div>
    <dt>次回請求日</dt>
    <dd>{event.next_billing_at ?? '不明'}</dd>
  </div>
  <div>
    <dt>利用期間の開始</dt>
    <dd>{event.period_start ?? '不明'}</dd>
  </div>
  <div>
    <dt>利用期間の終了</dt>
    <dd>{event.period_end ?? '不明'}</dd>
  </div>
  <div>
    <dt>周期</dt>
    <dd>{cycle(event.interval_unit, event.interval_count)}</dd>
  </div>
</dl>
