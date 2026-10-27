<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { ApiError } from './api.ts';
  import {
    createAnalysisAPI,
    type AnalysisFilter,
    type AnalysisPage,
    type AnalysisResult,
  } from './analysis-api.ts';
  import { createAnalysisController } from './analysis-controller.ts';
  import type { FeedView } from './daily-controller.ts';
  import type { MailAccount } from './mail-api.ts';
  import type { SubscriptionDrafts } from './subscriptions-controller.ts';
  import type { ProcessingRequests } from './mail-processing-controller.ts';
  import { eventNames, money } from './daily-text.ts';
  import { processingDate, processingErrors } from './mail-processing-text.ts';
  import Modal from './Modal.svelte';
  import AnalysisDetail from './AnalysisDetail.svelte';
  import AnalysisStart from './AnalysisStart.svelte';
  import AnalysisHistory from './AnalysisHistory.svelte';
  import './daily.css';
  const session = getAppSession(),
    token = session.token,
    api = createAnalysisAPI(PUBLIC_API_BASE_URL),
    requests: ProcessingRequests = new Map(),
    drafts: SubscriptionDrafts = new Map();
  let view = $state<FeedView<AnalysisPage>>({
      page: null,
      status: 'idle',
      error: null,
    }),
    accounts = $state<MailAccount[]>([]),
    accountError = $state(false),
    selected = $state<AnalysisResult | null>(null),
    detailOpen = $state(false),
    startOpen = $state(false),
    saving = $state(false),
    starting = $state(false),
    startKey = $state(0),
    historyKey = $state(0);
  let controller = $state<ReturnType<typeof createAnalysisController>>(),
    abort: AbortController | undefined,
    disposed = false;
  const tab = $derived(
    ['registered', 'all', 'history'].includes(
      page.url.searchParams.get('tab') ?? '',
    )
      ? page.url.searchParams.get('tab')!
      : 'pending',
  );
  const filter = $derived.by(() => {
    const p = page.url.searchParams;
    return {
      account_id: p.get('account_id') ?? '',
      state:
        tab === 'all' &&
        ['related', 'failed', 'unknown', 'not_related'].includes(
          p.get('state') ?? '',
        )
          ? p.get('state')
          : tab === 'all'
            ? 'all'
            : 'related',
      review:
        tab === 'pending'
          ? 'pending'
          : tab === 'registered'
            ? 'registered'
            : 'all',
      limit: 20,
      offset: /^\d+$/.test(p.get('offset') ?? '') ? Number(p.get('offset')) : 0,
    } as AnalysisFilter;
  });
  function link(patch: Record<string, string | number> = {}) {
    const p = new URLSearchParams({
      tab,
      account_id: filter.account_id,
      state: filter.state,
      offset: String(filter.offset),
    });
    for (const [key, value] of Object.entries(patch)) p.set(key, String(value));
    return `/analysis?${p}`;
  }
  async function loadAccounts() {
    abort?.abort();
    abort = new AbortController();
    accountError = false;
    try {
      const items = await session.mailAPI.list(token, abort.signal);
      if (!disposed) accounts = items;
    } catch (e) {
      if (disposed) return;
      accountError = true;
      if (
        e instanceof ApiError &&
        ['authentication_required', 'access_denied'].includes(e.code)
      )
        session.onDenied();
    }
  }
  function registered(result: AnalysisResult) {
    selected = result;
    void controller?.load(filter);
  }
  function accepted() {
    startOpen = false;
    detailOpen = false;
    startKey++;
    historyKey++;
    void controller?.load(filter);
    void goto(link({ tab: 'history', offset: 0 }));
  }
  onMount(() => {
    startOpen = page.url.searchParams.get('start') === 'true';
    controller = createAnalysisController(
      api,
      token,
      (v) => (view = v),
      session.onDenied,
    );
    if (tab !== 'history') void controller.load(filter);
    void loadAccounts();
    return () => {
      disposed = true;
      controller?.dispose();
      abort?.abort();
      requests.clear();
      drafts.clear();
    };
  });
  $effect(() => {
    if (tab !== 'history') void controller?.load(filter);
  });
  const resultNames = {
    succeeded: 'AI 抽出・未確認',
    not_related: '非関連',
    failed: '解析失敗',
    unknown: '結果不明',
  };
</script>

