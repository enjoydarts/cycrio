<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api.ts';
  import {
    parseNotificationPreferences,
    type NotificationsAPI,
  } from './notifications-api.ts';
  import {
    createNotificationSettings,
    type NotificationSettingsView,
  } from './notification-settings-controller.ts';
  import NotificationPreferenceFields from './NotificationPreferenceFields.svelte';
  import Button from './Button.svelte';
  import { processingErrors } from './mail-processing-text.ts';
  let {
    api,
    token,
    onDenied,
    onFrozen,
  }: {
    api: NotificationsAPI;
    token: TokenProvider;
    onDenied: () => void;
    onFrozen: (frozen: boolean) => void;
  } = $props();
  let view = $state<NotificationSettingsView>({
    page: null,
    values: null,
    loading: false,
    busy: false,
    confirmed: false,
    uncertain: false,
    conflict: false,
    saved: false,
    error: null,
  });
  let controller: ReturnType<typeof createNotificationSettings> | undefined;
  const locked = $derived(view.busy || view.uncertain);
  const valid = $derived.by(() => {
    try {
      if (!view.values) return false;
      parseNotificationPreferences(view.values);
      return true;
    } catch {
      return false;
    }
  });
  onMount(() => {
    controller = createNotificationSettings(
      api,
      token,
      (next) => {
        view = next;
        onFrozen(next.busy || next.uncertain);
      },
      onDenied,
    );
    void controller.load();
    return () => {
      controller?.dispose();
      onFrozen(false);
    };
  });
</script>

<section class="daily" aria-label="本人の通知設定">
  <h3>通知設定</h3>
  <p>
    保存すると本人の通知に反映します。メール通知は、連携済みの本人の検証済みメール宛てです。
  </p>
  {#if view.loading}<p role="status">通知設定を読み込んでいます…</p>{/if}
  {#if view.values && view.page}<form
      onsubmit={(event) => {
        event.preventDefault();
        void controller?.save();
      }}
    >
      <NotificationPreferenceFields
        values={view.values}
        email={view.page.email}
        disabled={locked || view.conflict}
        onchange={(patch) => controller?.edit(patch)}
      />
      <label class="choice"
        ><input
          type="checkbox"
          checked={view.confirmed}
          disabled={locked || view.conflict || !valid}
          onchange={(event) => controller?.confirm(event.currentTarget.checked)}
        /><span>通知の種類とタイミングを確認しました</span></label
      >
      {#if !view.uncertain && !view.conflict}<Button
          type="submit"
          wrap
          variant="primary"
          disabled={locked || !valid || !view.confirmed}>通知設定を保存</Button
        >{/if}
    </form>{/if}
  {#if view.saved}<p role="status" class="notice">
      通知設定を保存しました。
    </p>{/if}
  {#if view.error}<p role="alert" class="notice">
      {processingErrors[view.error]}
    </p>{/if}
  {#if view.uncertain}<p role="status" class="notice">
      保存の受付状況が不明です。入力を保持したまま、現在の設定を読み取り確認します。
    </p>
    <Button
      type="button"
      wrap
      disabled={view.busy}
      onclick={() => controller?.reconcile()}>保存状況を再確認</Button
    >
  {:else if view.conflict}<p class="notice">
      設定が更新されています。現在の設定を読み直し、選び直して確認してください。
    </p>
    <Button type="button" wrap onclick={() => controller?.load()}
      >最新の通知設定を読み込む</Button
    >
  {:else if view.error && !view.page}<Button
      type="button"
      wrap
      onclick={() => controller?.load()}>通知設定を再取得</Button
    >{/if}
</section>

<style>
  .choice {
    display: flex;
    flex-direction: row;
    gap: 10px;
    align-items: flex-start;
    margin-block: 16px;
  }
  input[type='checkbox'] {
    flex: 0 0 20px;
    width: 20px;
    height: 20px;
    min-height: 20px;
    margin: 2px 0 0;
    padding: 0;
  }
  p {
    overflow-wrap: anywhere;
  }
</style>
