<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import {
    createMessageAPI,
    messageFilters,
    type AnalysisMessage,
    type MessageFilter,
  } from './analysis-messages-api.ts';
  import {
    createMessageHistory,
    pollMessageHistory,
    type MessageHistoryView,
  } from './analysis-messages-controller.ts';
  import {
    filterNames,
    messageName,
    stageNames,
  } from './analysis-messages-text.ts';
  import type { ImportAccount } from './import-history-controller.ts';
  import type { MailAccount } from './mail-api.ts';
  import type { SubscriptionDrafts } from './subscriptions-controller.ts';
  import type { ProcessingRequests } from './mail-processing-controller.ts';
  import type { AnalysisResult } from './analysis-api.ts';
  import {
    processingErrors,
    processingReasons,
    processingDate,
  } from './mail-processing-text.ts';
  import Modal from './Modal.svelte';
  import AnalysisDetail from './AnalysisDetail.svelte';
  import AnalysisMessagesPanel from './AnalysisMessagesPanel.svelte';
  import AnalysisBatchHistory from './AnalysisBatchHistory.svelte';
  import ImportHistory from './ImportHistory.svelte';
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
    api = createMessageAPI(PUBLIC_API_BASE_URL),
    drafts: SubscriptionDrafts = new Map(),
    requests: ProcessingRequests = new Map();
  let view = $state<MessageHistoryView>({
      page: null,
      status: 'idle',
      error: null,
      selected: null,
      detailError: false,
      detailLoading: false,
    }),
    controller = $state<ReturnType<typeof createMessageHistory>>(),
    selected = $state<AnalysisMessage | null>(null),
    busy = $state(false),
    batchBusy = $state(false),
    open = $state(false);
  const stage = $derived(
    messageFilters.includes(
      page.url.searchParams.get('stage') as MessageFilter['stage'],
    )
      ? (page.url.searchParams.get('stage') as MessageFilter['stage'])
      : 'all',
  );
  const filter = $derived({ account_id: account, stage, limit: 20, offset });
  const message = $derived(view.selected?.message ?? selected);
  function stageLink(value: string) {
    const query = new URLSearchParams(page.url.searchParams.toString());
    query.set('stage', value);
    query.set('offset', '0');
    return `/analysis?${query}`;
  }
  function pageLink(href: string) {
    const url = new URL(href, 'https://cycrio.invalid');
    url.searchParams.set('stage', stage);
    return url.pathname + url.search;
  }
  function denied() {
    open = false;
    selected = null;
    drafts.clear();
    requests.clear();
    session.onDenied();
  }
  function close() {
    if (busy) return;
    open = false;
    selected = null;
    controller?.close();
  }
  function select(value: AnalysisMessage) {
    if (busy) return;
    selected = value;
    open = true;
    void controller?.select(value.id);
  }
  function registered(result: AnalysisResult) {
    controller?.updateResult(result);
    void controller?.refresh();
  }
  onMount(() => {
    controller = createMessageHistory(
      api,
      session.token,
      (next) => (view = next),
      denied,
      () => busy,
    );
    return () => {
      controller?.dispose();
      drafts.clear();
      requests.clear();
    };
  });
  $effect(() => {
    void controller?.load(filter);
  });
  $effect(() => {
    onBusyChange(busy || batchBusy);
  });
  $effect(() => {
    if (!controller || view.status === 'denied' || view.status === 'disposed')
      return;
    return pollMessageHistory(
      () => void controller?.refresh(),
      () => view.status !== 'loading' && !view.detailLoading && !busy,
    );
  });
</script>

