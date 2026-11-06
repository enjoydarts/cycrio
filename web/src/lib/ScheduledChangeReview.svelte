<script lang="ts">
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import {
    createSubscriptionsAPI,
    type Subscription,
  } from './subscriptions-api.ts';
  import type { ChangeSource } from './subscription-changes-api.ts';
  import {
    createScheduledChangesAPI,
    type ScheduledChangeResult,
  } from './subscription-scheduled-changes-api.ts';
  import {
    createScheduledChange,
    type ScheduledChangeView,
  } from './scheduled-change-controller.ts';
  import { validChangeSelection } from './subscription-change-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import SubscriptionChangeComparison from './SubscriptionChangeComparison.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  let {
    subscription,
    source,
    onSaved,
    onBusyChange = () => {},
    onFrozen = () => {},
    onBackToResults,
    onBack,
  }: {
    subscription: Subscription;
    source: ChangeSource;
    onSaved: (result: ScheduledChangeResult) => void;
    onBusyChange?: (busy: boolean) => void;
    onFrozen?: (busy: boolean) => void;
    onBackToResults: () => void;
    onBack: () => void;
  } = $props();
  const session = getAppSession();
  let view = $state<ScheduledChangeView>({
    preview: null,
    selected: [],
    confirmed: false,
    dateConfirmed: false,
    busy: false,
    uncertain: false,
    conflict: false,
    denied: false,
    error: null,
  });
  let controller: ReturnType<typeof createScheduledChange> | undefined;
  const locked = $derived(view.busy || view.uncertain);
  const valid = $derived(
    !!view.preview && validChangeSelection(view.preview, view.selected),
  );
  onMount(() => {
    controller = createScheduledChange(
      {
        ...createScheduledChangesAPI(PUBLIC_API_BASE_URL),
        get: createSubscriptionsAPI(PUBLIC_API_BASE_URL).get,
      },
      session.token,
      subscription,
      source,
      (next) => {
        view = next;
        onBusyChange(next.busy || next.uncertain);
        onFrozen(next.busy || next.uncertain);
      },
      onSaved,
      session.onDenied,
      () => crypto.randomUUID(),
    );
    void controller.preview();
    return () => {
      controller?.dispose();
      onBusyChange(false);
      onFrozen(false);
    };
  });
</script>

<section aria-label="効力日の契約変更を予約">
  <h3>効力日の契約変更を予約</h3>
  <p>
    現在の契約内容は変更せず、確認した項目を効力日（日本時間）以降に反映します。予約後に契約を編集した場合は、反映前の確認時に変更を検出し、予約を要再確認で停止します。
  </p>
  {#if view.busy && !view.preview}<p role="status">
      予約する変更候補を読み込んでいます…
    </p>{/if}
  {#if view.preview}
    <p>効力日：{view.preview.scheduled_for}（日本時間）</p>
    <label class="date-consent"
      ><input
        type="checkbox"
        checked={view.dateConfirmed}
        disabled={locked || view.conflict || view.denied}
        onchange={(event) =>
          controller?.confirmDate(event.currentTarget.checked)}
      /><span
        >効力日 {view.preview
          .scheduled_for}（日本時間）に予約することを確認しました</span
      ></label
    >
    <SubscriptionChangeComparison
      deferred
      preview={view.preview}
      selected={view.selected}
      confirmed={view.confirmed}
      confirmationDisabled={!view.dateConfirmed}
      {locked}
      conflict={view.conflict}
      onselect={(key, checked) => controller?.select(key, checked)}
      onconfirm={(checked) => controller?.confirm(checked)}
    />
  {/if}
  {#if view.error}<p role="alert" class="notice">
      {processingErrors[view.error]}
    </p>{/if}
  {#if view.conflict}<p class="notice">
      契約または抽出結果が更新されました。最新の差分と効力日を確認し直してください。
    </p>{/if}
  {#if view.uncertain}<p role="status" class="notice">
      予約の受付状況が不明です。入力と効力日を保持しています。同じ要求で再確認してください。
    </p>{/if}
  <ActionGroup>
    <Button type="button" wrap disabled={locked} onclick={onBack}
      >通常の変更確認に戻る</Button
    >
    {#if view.conflict}<Button
        type="button"
        wrap
        disabled={locked}
        onclick={onBackToResults}>一覧に戻って最新の抽出結果を確認</Button
      >{/if}
    {#if view.uncertain}<Button
        type="button"
        wrap
        disabled={view.busy}
        onclick={() => controller?.retry()}>同じ要求で予約を確認</Button
      >
    {:else if view.conflict || (!view.preview && view.error)}<Button
        type="button"
        wrap
        disabled={locked || view.denied}
        onclick={() => controller?.preview()}>予約の差分を確認し直す</Button
      >
    {:else}<Button
        type="button"
        wrap
        variant="primary"
        disabled={locked ||
          !valid ||
          !view.confirmed ||
          !view.dateConfirmed ||
          view.denied}
        onclick={() => controller?.apply()}
        >{view.busy ? '予約中…' : '選択した変更を予約'}</Button
      >{/if}
  </ActionGroup>
</section>

<style>
  section {
    min-width: 0;
    margin-top: 20px;
  }
  p,
  span {
    overflow-wrap: anywhere;
  }
  .date-consent {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    margin: 20px 0;
  }
  .date-consent input {
    flex: 0 0 20px;
    width: 20px;
    height: 20px;
    min-height: 20px;
    margin: 2px 0 0;
    padding: 0;
  }
</style>
