<script lang="ts">
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { createMailAutomationAPI } from './mail-automation-api.ts';
  import {
    createImportHistory,
    type ImportAccount,
    type ImportEntry,
  } from './import-history-controller.ts';
  import type { FeedView } from './daily-controller.ts';
  import ImportProgressPanel from './ImportProgressPanel.svelte';
  let { accounts }: { accounts: ImportAccount[] } = $props();
  const session = getAppSession();
  let view = $state<FeedView<ImportEntry[]>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let controller = $state<ReturnType<typeof createImportHistory>>();
  onMount(() => {
    controller = createImportHistory(
      createMailAutomationAPI(PUBLIC_API_BASE_URL),
      session.token,
      (next) => (view = next),
      session.onDenied,
    );
    return () => controller?.dispose();
  });
  $effect(() => {
    void controller?.load(accounts);
  });
  $effect(() => {
    if (
      !controller ||
      view.status === 'loading' ||
      view.status === 'denied' ||
      view.status === 'disposed'
    )
      return;
    const timer = setTimeout(() => void controller?.load(accounts), 5000);
    return () => clearTimeout(timer);
  });
</script>

{#if view.status === 'loading' && !view.page}<p role="status">
    取り込み状況を確認しています…
  </p>{/if}
{#if view.error}<p role="alert">
    取り込み状況を更新できませんでした。{view.page
      ? '前回取得した状態を表示しています。'
      : ''}
  </p>
  <button onclick={() => controller?.load(accounts)}
    >取り込み状況を再読み込み</button
  >{/if}
{#if view.page}<ImportProgressPanel entries={view.page} />{/if}
