<script lang="ts">
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
  import ImportHistory from './ImportHistory.svelte';
  import type { ImportAccount } from './import-history-controller.ts';
  import type { FeedView } from './daily-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import AnalysisHistoryPanel from './AnalysisHistoryPanel.svelte';
  let {
    account,
    accounts,
    offset,
    previous,
    next,
    onBusyChange = () => {},
  }: {
    account: string;
    accounts: ImportAccount[];
    offset: number;
    previous: string;
    next: string;
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
  <ImportHistory
    accounts={accounts.filter((a) => !account || a.id === account)}
  />
  <h2>AI解析の実行履歴</h2>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if view.status === 'loading' && !view.page}<p class="notice" role="status">
      履歴を読み込んでいます…
    </p>{:else if view.error}<p class="notice" role="alert">
      {processingErrors[view.error]}
    </p>
    <button onclick={() => controller?.load(filter)}>再読み込み</button>{/if}
  {#if view.page}<AnalysisHistoryPanel
      page={view.page}
      {saving}
      onCancel={cancel}
    />
    <div class="pager">
      <span>全 {view.page.total} 件</span>
      <div class="actions">
        {#if offset > 0}<a class="button" href={previous}>前へ</a
          >{/if}{#if offset + 20 < view.page.total}<a class="button" href={next}
            >次へ</a
          >{/if}
      </div>
    </div>{/if}
</div>
