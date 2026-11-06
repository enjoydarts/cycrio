<script lang="ts">
  import type { ScheduledChange } from './subscription-scheduled-changes-api.ts';
  import { changeLabels, changeValue } from './subscription-change-text.ts';
  import DataTable from './DataTable.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import EventFields from './EventFields.svelte';
  import MailReceivedDate from './MailReceivedDate.svelte';
  let {
    schedule,
    locked = false,
    onCancel,
  }: {
    schedule: ScheduledChange;
    locked?: boolean;
    onCancel: () => void;
  } = $props();
  const states = {
    pending: '予約中',
    applied: '反映済み',
    cancelled: '取消済み',
    needs_review: '要再確認',
  };
  const reasons = {
    '': '',
    subscription_changed: '契約が更新されたため、予約を停止しました。',
    subscription_deleted: '契約が削除されたため、予約を停止しました。',
    source_changed: '元の抽出情報が更新されたため、予約を停止しました。',
    algorithm_changed: '変更の判定方法が更新されたため、予約を停止しました。',
    request_conflict: '承認要求が競合したため、予約を停止しました。',
    invalid_values: '予約内容を安全に反映できないため、予約を停止しました。',
  };
</script>

<details>
  <summary
    >{states[schedule.state]} · {schedule.effective_on}（日本時間） · {schedule.selected_fields
      .map((key) => changeLabels[key])
      .join('・')}</summary
  >
  {#if schedule.state === 'needs_review'}<p class="notice">
      {reasons[schedule.reason]} 予約を取り消してから候補に戻り、最新の内容を確認して承認し直してください。
    </p>{/if}
  <DataTable responsive="cards">
    <thead
      ><tr><th>変更項目</th><th>予約時の値</th><th>予約した値</th></tr></thead
    >
    <tbody
      >{#each schedule.selected_fields as key}{@const field =
          schedule.preview.fields.find((f) => f.key === key)!}<tr
          ><td data-primary>{changeLabels[key]}</td><td data-label="予約時の値"
            >{changeValue(key, field.current)}</td
          ><td data-label="予約した値">{changeValue(key, field.proposed)}</td
          ></tr
        >{/each}</tbody
    >
  </DataTable>
  {#each [schedule.preview.source, ...schedule.preview.additional_evidence] as evidence}<section
      class="evidence"
    >
      <h3>元の抽出情報 · イベント {evidence.event_index + 1}</h3>
      <p><MailReceivedDate receivedAt={evidence.received_at} /></p>
      <EventFields event={evidence.original_event} />
    </section>{/each}
  {#if schedule.state === 'pending' || schedule.state === 'needs_review'}<ActionGroup
      ><Button type="button" wrap disabled={locked} onclick={onCancel}
        >予約を取り消す</Button
      ></ActionGroup
    >{/if}
</details>

<style>
  summary {
    cursor: pointer;
    padding: 12px 0;
    overflow-wrap: anywhere;
  }
  .evidence {
    margin: 16px 0;
    overflow-wrap: anywhere;
  }
  h3 {
    font-size: 14px;
  }
  p {
    overflow-wrap: anywhere;
  }
</style>
