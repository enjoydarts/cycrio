<script lang="ts">
  import { onMount } from 'svelte';
  import type { PushController, PushView } from './push-controller.ts';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import DataTable from './DataTable.svelte';
  import { deliveryReason } from './notification-text.ts';
  import { processingDate } from './mail-processing-text.ts';
  let { controller }: { controller: PushController } = $props();
  let view = $state<PushView>({
    page: null,
    status: 'idle',
    busy: false,
    cleanupPending: false,
    currentDevice: null,
  });
  const messages: Record<PushView['status'], string> = {
    idle: 'この端末のPush通知は、本人の操作後に準備します。',
    preparing: '通知の準備中です…',
    permission_ready:
      '準備ができました。次のボタンでこの端末の通知を許可してください。',
    registering: '端末を登録しています…',
    waiting:
      '受信確認中です。確認通知の有効期限は5分です。届かない場合は端末一覧を再取得してください。',
    expired:
      '受信確認の期限が切れました。この端末でPush通知を有効にする操作からやり直してください。',
    confirming: '受信した通知を確認しています…',
    active: 'この端末のPush通知は有効です。',
    disabled: '端末の通知を停止しました。',
    unsupported:
      'このブラウザーはPush通知に対応していません。対応するブラウザーで開いてください。',
    home_screen:
      'iPhone・iPadでは、共有メニューからホーム画面に追加し、追加したアプリを開いて操作してください。',
    denied:
      '通知が拒否されています。ブラウザーまたは端末のシステム設定で、このサイトの通知を許可してください。',
    error:
      '端末設定を確認できませんでした。通信状態を確認して再取得してください。',
    uncertain:
      '受付結果を確認できません。端末一覧を再取得して状態を確認してください。',
    reload:
      'Pushサービスの設定変更、またはSDKの初期化状態を確認できないため、ページを再読み込みしてから操作してください。',
  };
  onMount(() => {
    const unsubscribe = controller.subscribe((next) => (view = next));
    void controller.load();
    return unsubscribe;
  });
</script>

<section aria-label="Push通知の端末設定">
  <h3>Push通知の端末</h3>
  <p role="status">{messages[view.status]}</p>
  {#if view.page && !view.page.available}<p>
      {deliveryReason(view.page.reason)}
    </p>{/if}
  {#if view.cleanupPending}<p role="alert">
      以前の端末停止が完了していません。再試行してください。
    </p>
    <Button type="button" wrap onclick={() => controller.retryCleanup()}
      >端末停止を再試行</Button
    >{/if}
  <ActionGroup>
    {#if view.page?.available && !['active', 'waiting', 'confirming', 'registering', 'reload', 'uncertain'].includes(view.status)}
      {#if view.status === 'permission_ready'}<Button
          type="button"
          variant="primary"
          wrap
          disabled={view.busy}
          onclick={() => controller.allow()}>この端末の通知を許可する</Button
        >
      {:else}<Button
          type="button"
          wrap
          disabled={view.busy || view.cleanupPending}
          onclick={() => controller.prepare()}
          >この端末でPush通知を有効にする</Button
        >{/if}
    {/if}
    <Button
      type="button"
      wrap
      disabled={view.busy}
      onclick={() => controller.load()}>端末一覧を再取得</Button
    >
  </ActionGroup>
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
                  : device.state === 'pending'
                    ? '受信確認中'
                    : device.state === 'expired'
                      ? '受信確認の期限切れ'
                      : '停止'}</td
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
