<script lang="ts">
  import type { SubscriptionValues } from './subscriptions-api.ts';
  import { processingDate } from './mail-processing-text.ts';
  let {
    values,
    receivedAt = null,
    disabled = false,
    onchange = () => {},
  }: {
    values: SubscriptionValues;
    receivedAt?: string | null;
    disabled?: boolean;
    onchange?: (patch: Partial<SubscriptionValues>) => void;
  } = $props();
</script>

<label
  >次回請求日の設定<select
    value={values.next_billing_mode ?? 'manual'}
    {disabled}
    onchange={(e) =>
      onchange({
        next_billing_mode: e.currentTarget.value as 'automatic' | 'manual',
        next_billing_date: null,
      })}
  >
    <option value="automatic">受信日と周期から自動計算</option><option
      value="manual">日付を手動指定</option
    >
  </select></label
>
{#if values.next_billing_mode === 'automatic'}
  <div>
    <span>次回請求日（推定）</span>
    <p>{values.next_billing_date ?? '保存後に計算・条件不足の場合は不明'}</p>
    <small class="muted"
      >登録時に基準としたメール受信日（日本時間）と請求周期から計算します。{#if receivedAt}受信日時:
        {processingDate(receivedAt)}{/if} 保存後の推定日を確認してください。</small
    >
  </div>
{:else}
  <label
    >次回請求日（任意）<input
      type="date"
      value={values.next_billing_date ?? ''}
      {disabled}
      oninput={(e) =>
        onchange({ next_billing_date: e.currentTarget.value || null })}
    /><small class="muted"
      >保存した手動日は保持します。終了済み・利用終了日より後の日付は、現在の請求予定には表示しません。</small
    ></label
  >
{/if}
