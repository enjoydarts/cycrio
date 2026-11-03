<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import {
    createSubscriptionName,
    type NameView,
  } from './subscription-name.ts';
  import {
    createSubscriptionsAPI,
    type Subscription,
  } from './subscriptions-api.ts';
  import type { TokenProvider } from './api.ts';
  import { processingErrors } from './mail-processing-text.ts';
  let {
    subscription,
    base,
    token,
    onclose,
    onSaved,
    onDenied,
    onFrozen,
    onReload,
  }: {
    subscription: Subscription;
    base: string;
    token: TokenProvider;
    onclose: () => void;
    onSaved: (saved: Subscription) => void;
    onDenied: () => void;
    onFrozen: (locked: boolean) => void;
    onReload: () => void;
  } = $props();
  let view = $state<NameView>(
    untrack(() => ({
      name: subscription.display_name ?? '',
      busy: false,
      uncertain: false,
      conflict: false,
      denied: false,
      error: null,
    })),
  );
  const controller = untrack(() =>
    createSubscriptionName(
      createSubscriptionsAPI(base),
      token,
      subscription,
      (next) => {
        view = next;
        onFrozen(next.busy || next.uncertain);
      },
      (saved) => onSaved(saved),
      () => onDenied(),
    ),
  );
  const locked = $derived(view.busy || view.uncertain);
  onDestroy(() => controller.dispose());
</script>

<Modal open title="名前を変更" busy={locked} {onclose}>
  <form
    class="daily name-form"
    onsubmit={(event) => {
      event.preventDefault();
      void controller.save();
    }}
  >
    <label class="name-field"
      >表示名
      <input
        value={view.name}
        oninput={(event) => controller.edit(event.currentTarget.value)}
        disabled={locked || view.conflict || view.denied}
        placeholder={subscription.service}
      />
    </label>
    <p class="muted">
      元のサービス名: {subscription.service}。空欄で保存すると元のサービス名に戻ります。
    </p>
    {#if view.error}<p role="alert" class="notice">
        {processingErrors[view.error]}
      </p>{/if}
    {#if view.uncertain}<p role="status" class="notice">
        保存の受付状況が不明です。保存状況を確認してください。
      </p>{/if}
    <ActionGroup>
      <Button type="button" disabled={locked} onclick={onclose}
        >キャンセル</Button
      >
      {#if view.conflict}
        <Button type="button" wrap onclick={onReload}
          >最新の契約を読み込む</Button
        >
      {:else if view.uncertain}
        <Button
          type="button"
          wrap
          disabled={view.busy}
          onclick={() => controller.reconcile()}>保存状況を確認</Button
        >
      {:else}
        <Button
          type="submit"
          variant="primary"
          disabled={view.busy || view.denied}>保存</Button
        >
      {/if}
    </ActionGroup>
  </form>
</Modal>

<style>
  .name-form {
    display: grid;
    gap: 16px;
  }
  .name-field {
    display: grid;
    gap: 8px;
  }
  input {
    box-sizing: border-box;
    width: 100%;
    min-width: 0;
  }
</style>
