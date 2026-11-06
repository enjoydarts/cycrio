<script lang="ts">
  import type { ChangeKey, ChangePreview } from './subscription-changes-api.ts';
  import {
    selectableChange,
    validChangeSelection,
  } from './subscription-change-controller.ts';
  import {
    changeLabels,
    changeReason,
    changeValue,
  } from './subscription-change-text.ts';
  import LibraryPanel from './LibraryPanel.svelte';
  import DataTable from './DataTable.svelte';
  import EventFields from './EventFields.svelte';
  import MailReceivedDate from './MailReceivedDate.svelte';
  let {
    preview: p,
    selected = [],
    confirmed = false,
    locked = false,
    conflict = false,
    confirmationDisabled = false,
    deferred = false,
    onselect,
    onconfirm,
  }: {
    preview: ChangePreview;
    selected?: ChangeKey[];
    confirmed?: boolean;
    locked?: boolean;
    conflict?: boolean;
    confirmationDisabled?: boolean;
    deferred?: boolean;
    onselect: (key: ChangeKey, value: boolean) => void;
    onconfirm: (value: boolean) => void;
  } = $props();
  const valid = $derived(validChangeSelection(p, selected));
</script>

<LibraryPanel
  ><DataTable responsive="cards">
    <thead><tr><th>反映する項目</th><th>現在</th><th>変更候補</th></tr></thead>
    <tbody
      >{#each p.fields as field}<tr>
          <td data-primary
            ><label class="field-choice"
              ><input
                type="checkbox"
                checked={selected.includes(field.key)}
                disabled={locked || conflict || !selectableChange(field)}
                onchange={(e) => onselect(field.key, e.currentTarget.checked)}
              /><span>{changeLabels[field.key]}</span></label
            ></td
          >
          <td data-label="現在">{changeValue(field.key, field.current)}</td>
          <td data-label="変更候補"
            >{changeValue(field.key, field.proposed)}{#if field.reason}<p
                class="muted"
              >
                {changeReason(field.reason)}
              </p>{/if}</td
          >
        </tr>{:else}<tr
          ><td colspan="3"
            >反映できる差分はありません。元情報を確認してください。</td
          ></tr
        >{/each}</tbody
    >
  </DataTable></LibraryPanel
>
{#each p.warnings as warning}<p class="notice" role="status">
    {warning.field ? `${changeLabels[warning.field]}：` : ''}{changeReason(
      warning.code,
    )}
  </p>{/each}
{#if selected.includes('next_billing_date')}<p class="notice">
    次回請求日を反映すると、日付の設定は手動指定になります。
  </p>{/if}
{#if selected.includes('interval') && !selected.includes('next_billing_date') && !p.warnings.some((warning) => warning.code === 'derived_billing_date_requires_lifecycle_review') && p.subscription.next_billing_mode === 'automatic'}
  {#if deferred}<p class="notice">
      次回請求日の推定は、予約の反映時に再計算します。
    </p>
  {:else}<p class="notice">
      変更後の次回請求日（推定）：{p.derived_next_billing_date ??
        '条件不足のため不明'}
    </p>{/if}
{/if}
<details>
  <summary>元の抽出情報を確認</summary>
  {#each [p.source, ...p.additional_evidence] as evidence}<section
      class="evidence"
    >
      <p>
        <MailReceivedDate receivedAt={evidence.received_at} /> · イベント {evidence.event_index +
          1}
      </p>
      <EventFields event={evidence.original_event} />
    </section>{/each}
</details>
{#if selected.length && !valid}<p class="notice" role="alert">
    料金と通貨を組み合わせて選択してください。片方だけが不明になる変更は保存できません。
  </p>{/if}
<label class="field-choice confirmation"
  ><input
    type="checkbox"
    checked={confirmed}
    disabled={locked || conflict || !valid || confirmationDisabled}
    onchange={(e) => onconfirm(e.currentTarget.checked)}
  /><span>選択した変更と元の抽出情報を確認しました</span></label
>

<style>
  p,
  td,
  .evidence {
    overflow-wrap: anywhere;
  }
  .field-choice {
    display: flex;
    flex-direction: row;
    gap: 10px;
    align-items: flex-start;
    min-width: 0;
  }
  .field-choice input {
    flex: 0 0 20px;
    width: 20px;
    height: 20px;
    min-height: 20px;
    margin: 2px 0 0;
    padding: 0;
  }
  .confirmation {
    margin: 20px 0;
  }
  .evidence {
    margin: 12px 0;
  }
  summary {
    cursor: pointer;
    padding-block: 12px;
  }
</style>
