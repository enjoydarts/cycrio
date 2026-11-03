<script lang="ts">
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import {
    createSubscriptionsAPI,
    type Subscription,
  } from './subscriptions-api.ts';
  import {
    createSubscriptionChangesAPI,
    type ChangeSource,
    type ChangeResult,
  } from './subscription-changes-api.ts';
  import {
    createSubscriptionChange,
    validChangeSelection,
    type ChangeView,
  } from './subscription-change-controller.ts';
  import { subscriptionTitle } from './subscription-title.ts';
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
  }: {
    subscription: Subscription;
    source: ChangeSource;
    onSaved: (result: ChangeResult) => void;
    onBusyChange?: (busy: boolean) => void;
    onFrozen?: (frozen: boolean) => void;
    onBackToResults: () => void;
  } = $props();
  const session = getAppSession();
  let view = $state<ChangeView>({
    preview: null,
    selected: [],
    confirmed: false,
    busy: false,
    uncertain: false,
    conflict: false,
    denied: false,
    error: null,
  });
  let controller: ReturnType<typeof createSubscriptionChange> | undefined;
  const locked = $derived(view.busy || view.uncertain);
  const valid = $derived(
    !!view.preview && validChangeSelection(view.preview, view.selected),
  );
  onMount(() => {
    controller = createSubscriptionChange(
      {
        ...createSubscriptionChangesAPI(PUBLIC_API_BASE_URL),
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

<section class="change-review" aria-label="契約の変更を確認">
  <h3>契約の変更を確認</h3>
  <p>
    「{subscriptionTitle(
      subscription,
    )}」の現在の内容と、メールから抽出された通常継続情報を比較します。反映する項目は自分で選択してください。
  </p>
  {#if view.busy && !view.preview}<p role="status">
      変更候補を読み込んでいます…
    </p>{/if}
  {#if view.preview}
    <SubscriptionChangeComparison
      preview={view.preview}
      selected={view.selected}
      confirmed={view.confirmed}
      {locked}
      conflict={view.conflict}
      onselect={(key, value) => controller?.select(key, value)}
      onconfirm={(value) => controller?.confirm(value)}
    />
  {/if}
  {#if view.error}<p role="alert" class="notice">
      {processingErrors[view.error]}
    </p>{/if}
  {#if view.conflict}<p class="notice">
      契約または抽出結果が更新されました。最新の差分を読み直し、反映する項目を選び直してください。抽出結果が更新された場合は一覧に戻って確認してください。
    </p>{/if}
  {#if view.uncertain}<p role="status" class="notice">
      変更の受付状況が不明です。入力を保持しています。同じ要求で再確認してください。
    </p>{/if}
  <ActionGroup>
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
        onclick={() => controller?.retry()}>同じ要求で変更を確認</Button
      >
    {:else if view.conflict || (!view.preview && view.error)}<Button
        type="button"
        wrap
        disabled={locked || view.denied}
        onclick={() => controller?.preview()}>最新の差分を確認し直す</Button
      >
    {:else}<Button
        type="button"
        wrap
        variant="primary"
        disabled={locked || !valid || !view.confirmed || view.denied}
        onclick={() => controller?.apply()}
        >{view.busy ? '反映中…' : '選択した変更を反映'}</Button
      >{/if}
  </ActionGroup>
</section>

<style>
  .change-review {
    min-width: 0;
    margin-top: 20px;
  }
  p {
    overflow-wrap: anywhere;
  }
</style>
