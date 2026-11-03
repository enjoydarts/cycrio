<script lang="ts">
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { extractedCharge } from './extracted-charge.ts';
  import { money } from './daily-text.ts';
  import type { ProcessingEvent } from './mail-processing-api.ts';
  import type { SubscriptionValues } from './subscriptions-api.ts';
  let {
    event,
    values,
    disabled = false,
    onchange = () => {},
  }: {
    event: ProcessingEvent;
    values: SubscriptionValues;
    disabled?: boolean;
    onchange?: (patch: Partial<SubscriptionValues>) => void;
  } = $props();
  const charge = $derived(extractedCharge(event, values.contract_kind));
  const showTransaction = $derived(
    event.amount !== null &&
      (event.amount !== event.recurring_amount ||
        event.currency !== event.recurring_currency),
  );
</script>

{#if showTransaction}
  <div class="notice">
    <p>
      抽出された取引金額：{money(
        event.amount,
        event.currency,
      )}。今回の請求額であり、通常料金と異なる場合があります。
    </p>
    {#if charge}
      <p class="muted">
        通常の継続料金として使える金額か確認してから反映してください。
      </p>
      <ActionGroup>
        <Button
          type="button"
          wrap
          {disabled}
          onclick={() => {
            if (charge) onchange(charge);
          }}>抽出された請求額を使う</Button
        >
      </ActionGroup>
    {/if}
  </div>
{/if}
{#if values.contract_kind !== 'fixed_term' && values.amount !== null && values.currency === null}
  <p class="notice">金額は入力済みです。通貨を選択してください。</p>
{/if}
