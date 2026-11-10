<script lang="ts">
  import { onMount } from 'svelte';
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import ActionLink from './ActionLink.svelte';
  import PushSettings from './PushSettings.svelte';
  import {
    createPushInvitation,
    type PushInvitationView,
  } from './push-invitation.ts';
  import { notificationKindNames } from './notification-text.ts';
  import type { PushController } from './push-controller.ts';
  import type { PushBrowser } from './push-browser.ts';
  import type { NotificationsAPI } from './notifications-api.ts';
  import type { TokenProvider } from './api.ts';

  let {
    owner,
    token,
    pathname,
    pushController,
    browser,
    notificationsAPI,
    onDenied,
  }: {
    owner: string;
    token: TokenProvider;
    pathname: string;
    pushController: PushController;
    browser: Pick<PushBrowser, 'availability' | 'currentPermission'>;
    notificationsAPI: NotificationsAPI;
    onDenied: () => void;
  } = $props();
  let controller = $state<ReturnType<typeof createPushInvitation>>();
  let view = $state<PushInvitationView>({
    open: false,
    stage: 'offer',
    busy: false,
    error: false,
    deviceReady: false,
    kinds: [],
    push: null,
  });

  onMount(() => {
    try {
      const invitation = createPushInvitation({
        owner,
        token,
        pathname,
        push: pushController,
        browser,
        notifications: notificationsAPI,
        denied: onDenied,
        storage: window.localStorage,
        blocked: () => document.querySelector('dialog[open]') !== null,
      });
      controller = invitation;
      const unsubscribe = invitation.subscribe((next) => {
        view = next;
      });
      void pushController.load();
      return () => {
        unsubscribe();
        invitation.dispose();
      };
    } catch {
      /* 保存できない環境では自動案内しない。 */
    }
  });
  $effect(() => {
    const path = pathname;
    view.push;
    if (controller) void controller.offer(path);
  });
</script>

<svelte:window
  onstorage={(event) => {
    if (event.key?.startsWith('cycrio.push-invitation.'))
      controller?.synchronize();
  }}
/>

<Modal
  open={view.open}
  size="compact"
  title="Push通知を有効にしますか？"
  busy={view.busy}
  onclose={() => controller?.dismiss()}
>
  {#if view.stage === 'offer'}
    <p>アプリを閉じていても、お知らせをこの端末で受け取れます。</p>
    <p>
      <strong>通知する内容：</strong>{view.kinds
        .map((kind) => notificationKindNames[kind])
        .join('、')}
    </p>
    <p>
      メール通知の設定は変わりません。通知の種類はあとから設定で変更できます。
    </p>
    {#if view.error}<p role="alert">
        設定を保存できません。通知設定から有効化してください。
      </p>{/if}
    <ActionGroup align="end" spacing="before">
      <Button type="button" wrap onclick={() => controller?.dismiss()}
        >あとで</Button
      >
      <Button
        type="button"
        variant="primary"
        wrap
        onclick={() => controller?.enable()}>有効にする</Button
      >
    </ActionGroup>
  {:else if view.stage === 'home'}
    <p>
      iPhone・iPadで通知を受け取るには、Cycrioをホーム画面に追加してください。
    </p>
    <ol>
      <li>Safariの共有メニューを開きます。</li>
      <li>「ホーム画面に追加」を選びます。</li>
      <li>表示される場合は「Webアプリとして開く」をONにして追加します。</li>
      <li>
        追加したCycrioのアイコンから開いてログインし、通知を有効にします。
      </li>
    </ol>
    <ActionGroup align="end" spacing="before"
      ><Button type="button" wrap onclick={() => controller?.dismiss()}
        >あとで</Button
      ></ActionGroup
    >
  {:else}
    {#if view.error}
      <p role="alert">
        通知設定の保存を確認できませんでした。通知設定を開いて現在の設定を確認してください。通知の許可はまだ要求していません。
      </p>
    {:else if view.deviceReady}
      <PushSettings controller={pushController} loadOnMount={false} />
    {:else}<p role="status">通知の設定と、この端末の準備をしています…</p>{/if}
    <ActionGroup align="end" spacing="before">
      <ActionLink
        href="/settings/notifications"
        disabled={view.busy}
        onclick={() => controller?.dismiss()}>通知設定を開く</ActionLink
      >
      <Button
        type="button"
        wrap
        disabled={view.busy}
        onclick={() => controller?.dismiss()}>閉じる</Button
      >
    </ActionGroup>
  {/if}
</Modal>

<style>
  p,
  li {
    overflow-wrap: anywhere;
    line-height: 1.7;
  }
  ol {
    padding-inline-start: 24px;
  }
  li + li {
    margin-block-start: 8px;
  }
</style>
