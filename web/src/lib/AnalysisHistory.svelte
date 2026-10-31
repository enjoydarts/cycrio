<script lang="ts">
  import ActionGroup from './ActionGroup.svelte';
  import Button from './Button.svelte';
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import {
    createImportExecutionsAPI,
    type ImportExecution,
    type ImportExecutionPage,
  } from './import-executions-api.ts';
  import { createFeed, type FeedView } from './daily-controller.ts';
  import type { MailAccount } from './mail-api.ts';
  import type { ProcessingRequests } from './mail-processing-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import { ApiError } from './api.ts';
  import ImportExecutionsPanel from './ImportExecutionsPanel.svelte';
  import ImportExecutionDetail from './ImportExecutionDetail.svelte';
  import AnalysisStart from './AnalysisStart.svelte';
  import MailAutomation from './MailAutomation.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import Modal from './Modal.svelte';
  let {
    account,
    accounts,
    onBusyChange = () => {},
  }: {
    account: string;
    accounts: MailAccount[];
    onBusyChange?: (busy: boolean) => void;
  } = $props();
  const session = getAppSession(),
    api = createImportExecutionsAPI(PUBLIC_API_BASE_URL),
    requests: ProcessingRequests = new Map();
  let view = $state<FeedView<ImportExecutionPage>>({
      page: null,
      status: 'idle',
      error: null,
    }),
    controller =
      $state<
        ReturnType<
          typeof createFeed<
            ImportExecutionPage,
            { account_id: string; limit: number; offset: number }
          >
        >
      >();
  let offset = $state(0),
    selected = $state<ImportExecution | null>(null),
    retryTarget = $state<ImportExecution | null>(null),
    settingsOpen = $state(false),
    retryBusy = $state(false),
    settingsBusy = $state(false),
    stopBusy = $state(false),
    stopError = $state<string | null>(null),
    lastUpdatedAt = $state<string | null>(null),
    accepted = $state<{ accepted_count: number; skipped_count: number } | null>(
      null,
    );
  const source = $derived(accounts.find((a) => a.id === account)),
    filter = $derived({ account_id: account, limit: 20, offset }),
    busy = $derived(retryBusy || settingsBusy || stopBusy);
  let disposed = false,
    stopAbort: AbortController | undefined;
  const count = (value: number) => value.toLocaleString('ja-JP');
  const updateTime = new Intl.DateTimeFormat('ja-JP', {
    timeZone: 'Asia/Tokyo',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  });
  function denied() {
    selected = null;
    retryTarget = null;
    settingsOpen = false;
    requests.clear();
    session.onDenied();
  }
  function refresh() {
    if (account) void controller?.load(filter);
  }
  function retry(row: ImportExecution) {
    selected = null;
    retryTarget = row;
  }
  $effect(() => onBusyChange(busy));
  onMount(() => {
    settingsOpen = page.url.searchParams.get('setup_account') === account;
    controller = createFeed(
      api.list,
      session.token,
      (v) => {
        view = v;
        if (v.status === 'ready' && v.page && !v.error)
          lastUpdatedAt = new Date().toISOString();
        if (selected && v.page) {
          selected =
            v.page.history.find((row) => row.id === selected?.id) ?? selected;
        }
      },
      denied,
    );
    refresh();
    return () => {
      disposed = true;
      stopAbort?.abort();
      controller?.dispose();
      requests.clear();
    };
  });
  $effect(() => {
    if (
      !account ||
      !controller ||
      ['loading', 'denied', 'disposed'].includes(view.status) ||
      busy
    )
      return;
    const timer = setTimeout(refresh, 5000);
    return () => clearTimeout(timer);
  });
  function paginate(next: number) {
    offset = next;
    refresh();
  }
  async function stop(row: ImportExecution) {
    if (!row.active_run_id || busy) return;
    stopBusy = true;
    stopError = null;
    stopAbort = new AbortController();
    try {
      await session.mailProcessingAPI.cancel(
        account,
        row.active_run_id,
        session.token,
        stopAbort.signal,
      );
      if (!disposed) refresh();
    } catch (e) {
      if (!disposed) {
        const code = e instanceof ApiError ? e.code : 'dependency_unavailable';
        stopError = processingErrors[code];
        if (['authentication_required', 'access_denied'].includes(code))
          denied();
      }
    } finally {
      if (!disposed) stopBusy = false;
    }
  }
