<script lang="ts">
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import MailProcessingModels from './MailProcessingModels.svelte';
  import type { ReanalysisView } from './reanalysis-controller.ts';
  import {
    processingBlocked,
    processingDate,
    processingErrors,
  } from './mail-processing-text.ts';
  let {
    view,
    onConfirm,
    onStart,
    onReplay,
    onRefresh,
  }: {
    view: ReanalysisView;
    onConfirm: (value: boolean) => void;
    onStart: () => void;
    onReplay: () => void;
    onRefresh: () => void;
  } = $props();
  const busy = $derived(view.status === 'loading' || view.status === 'saving');
  const ready = $derived(
    view.preview?.ready && view.preview.full_email_allowed === true,
  );
</script>

<div class="reanalysis-confirmation">
  <p>
    この確認画面を開いた時刻（{processingDate(
      view.before,
    )}・日本時間）より前に、成功・対象外と判定されたメールを全件再解析します。失敗・結果不明・未処理は対象に含みません。
  </p>
  <p>
    取得元に元メールが残っている必要があります。確認済み・不要と判断した元メールは保護してスキップします。再解析が成功するまでは以前の候補を保持します。
  </p>
  <p>
    メール本文を再取得してAIに送信するため、再度料金が発生します。この再解析中は同じメール接続の通常解析が待機します。新着メールの所在取得は継続します。
  </p>
  {#if view.preview}
    <MailProcessingModels
      primary={view.preview.primary}
      secondary={view.preview.secondary}
      max_input_bytes={view.preview.max_input_bytes}
      max_output_tokens={view.preview.max_output_tokens}
      timeout_seconds={view.preview.timeout_seconds}
    />
    {#if !ready}<p class="notice" role="status">
        {view.preview.ready
          ? 'メール全文の送信許可を取り込み設定で確認してください。'
          : processingBlocked[view.preview.blocked_reason]}
      </p>{/if}
  {/if}
  {#if view.error}<p class="notice" role="alert">
      {processingErrors[view.error]}{#if view.error === 'conflict'}
        最新の設定と送信条件を確認し、もう一度同意してください。{/if}
    </p>{/if}
  {#if view.uncertain}
    <p class="notice" role="status">
      受付結果を確認できません。同じリクエストで受付を確認してください。確認が終わるまでこの画面を閉じられません。
    </p>
    <Button type="button" wrap disabled={busy} onclick={onReplay}
      >同じリクエストで受付を確認</Button
    >
  {:else}
    <label class="consent"
      ><input
        type="checkbox"
        checked={view.confirmed}
        disabled={busy || !ready}
        onchange={(event) => onConfirm(event.currentTarget.checked)}
      />メール全文の再送信と全件の再解析による再課金に同意します</label
    >
    <ActionGroup>
      <Button
        type="button"
        wrap
        variant="primary"
        disabled={busy || !ready || !view.confirmed}
        onclick={onStart}
        >{view.status === 'saving'
          ? '受付中…'
          : '同意して処理済みメールを再解析'}</Button
      >
      <Button type="button" wrap disabled={busy} onclick={onRefresh}
        >送信条件を再確認</Button
      >
    </ActionGroup>
  {/if}
</div>

<style>
  .reanalysis-confirmation {
    display: grid;
    gap: 16px;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .reanalysis-confirmation p {
    margin: 0;
  }
  .consent {
    display: flex;
    align-items: flex-start;
    gap: 8px;
  }
  .consent input {
    flex-shrink: 0;
    margin-top: 4px;
  }
</style>