<section class="daily">
  <header class="titlebar">
    <div>
      <h1>メール解析</h1>
      <p>AI が見つけた情報を確認して、サブスクに登録します。</p>
    </div>
    <button class="primary" onclick={() => (startOpen = true)}
      >メールを解析</button
    >
  </header>
  <nav class="tabs" aria-label="解析結果の表示">
    {#each [['pending', '未確認'], ['registered', '登録済み'], ['all', 'すべて'], ['history', '実行履歴']] as [key, label]}<a
        href={link({ tab: key, offset: 0, state: 'all' })}
        aria-current={tab === key ? 'page' : undefined}>{label}</a
      >{/each}
  </nav>
  <div class="toolbar">
    <div class="filters">
      <label
        >メールアカウント<select
          value={filter.account_id}
          onchange={(e) =>
            goto(link({ account_id: e.currentTarget.value, offset: 0 }))}
          ><option value="">すべてのアカウント</option
          >{#each accounts as a}<option value={a.id}>{a.label}</option
            >{/each}</select
        ></label
      >{#if tab === 'all'}<label
          >解析状態<select
            value={filter.state}
            onchange={(e) =>
              goto(link({ state: e.currentTarget.value, offset: 0 }))}
            ><option value="all">すべて</option><option value="related"
              >関連あり</option
            ><option value="failed">失敗</option><option value="unknown"
              >結果不明</option
            ><option value="not_related">非関連</option></select
          ></label
        >{/if}
    </div>
    <button
      onclick={() =>
        tab === 'history' ? historyKey++ : controller?.load(filter)}
      >更新</button
    >
  </div>
  {#if accountError}<p role="alert" class="notice">
      メールアカウントを読み込めませんでした。<button onclick={loadAccounts}
        >再読み込み</button
      >
    </p>{/if}
  {#if tab === 'history'}{#key historyKey}<AnalysisHistory
        account={filter.account_id}
        offset={filter.offset}
        previous={link({ offset: Math.max(0, filter.offset - 20) })}
        next={link({ offset: filter.offset + 20 })}
      />{/key}
  {:else if view.status === 'loading'}<p class="notice" role="status">
      解析結果を読み込んでいます…
    </p>{:else if view.error}<div class="notice" role="alert">
      <p>{processingErrors[view.error]}</p>
      <button onclick={() => controller?.load(filter)}>再読み込み</button>
    </div>{:else if view.page}
    {#if view.page.results.length}<table>
        <thead
          ><tr
            ><th>検出した情報</th><th>抽出金額</th><th>解析日時（日本時間）</th
            ><th>確認状況</th><th>詳細</th></tr
          ></thead
        ><tbody
          >{#each view.page.results as result (result.id)}<tr
              ><td
                ><strong
                  >{result.events[0]?.service ??
                    resultNames[result.state]}</strong
                ><span class="secondary"
                  >{result.events.length
                    ? `${eventNames[result.events[0].event_type]}${result.events.length > 1 ? ` / ほか ${result.events.length - 1} イベント` : ''}`
                    : resultNames[result.state]}</span
                ><span class="secondary">{result.account_label}</span></td
              ><td data-label="抽出金額" class="money"
                >{result.events[0]
                  ? money(result.events[0].amount, result.events[0].currency)
                  : '—'}</td
              ><td data-label="解析日時"
                >{processingDate(result.finished_at)}</td
              ><td data-label="確認状況"
                ><span class="badge"
                  >{result.state === 'succeeded'
                    ? `${result.registrations.length} / ${result.events.length} 登録済み`
                    : resultNames[result.state]}</span
                ></td
              ><td
                ><button
                  onclick={() => {
                    selected = result;
                    detailOpen = true;
                  }}>確認する</button
                ></td
              ></tr
            >{/each}</tbody
        >
      </table>
    {:else}<div class="empty">
        <h2>
          {tab === 'pending'
            ? '未確認の解析結果はありません'
            : tab === 'registered'
              ? '登録済みの解析結果はありません'
              : '解析結果はありません'}
        </h2>
        <p>メールを接続・同期してから、条件を確認して解析を開始できます。</p>
        <div class="actions">
          <a href="/settings/mail">メールの接続・同期</a>
        </div>
      </div>{/if}
    <div class="pager">
      <span>全 {view.page.total} 件（メール単位）</span>
      <div class="actions">
        {#if filter.offset > 0}<a
            class="button"
            href={link({ offset: Math.max(0, filter.offset - 20) })}>前へ</a
          >{/if}{#if filter.offset + 20 < view.page.total}<a
            class="button"
            href={link({ offset: filter.offset + 20 })}>次へ</a
          >{/if}
      </div>
    </div>
  {/if}
</section>
<Modal
  open={detailOpen}
  title="解析結果を確認"
  busy={saving}
  onclose={() => (detailOpen = false)}
  >{#if selected}{#key selected.id}<AnalysisDetail
        result={selected}
        active={detailOpen}
        {accounts}
        {requests}
        {drafts}
        onRegistered={registered}
        onAccepted={accepted}
        onBackToResults={() => {
          detailOpen = false;
          void goto(link({ tab: 'all', offset: 0 }));
          void controller?.load({
            ...filter,
            state: 'all',
            review: 'all',
            offset: 0,
          });
        }}
        onBusyChange={(value) => (saving = value)}
      />{/key}{/if}</Modal
>
<Modal
  open={startOpen}
  title="メールを解析"
  busy={starting}
  onclose={() => (startOpen = false)}
  >{#key startKey}<AnalysisStart
      active={startOpen}
      {accounts}
      {requests}
      onAccepted={accepted}
      onBusyChange={(value) => (starting = value)}
      returnTo={page.url.pathname + page.url.search}
      initialAccount={page.url.searchParams.get('setup_account') ?? ''}
      initialMaximum={page.url.searchParams.get('setup_max') ?? ''}
    />{/key}</Modal
>