</script>

{#if account && source}
  <LibraryPanel>
    <div class="library-toolbar connection-toolbar">
      <span class="connection-state"
        ><span class="badge"
          >{source.state === 'ready'
            ? '接続済み'
            : source.state === 'needs_reauth'
              ? '再認証が必要'
              : source.state === 'disconnected'
                ? '切断済み'
                : '接続を確認'}</span
        ><span>取り込み履歴</span></span
      >
      <ActionGroup class={['actions', 'part-AnalysisHistory-actions']}>
        {#if lastUpdatedAt}<time
            class="updated-at"
            datetime={lastUpdatedAt}
            title="5秒ごとに自動更新（日本時間）"
            >最終更新 {updateTime.format(new Date(lastUpdatedAt))}</time
          >{/if}
        <Button wrap disabled={busy} onclick={() => (settingsOpen = true)}
          >取り込み設定</Button
        ><Button wrap disabled={busy} onclick={refresh}>更新</Button>
      </ActionGroup>
    </div>
    {#if accepted}<p class="notice library-content" role="status">
        {count(accepted.accepted_count)} 通の再試行を受け付けました。{#if accepted.skipped_count}取得対象外の
          {count(accepted.skipped_count)} 通はスキップしました。{/if}
      </p>{/if}
    {#if view.error}<p class="notice library-content" role="alert">
        {processingErrors[view.error]}<Button wrap onclick={refresh}
          >再読み込み</Button
        >
      </p>{/if}
    {#if view.page}<ImportExecutionsPanel
        page={view.page}
        onSelect={(row) => {
          selected = row;
          stopError = null;
        }}
        onRetry={retry}
      />
      <ActionGroup
        class={['library-footer', 'part-AnalysisHistory-library-footer']}
      >
        <span>取り込み {count(view.page.total)} 回 · 実行日時は日本時間</span>
        <ActionGroup class={['actions', 'part-AnalysisHistory-actions']}>
          <Button
            wrap
            disabled={busy || offset === 0}
            onclick={() => paginate(Math.max(0, offset - 20))}>前へ</Button
          ><Button
            wrap
            disabled={busy || offset + 20 >= view.page.total}
            onclick={() => paginate(offset + 20)}>次へ</Button
          >
        </ActionGroup>
      </ActionGroup>
    {:else if view.status === 'loading'}<p
        class="notice library-content"
        role="status"
      >
        取り込み履歴を読み込んでいます…
      </p>{/if}
  </LibraryPanel>
  <Modal
    open={!!selected}
    title="取り込みの詳細"
    busy={stopBusy}
    onclose={() => (selected = null)}
  >
    {#if selected}<ImportExecutionDetail
        row={selected}
        {account}
        onRetry={retry}
        onStop={stop}
        stopping={stopBusy}
        error={stopError}
      />{/if}
  </Modal>
  <Modal
    open={!!retryTarget}
    title="この履歴の失敗分を再試行"
    busy={retryBusy}
    onclose={() => (retryTarget = null)}
  >
    {#if retryTarget}{#key retryTarget.id}<AnalysisStart
          accounts={[source]}
          {requests}
          failedOnly
          retryHistory={retryTarget}
          initialAccount={account}
          onBusyChange={(value) => (retryBusy = value)}
          onAccepted={(summary) => {
            retryTarget = null;
            retryBusy = false;
            accepted = summary ?? null;
            refresh();
          }}
          returnTo={`/analysis?tab=history&account_id=${account}`}
        />{/key}{/if}
  </Modal>
  <Modal
    open={settingsOpen}
    title="取り込みの設定"
    busy={settingsBusy}
    onclose={() => {
      settingsOpen = false;
      refresh();
    }}
  >
    {#if settingsOpen}<MailAutomation
        account={source}
        onFrozen={(value) => (settingsBusy = value)}
        onRefresh={refresh}
        returnTo={`/analysis?tab=history&account_id=${account}`}
      />{/if}
  </Modal>
{/if}

<style>
  .updated-at {
    font-size: 11px;
    color: var(--color-muted);
    font-variant-numeric: tabular-nums;
  }
  .connection-toolbar {
    justify-content: space-between;
    gap: 16px;
  }
  .connection-state {
    display: flex;
    align-items: center;
    gap: 12px;
    font-size: 13px;
  }
</style>
