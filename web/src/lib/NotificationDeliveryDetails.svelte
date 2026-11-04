<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api.ts';
  import type {
    NotificationsAPI,
    NotificationDelivery,
  } from './notifications-api.ts';
  import { createFeed, type FeedView } from './daily-controller.ts';
  import { processingDate, processingErrors } from './mail-processing-text.ts';
  import { deliveryState, deliveryReason } from './notification-text.ts';
  import Button from './Button.svelte';
  let {
    id,
    api,
    token,
    onDenied,
  }: {
    id: string;
    api: NotificationsAPI;
    token: TokenProvider;
    onDenied: () => void;
  } = $props();
  let view = $state<FeedView<NotificationDelivery[]>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let feed:
    ReturnType<typeof createFeed<NotificationDelivery[], string>> | undefined;
  onMount(() => {
    feed = createFeed(
      (key: string, bearer, signal) => api.deliveries(key, bearer, signal),
      token,
      (next) => (view = next),
      onDenied,
    );
    void feed.load(id);
    return () => feed?.dispose();
  });
</script>

{#if view.status === 'loading'}<p role="status">通知状態を確認しています…</p>
{:else if view.error}<p role="alert">{processingErrors[view.error]}</p>
  <Button type="button" wrap onclick={() => feed?.load(id)}>状態を再取得</Button
  >
{:else if view.page}<ul>
    {#each view.page as delivery}<li>
        <strong
          >{delivery.channel === 'in_app'
            ? 'アプリ内'
            : 'メール'}：{deliveryState(delivery)}</strong
        >
        <p>更新日時：{processingDate(delivery.updated_at)}（日本時間）</p>
        {#if delivery.reason}<p>{deliveryReason(delivery.reason)}</p>{/if}
      </li>{:else}<li>配信記録はありません。</li>{/each}
  </ul>{/if}

<style>
  ul {
    padding-left: 20px;
  }
  li,
  p {
    overflow-wrap: anywhere;
  }
  p {
    font-size: 12px;
  }
</style>