<div class="daily message-history">
  <div class="history-heading">
    <div>
      <p class="secondary">
        メール原文は保存せず、抽出情報と処理状況を表示します。
      </p>
    </div>
  </div>
  {#if view.page}{@const s = view.page.summary}
    <div class="stage-summary" aria-label="アカウント全体の工程別集計">
      <div>
        <span>発見済み</span><strong>{s.discovered}<small>件</small></strong>
      </div>
      {#each [['一次判定', s.primary], ['二次抽出', s.secondary], ['登録', s.registration]] as const as [label, counts]}<div
        >
          <span>{label}</span><strong
            >{counts.completed}<small>件 完了</small></strong
          ><span class="counts"
            >待機 {counts.waiting} · 処理中 {counts.running}{#if counts.failed + counts.unknown + counts.needs_review + counts.cancelled}
              · 要対応 {counts.failed +
                counts.unknown +
                counts.needs_review +
                counts.cancelled}{/if}</span
          >
          {#if counts.not_started || counts.skipped}<span class="counts"
              >未開始 {counts.not_started} · 対象外 {counts.skipped}</span
            >{/if}
        </div>{/each}
    </div>
    <p class="summary-note secondary">
      集計は選択中アカウントの全メールが対象です（工程の絞り込み前）。
    </p>{/if}
  <div class="history-toolbar">
    <label
      >工程<select
        value={stage}
        disabled={busy || batchBusy}
        onchange={(event) => goto(stageLink(event.currentTarget.value))}
        >{#each messageFilters as key}<option value={key}
            >{filterNames[key]}</option
          >{/each}</select
      ></label
    ><span class="secondary" role="status"
      >{view.status === 'loading' ? '更新中…' : '自動更新 · 5秒'}</span
    >
  </div>
  {#if view.error}<p class="notice" role="alert">
      {processingErrors[view.error]}{view.page
        ? '前回取得した状態を表示しています。'
        : ''}<button onclick={() => controller?.refresh()}>再読み込み</button>
    </p>{/if}
  {#if view.status === 'loading' && !view.page}<p class="notice" role="status">
      メールの工程を読み込んでいます…
    </p>{/if}
  {#if view.page}<AnalysisMessagesPanel
      messages={view.page.messages}
      selectedId={open ? selected?.id : null}
      onSelect={select}
    />
    <div class="pager">
      <span>全 {view.page.total} 件（メール単位）</span>
      <div class="actions">
        {#if offset > 0}<a class="button" href={pageLink(previous)}>前へ</a
          >{/if}{#if offset + 20 < view.page.total}<a
            class="button"
            href={pageLink(next)}>次へ</a
          >{/if}
      </div>
    </div>{/if}
  <div class="import-supplement">
    <ImportHistory
      accounts={accounts.filter((a) => !account || a.id === account)}
      compact
    />
  </div>
  <details class="batch-history" open={batchBusy}>
    <summary>バッチごとの実行履歴・停止</summary><AnalysisBatchHistory
      {account}
      onBusyChange={(value) => (batchBusy = value)}
    />
  </details>
</div>
<Modal {open} title="メールの処理状況" {busy} onclose={close}>
  {#if message}<p class="secondary">
      {message.account_label} · 取込日時 {processingDate(
        message.discovered_at,
      )}（日本時間）
    </p>
    <h3>{messageName(message)}</h3>
    <dl class="detail-stages">
      {#each [['一次判定', message.primary], ['二次抽出', message.secondary], ['登録', message.registration]] as const as [label, state]}<div
        >
          <dt>{label}</dt>
          <dd>{stageNames[state]}</dd>
        </div>{/each}
    </dl>
    {#if message.error_code}<p class="notice">
        {(processingReasons as Record<string, string>)[message.error_code] ??
          '現在の工程を確認してください。'}
      </p>{/if}{/if}
  {#if view.detailLoading && !view.selected}<p role="status">
      詳細を読み込んでいます…
    </p>{/if}
  {#if view.detailError}<p class="notice" role="alert">
      詳細を更新できませんでした。表示中の内容を保持しています。<button
        onclick={() => controller?.refresh()}>再読み込み</button
      >
    </p>{/if}
  {#if view.selected?.result}{#key view.selected.result.id}<AnalysisDetail
        result={view.selected.result}
        active={open}
        accounts={accounts as MailAccount[]}
        {requests}
        {drafts}
        onRegistered={registered}
        onAccepted={() => {
          busy = false;
          close();
          void controller?.refresh();
        }}
        onBackToResults={() => {
          close();
          void controller?.refresh();
        }}
        onBusyChange={(value) => (busy = value)}
      />{/key}{:else if view.selected}<p class="secondary">
      処理が進むと、抽出した情報や登録先をここで確認できます。
    </p>{/if}
</Modal>

<style>
  h3 {
    font-size: 16px;
  }
  .secondary {
    font-size: 12px;
    color: var(--color-muted);
  }
  .history-heading p {
    margin: 0;
    line-height: 1.7;
  }
  .stage-summary {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    border: 1px solid var(--color-border);
    border-radius: 8px;
    margin-top: 12px;
  }
  .stage-summary > div {
    display: flex;
    flex-direction: column;
    gap: 5px;
    padding: 12px 14px;
    border-right: 1px solid var(--color-border);
    font-size: 11px;
    color: var(--color-muted);
  }
  .stage-summary > div:last-child {
    border: 0;
  }
  .stage-summary strong {
    font-size: 21px;
    color: var(--color-text);
    font-weight: 600;
  }
  .stage-summary small {
    font-size: 11px;
    font-weight: 400;
    margin-left: 5px;
  }
  .counts {
    line-height: 1.6;
  }
  .summary-note {
    margin: 6px 0 16px !important;
    font-size: 11px;
  }
  .history-toolbar {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
    margin: 16px 0;
  }
  .history-toolbar label {
    font-size: 12px;
    display: flex;
    gap: 10px;
    align-items: center;
  }
  .history-toolbar select {
    max-width: 100%;
    min-width: 150px;
  }
  .history-toolbar > span {
    font-size: 11px;
    white-space: nowrap;
  }
  .import-supplement {
    border-top: 1px solid var(--color-border);
    padding-top: 20px;
    margin-top: 24px;
  }
  .batch-history {
    margin-top: 16px;
    font-size: 12px;
  }
  .batch-history > summary {
    cursor: pointer;
    min-height: 36px;
    display: list-item;
  }
  .detail-stages {
    display: flex;
    flex-wrap: wrap;
    gap: 24px;
    margin: 16px 0 24px;
  }
  .detail-stages dt {
    font-size: 12px;
    color: var(--color-muted);
  }
  .detail-stages dd {
    margin: 6px 0;
    font-size: 14px;
  }
  @media (max-width: 600px) {
    .stage-summary {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    .stage-summary > div:nth-child(2) {
      border-right: 0;
    }
    .stage-summary > div:nth-child(-n + 2) {
      border-bottom: 1px solid var(--color-border);
    }
    .history-toolbar {
      flex-wrap: wrap;
    }
    .stage-summary > div {
      padding: 10px;
    }
    .stage-summary strong {
      font-size: 18px;
    }
  }
</style>
