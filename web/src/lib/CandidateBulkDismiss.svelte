<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { createApprovalAPI, type Candidate } from './approval-api.ts';
  import {
    createCandidateBulkDismiss,
    type CandidateBulkDismissState,
  } from './candidate-bulk-dismiss.ts';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { processingErrors } from './mail-processing-text.ts';
  let {
    candidates,
    onDone,
    onCancel,
    onBusyChange,
    onDenied,
  }: {
    candidates: Candidate[];
    onDone: () => void;
    onCancel: () => void;
    onBusyChange: (busy: boolean) => void;
    onDenied: () => void;
  } = $props();
  const session = getAppSession();
  let view = $state<CandidateBulkDismissState>({
    status: 'idle',
    total: 0,
    completed: 0,
    groups: 0,
    error: null,
  });
  let disposed = false;
  const controller = createCandidateBulkDismiss(
    createApprovalAPI(PUBLIC_API_BASE_URL),
    session.token,
    () => onDenied(),
    (next) => {
      view = next;
      onBusyChange(next.status === 'saving' || next.status === 'uncertain');
    },
  );
  onMount(() => {
    void controller.prepare(candidates);
  });
  onDestroy(() => {
    disposed = true;
    controller.dispose();
    onBusyChange(false);
  });
  async function submit() {
    await controller.submit();
    if (!disposed && controller.state().status === 'success') onDone();
  }
</script>

<div class="daily">
  {#if view.status === 'idle' || view.status === 'loading'}<p role="status">
      選択した候補と関連メールを確認しています…
    </p>
  {:else}
    {#if view.total > 0}<p>
        選択した候補と関連メールの候補、計 {view.total.toLocaleString('ja-JP')} 件を確認待ちから除外します。
      </p>{/if}
    <p class="hint">登録済みのサブスクは変更されません。</p>
    {#if view.status === 'saving' || view.completed}<p role="status">
        除外済み {view.completed.toLocaleString('ja-JP')} / {view.total.toLocaleString(
          'ja-JP',
        )} 件
      </p>{/if}
    {#if view.status === 'uncertain'}<p role="alert" class="notice">
        除外できたか確認できません。同じ内容で結果を確認してください。
      </p>
    {:else if view.error}<p role="alert" class="notice">
        {processingErrors[view.error]}{view.completed
          ? ' 完了済みの分は除外されています。残りは変更していません。'
          : ''}
      </p>{/if}
  {/if}
  <ActionGroup spacing="before">
    {#if view.status === 'ready' || view.status === 'uncertain'}<Button
        wrap
        type="button"
        variant="danger"
        onclick={submit}
        >{view.status === 'uncertain'
          ? '除外結果を確認'
          : 'まとめて除外する'}</Button
      >{/if}
    {#if view.status === 'saving'}<Button wrap type="button" disabled
        >除外しています…</Button
      >{/if}
    <Button
      wrap
      type="button"
      disabled={view.status === 'saving' || view.status === 'uncertain'}
      onclick={view.status === 'error' ? onDone : onCancel}
      >{view.status === 'error' ? '一覧を読み直す' : 'キャンセル'}</Button
    >
  </ActionGroup>
</div>

<style>
  .hint {
    color: var(--color-muted);
    margin-top: 12px;
  }
</style>
