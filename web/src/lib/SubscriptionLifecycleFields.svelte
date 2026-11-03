<script lang="ts">
  import type { SubscriptionValues } from './subscriptions-api.ts';
  let {
    values,
    disabled = false,
    onchange,
  }: {
    values: SubscriptionValues;
    disabled?: boolean;
    onchange: (patch: Partial<SubscriptionValues>) => void;
  } = $props();
</script>

<label
  >無料期間終了日（任意）<input
    type="date"
    value={values.trial_end_date ?? ''}
    {disabled}
    oninput={(e) => onchange({ trial_end_date: e.currentTarget.value || null })}
  /><small class="muted"
    >明示された終了日です。有料請求日や料金は自動で補完しません。</small
  ></label
>
<label
  >自動更新（任意）<select
    value={values.auto_renew === true
      ? 'on'
      : values.auto_renew === false
        ? 'off'
        : ''}
    {disabled}
    onchange={(e) =>
      onchange({
        auto_renew:
          e.currentTarget.value === 'on'
            ? true
            : e.currentTarget.value === 'off'
              ? false
              : null,
      })}
    ><option value="">不明</option><option value="on">有効</option><option
      value="off">停止</option
    ></select
  ><small class="muted"
    >停止だけでは利用終了にしません。最終の請求がある場合もあります。</small
  ></label
>
