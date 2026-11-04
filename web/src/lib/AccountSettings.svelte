<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api.ts';
  import type { ErasureAPI } from './erasure-api.ts';
  import {
    createErasureController,
    type ErasureView,
  } from './erasure-controller.ts';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  let {
    api,
    token,
    signOut,
  }: {
    api: ErasureAPI;
    token: TokenProvider;
    signOut?: () => Promise<void> | void;
  } = $props();
  let open = $state(false);
  let view = $state<ErasureView>({
    confirmation: '',
    confirmed: false,
    busy: false,
    uncertain: false,
    accepted: false,
    error: null,
  });
  let controller: ReturnType<typeof createErasureController> | undefined;
  onMount(() => {
    controller = createErasureController(
      api,
      token,
      (next) => {
        view = next;
      },
      () => signOut?.(),
    );
    return () => controller?.dispose();
  });
</script>

<section class="library-page" aria-label="アカウント設定">
  <LibraryHeader
    title="アカウント"
    description="Cycrioから退会し、利用データを削除します。"
  />
  <LibraryPanel padded>
    <h2>退会とデータ削除</h2>
    <p>
      退会の受付後は利用を停止し、契約・請求履歴・メール解析結果・メール接続・個人用の資格情報・通知などの利用データを削除します。この操作は取り消せません。
    </p>
    <p>
      Cycrio内のデータ削除後、Clerkのユーザーも削除します。Clerkへの削除要求が失敗した場合は自動で再試行します。
    </p>
    <p>
      Google・Microsoftのアカウント自体は削除しません。利用先の有料サービスやサブスクリプションの解約は、ご自身で行ってください。
    </p>
    <p>
      提供元でのOAuth許可は取り消されません。必要に応じてGoogle・Microsoftなどの設定でも連携を解除してください。外部プロバイダーへ送信済みの内容は回収しません。
    </p>
    <p>
      本人を識別できない月単位の費用・監査集計と、再登録・遅着原価の照合に必要な指紋を保持します。共有のサービス用APIキー・管理設定・他の利用者のデータは削除しません。
    </p>
    <Button
      type="button"
      variant="danger"
      wrap
      disabled={!signOut}
      onclick={() => {
        open = true;
      }}>退会の確認へ</Button
    >
  </LibraryPanel>
  <Modal
    {open}
    title="Cycrioから退会する"
    busy={view.busy}
    canClose={() => !view.uncertain && !view.accepted}
    onclose={() => {
      open = false;
      controller?.confirm('', false);
    }}
  >
    <p>データ削除は取り消せません。サブスクリプションの解約は別途必要です。</p>
    {#if view.accepted}
      <p role="status">
        退会を受け付けました。利用データの削除を進め、サインアウトします。
      </p>
    {:else}
      <label class="confirmation"
        >確認のため「退会する」と入力してください<input
          aria-label="退会の確認文言"
          value={view.confirmation}
          disabled={view.busy || view.uncertain}
          oninput={(event) =>
            controller?.confirm(event.currentTarget.value, view.confirmed)}
          autocomplete="off"
        /></label
      >
      <label class="acknowledgement"
        ><input
          type="checkbox"
          checked={view.confirmed}
          disabled={view.busy || view.uncertain}
          onchange={(event) =>
            controller?.confirm(view.confirmation, event.currentTarget.checked)}
        />データ削除と、サービスの解約は別途必要なことを確認しました</label
      >
      {#if view.uncertain}
        <p role="status">
          受付結果を確認できません。再送せず、同じ退会要求の状況を確認してください。
        </p>
        <Button
          type="button"
          wrap
          disabled={view.busy}
          onclick={() => controller?.reconcile()}>退会の受付状況を確認</Button
        >
      {:else}
        <ActionGroup
          ><Button
            type="button"
            variant="danger"
            wrap
            disabled={view.busy ||
              !view.confirmed ||
              view.confirmation !== '退会する'}
            onclick={() => controller?.submit()}>退会してデータを削除</Button
          ></ActionGroup
        >
      {/if}
      {#if view.error}<p role="alert">
          退会の受付状況を確認できませんでした。時間をおいて状況を確認してください。
        </p>{/if}
    {/if}
  </Modal>
</section>

<style>
  p {
    overflow-wrap: anywhere;
    line-height: 1.8;
  }
  .confirmation {
    display: grid;
    gap: 8px;
    margin-block: 16px;
  }
  .confirmation input {
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
  }
  .acknowledgement {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    margin-block: 20px;
    line-height: 1.7;
  }
</style>
