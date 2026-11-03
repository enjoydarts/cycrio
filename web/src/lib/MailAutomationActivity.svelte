<script lang="ts">
  import type { MailAutomationState } from './mail-automation-api.ts';
  import {
    automationLastStart,
    automationNextCheck,
  } from './mail-automation-activity.ts';
  import { processingDate } from './mail-processing-text.ts';
  let { state }: { state: MailAutomationState } = $props();
  const started = $derived(automationLastStart(state));
  const next = $derived(automationNextCheck(state));
</script>

<dl class="automation-activity" aria-label="自動取り込みの実行日時（日本時間）">
  <div>
    <dt>最終実行開始</dt>
    <dd>
      {#if started}<time datetime={started}>{processingDate(started)}</time
        >{:else}開始記録なし{/if}
    </dd>
  </div>
  <div>
    <dt>最終確認成功</dt>
    <dd>
      {#if state.last_success_at}<time datetime={state.last_success_at}
          >{processingDate(state.last_success_at)}</time
        >{:else}成功記録なし{/if}
    </dd>
  </div>
  <div>
    <dt>次の確認予定</dt>
    <dd class:overdue={next.overdue}>
      {#if next.at}<time datetime={next.at}>{processingDate(next.at)}</time
        >{/if}{#if next.label}<span>{next.label}</span>{/if}
    </dd>
  </div>
</dl>

<style>
  .automation-activity {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 180px), 1fr));
    gap: 12px 20px;
    margin: 16px 0;
    font-size: 12px;
  }
  .automation-activity dt {
    color: var(--color-muted);
    font-weight: 400;
    margin: 0 0 5px;
  }
  .automation-activity dd {
    margin: 0;
    font-weight: 500;
    overflow-wrap: anywhere;
  }
  .automation-activity dd span {
    display: block;
  }
  .automation-activity .overdue {
    color: var(--color-danger);
  }
</style>
