<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, type TokenProvider } from './api.ts';
  import type { NotificationsAPI } from './notifications-api.ts';
  import {
    createNotificationInbox,
    type NotificationInboxView,
  } from './notification-inbox-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import Modal from './Modal.svelte';
  import Pagination from './Pagination.svelte';
  import NotificationList from './NotificationList.svelte';
  import NotificationSettings from './NotificationSettings.svelte';
  import NotificationDeliveryDetails from './NotificationDeliveryDetails.svelte';
  let {
    api,
    token,
    onDenied = () => {},
  }: {
    api?: NotificationsAPI;
    token?: TokenProvider;
    onDenied?: () => void;
  } = $props();
  let open = $state(false),
    tab = $state<'list' | 'settings'>('list'),
    unread = $state<number | null>(null),
    frozen = $state(false),
    readFilter = $state<'all' | 'unread' | 'read'>('unread'),
    offset = $state(0),
    deliveryID = $state('');
  let view = $state<NotificationInboxView>({
    page: null,
    status: 'idle',
    reading: null,
    error: null,
  });
  let inbox: ReturnType<typeof createNotificationInbox> | undefined,
    disposed = false,
    headerSequence = 0,
    headerAbort: AbortController | undefined;
  const busy = $derived(frozen || view.reading !== null);
  function denied() {
    open = false;
    unread = null;
    deliveryID = '';
    tab = 'list';
    frozen = false;
    headerSequence++;
    headerAbort?.abort();
    inbox?.dispose();
    view = {
      page: null,
      status: 'denied',
      reading: null,
      error: 'access_denied',
    };
    onDenied();
  }
  async function refreshUnread() {
    if (!api || !token || disposed || view.status === 'denied') return;
    const current = ++headerSequence;
    headerAbort?.abort();
    headerAbort = new AbortController();
    try {
      const page = await api.list(
        { read: 'unread', limit: 1, offset: 0 },
        token,
        headerAbort.signal,
      );
      if (!disposed && current === headerSequence) unread = page.unread_count;
    } catch (error) {
      if (
        !disposed &&
        current === headerSequence &&
        error instanceof ApiError &&
        ['authentication_required', 'access_denied'].includes(error.code)
      )
        denied();
    }
  }
  function load(next = 0) {
    offset = next;
    deliveryID = '';
    void inbox?.load({ read: readFilter, limit: 20, offset });
  }
  onMount(() => {
    if (api && token) {
      inbox = createNotificationInbox(
        api,
        token,
        (next) => (view = next),
        denied,
        (value) => (unread = value),
      );
      void refreshUnread();
    }
    const refresh = () => {
      if (document.visibilityState === 'visible' && !open) void refreshUnread();
    };
    const timer = window.setInterval(refresh, 60000);
    window.addEventListener('focus', refresh);
    return () => {
      disposed = true;
      headerSequence++;
      headerAbort?.abort();
      inbox?.dispose();
      window.clearInterval(timer);
      window.removeEventListener('focus', refresh);
    };
  });
</script>

<svelte:window
  onbeforeunload={(event) => {
    if (busy) {
      event.preventDefault();
      event.returnValue = '';
    }
  }}
/>
<Button
  type="button"
  variant="icon"
  aria-label={unread === null ? '通知を開く' : `通知を開く（未読${unread}件）`}
  title="通知"
  disabled={!api || !token || view.status === 'denied'}
  onclick={() => {
    open = true;
    tab = 'list';
    load();
  }}
  ><span class="bell"
    ><svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.7"
      aria-hidden="true"
      ><path d="M6 9a6 6 0 0 1 12 0v6l2 3H4l2-3V9Z M10 21h4" /></svg
    >{#if unread && unread > 0}<span class="count" aria-hidden="true"
        >{unread > 99 ? '99+' : unread}</span
      >{/if}</span
  ></Button
>
{#if api && token}<Modal
    {open}
    title="通知"
    {busy}
    onclose={() => {
      open = false;
      deliveryID = '';
      void refreshUnread();
    }}
  >
    <ActionGroup
      ><Button
        type="button"
        wrap
        variant="tab"
        aria-current={tab === 'list' ? 'page' : undefined}
        disabled={busy}
        onclick={() => {
          tab = 'list';
          load(offset);
        }}>通知一覧</Button
      ><Button
        type="button"
        wrap
        variant="tab"
        aria-current={tab === 'settings' ? 'page' : undefined}
        disabled={busy}
        onclick={() => (tab = 'settings')}>通知設定</Button
      ></ActionGroup
    >
    {#if tab === 'settings'}<NotificationSettings
        {api}
        {token}
        onDenied={denied}
        onFrozen={(value) => (frozen = value)}
      />
    {:else}<section
        class="daily notification-inbox"
        aria-label="本人の通知一覧"
      >
        <label
          >表示する通知<select
            value={readFilter}
            disabled={busy}
            onchange={(event) => {
              readFilter = event.currentTarget.value as typeof readFilter;
              load();
            }}
            ><option value="all">すべて</option><option value="unread"
              >未読</option
            ><option value="read">既読</option></select
          ></label
        >
        {#if view.status === 'loading'}<p role="status">
            通知を読み込んでいます…
          </p>{:else if view.error}<p role="alert" class="notice">
            {processingErrors[view.error]}
          </p>
          <Button type="button" wrap onclick={() => load(offset)}
            >通知を再取得</Button
          >
        {:else if view.page}<NotificationList
            notifications={view.page.notifications}
            {busy}
            onRead={(id) => inbox?.read(id)}
            onDelivery={(id) => (deliveryID = deliveryID === id ? '' : id)}
            onNavigate={() => {
              open = false;
              deliveryID = '';
            }}
          /><ActionGroup
            ><span
              >全 {view.page.total} 件 · 未読 {view.page.unread_count} 件</span
            ><Pagination
              total={view.page.total}
              offset={view.page.offset}
              limit={view.page.limit}
              label="通知のページ"
              disabled={busy}
              onchange={load}
            /></ActionGroup
          >{/if}
        {#if deliveryID}<section aria-label="選択した通知の状態">
            <h3>通知の状態</h3>
            {#key deliveryID}<NotificationDeliveryDetails
                id={deliveryID}
                {api}
                {token}
                onDenied={denied}
              />{/key}
          </section>{/if}
      </section>{/if}
  </Modal>{/if}

<style>
  .bell {
    position: relative;
    display: inline-flex;
  }
  .count {
    position: absolute;
    top: -8px;
    right: -12px;
    border-radius: 10px;
    padding: 1px 4px;
    font-size: 10px;
    line-height: 14px;
    background: var(--color-action);
    color: var(--color-on-action, white);
  }
  .notification-inbox {
    margin-top: 16px;
    min-width: 0;
  }
  label {
    display: grid;
    gap: 8px;
    margin-bottom: 16px;
  }
</style>
