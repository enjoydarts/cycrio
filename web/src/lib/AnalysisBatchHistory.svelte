<script lang="ts">
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { ApiError } from './api.ts';
  import {
    createAnalysisAPI,
    type AnalysisRun,
    type RunPage,
  } from './analysis-api.ts';
  import { createAnalysisHistory } from './analysis-controller.ts';
  import type { FeedView } from './daily-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import AnalysisHistoryPanel from './AnalysisHistoryPanel.svelte';
  let {
    account,
    onBusyChange = () => {},
  }: {
    account: string;
    onBusyChange?: (busy: boolean) => void;
  } = $props();
  const session = getAppSession(),
    token = session.token,
    api = createAnalysisAPI(PUBLIC_API_BASE_URL);
  let view = $state<FeedView<RunPage>>({
      page: null,
      status: 'idle',
      error: null,
    }),
    saving = $state<string | null>(null),
    error = $state<string | null>(null);
  let offset = $state(0);
  $effect(() => {
    account;
    offset = 0;
  });
  let controller = $state<ReturnType<typeof createAnalysisHistory>>(),
    abort: AbortController | undefined,
    disposed = false;
  const filter = $derived({ account_id: account, limit: 20, offset });
  $effect(() => {
    onBusyChange(!!saving);
  });
  onMount(() => {
    controller = createAnalysisHistory(
      api,
      token,
      (v) => (view = v),
      session.onDenied,
    );
    void controller.load(filter);
    return () => {
      disposed = true;
      controller?.dispose();
      abort?.abort();
    };
  });
  $effect(() => {
    void controller?.load(filter);
  });
  $effect(() => {
    if (
      view.status === 'loading' ||
      view.status === 'denied' ||
      view.status === 'disposed'
    )
      return;
    const timer = setTimeout(() => void controller?.load(filter), 5000);
    return () => clearTimeout(timer);
  });
  async function cancel(run: AnalysisRun) {
    if (saving) return;
    saving = run.id;
    error = null;
    abort = new AbortController();
    try {
      await session.mailProcessingAPI.cancel(
        run.account_id,
        run.id,
        token,
        abort.signal,
      );
      if (!disposed) await controller?.load(filter);
    } catch (e) {
      if (!disposed) {
        const code = e instanceof ApiError ? e.code : 'dependency_unavailable';
        error = processingErrors[code];
        if (['authentication_required', 'access_denied'].includes(code))
          session.onDenied();
      }
    } finally {
      if (!disposed) saving = null;
    }
  }
</script>

<div class="daily">
  <h2>AI解析の実行履歴</h2>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if view.status === 'loading' && !view.page}<p class="notice" role="status">
      履歴を読み込んでいます…
    </p>{:else if view.error}<p class="notice" role="alert">
      {processingErrors[view.error]}
    </p>
    <Button wrap onclick={() => controller?.load(filter)}>再読み込み</Button
    >{/if}
  {#if view.page}<AnalysisHistoryPanel
      page={view.page}
      {saving}
      onCancel={cancel}
    />
    <ActionGroup class={['pager', 'part-AnalysisBatchHistory-pager']}>
      <span>全 {view.page.total} 件</span>
      <ActionGroup class={['actions', 'part-AnalysisBatchHistory-actions']}>
        {#if offset > 0}<Button
            wrap
            disabled={!!saving}
            onclick={() => (offset = Math.max(0, offset - 20))}>前へ</Button
          >{/if}{#if offset + 20 < view.page.total}<Button
            wrap
            disabled={!!saving}
            onclick={() => (offset += 20)}>次へ</Button
          >{/if}
      </ActionGroup>
    </ActionGroup>{/if}
</div>
