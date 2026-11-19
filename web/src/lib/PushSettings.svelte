<script lang="ts">
  import { onMount } from 'svelte';
  import type { PushController, PushView } from './push-controller.ts';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import DataTable from './DataTable.svelte';
  import { deliveryReason } from './notification-text.ts';
  import { processingDate } from './mail-processing-text.ts';
  let {
    controller,
    loadOnMount = true,
  }: { controller: PushController; loadOnMount?: boolean } = $props();
  let view = $state<PushView>({
    page: null,
    status: 'idle',
    busy: false,
    cleanupPending: false,
    currentDevice: null,
  });
  let refreshing = $state(false);
  let refreshResult = $state('');
  async function refresh() {
    refreshing = true;
    refreshResult = '';
    try {
      refreshResult = (await controller.load())
        ? '端末一覧を更新しました。'
        : '端末一覧を再取得できませんでした。';
    } finally {
      refreshing = false;
    }
  }
  const messages: Record<PushView['status'], string> = {
    idle: '',
    preparing: 'この端末の通知を設定しています…',
    permission_ready: '',
    registering: '端末を登録しています…',
    waiting:
      'この端末の登録を完了できていません。端末一覧を再取得してください。',
    expired: 'この端末の登録が完了していません。もう一度有効にしてください。',
    confirming: '端末を登録しています…',
    active: 'この端末のPush通知は有効です。',
    disabled: '端末の通知を停止しました。',
    unsupported:
      'このブラウザーはPush通知に対応していません。対応するブラウザーで開いてください。',
    home_screen:
      'iPhone・iPadでは、共有メニューからホーム画面に追加し、追加したアプリを開いて操作してください。',
    denied:
      '通知が拒否されています。ブラウザーまたは端末のシステム設定で、このサイトの通知を許可してください。',
    error:
      '端末の通知を設定できませんでした。通信状態を確認してもう一度お試しください。',
    uncertain:
      '受付結果を確認できません。端末一覧を再取得して状態を確認してください。',
    reload:
      'Pushサービスの設定変更、またはSDKの初期化状態を確認できないため、ページを再読み込みしてから操作してください。',
  };
  onMount(() => {
    const unsubscribe = controller.subscribe((next) => (view = next));
    if (loadOnMount) void controller.load();
    return unsubscribe;
  });
</script>

<section aria-label="Push通知の端末設定">
  <h3>Push通知の端末</h3>
  {#if messages[view.status]}<p role="status">{messages[view.status]}</p>{/if}
  {#if view.page && !view.page.available}<p>
      {deliveryReason(view.page.reason)}
    </p>{/if}
  {#if view.cleanupPending}<p role="alert">
      以前の端末停止が完了していません。再試行してください。
    </p>
    <Button type="button" wrap onclick={() => controller.retryCleanup()}
      >端末停止を再試行</Button
    >{/if}
  <ActionGroup stackOnMobile spacing="after">
    {#if view.page?.available && !['active', 'waiting', 'confirming', 'registering', 'reload', 'uncertain'].includes(view.status)}
      <Button
        type="button"
        wrap
        variant="primary"
        disabled={view.busy || view.cleanupPending}
        onclick={() => controller.enable()}
        >この端末でPush通知を有効にする</Button
      >
    {/if}
    <Button
      type="button"
      wrap
      disabled={view.busy || refreshing}
      onclick={refresh}
      >{refreshing ? '端末一覧を再取得中…' : '端末一覧を再取得'}</Button
    >
  </ActionGroup>
  {#if refreshResult}<p role="status">{refreshResult}</p>{/if}
  {#if view.page}<LibraryPanel
      ><DataTable responsive="cards"
        ><thead
          ><tr><th>端末</th><th>登録日</th><th>状態</th><th>操作</th></tr
          ></thead
        ><tbody>
          {#each view.page.devices as device}<tr
              ><td data-primary
                >{device.id === view.currentDevice
                  ? 'この端末'
                  : '登録端末'}</td
              ><td data-label="登録日"
                >{processingDate(device.created_at)}（日本時間）</td
              ><td data-label="状態"
                >{device.state === 'active'
                  ? '有効'
                  : device.state === 'disabled'
                    ? '停止'
                    : '登録未完了'}</td
              ><td data-actions
                >{#if device.state !== 'disabled'}<Button
                    type="button"
                    wrap
                    disabled={view.busy}
                    onclick={() => controller.disable(device.id)}
                    >この端末の通知を停止</Button
                  >{/if}</td
              ></tr
            >{:else}<tr><td colspan="4">登録した端末はありません。</td></tr
            >{/each}
        </tbody></DataTable
      ></LibraryPanel
    >{/if}
</section>
