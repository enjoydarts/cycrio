<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { page } from '$app/state';
  import { goto, beforeNavigate } from '$app/navigation';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { ApiError } from './api.ts';
  import {
    createAnalysisAPI,
    type AnalysisFilter,
    type AnalysisPage,
    type AnalysisResult,
  } from './analysis-api.ts';
  import {
    createAnalysisController,
    createAnalysisDetailController,
    adoptFreshAnalysisSelection,
  } from './analysis-controller.ts';
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
  import MailAutomation from './MailAutomation.svelte';
  import AnalysisResultStatus from './AnalysisResultStatus.svelte';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
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
    historyKey = $state(0),
    automationFrozen = $state(false),
    managementOpen = $state(false),
    historyBusy = $state(false),
    navigationMessage = $state('');
  let controller = $state<ReturnType<typeof createAnalysisController>>(),
    detailController:
      ReturnType<typeof createAnalysisDetailController> | undefined,
    detailReadError = $state(false),
    abort: AbortController | undefined,
    disposed = false;
  const tab = $derived(
    ['pending', 'attention', 'registered', 'all', 'history'].includes(
      page.url.searchParams.get('tab') ?? '',
    )
      ? page.url.searchParams.get('tab')!
      : 'all',
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
          : tab === 'all' || tab === 'attention'
            ? 'all'
            : 'related',
      review:
        tab === 'attention'
          ? 'attention'
          : tab === 'pending'
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
    if (detailOpen && selected?.id === result.id) {
      selected = result;
      detailReadError = false;
      void detailController?.load({
        id: result.id,
        account_id: result.account_id,
        fallback: result,
      });
    }
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
      (v) => {
        view = v;
        selected = adoptFreshAnalysisSelection(selected, v);
      },
      session.onDenied,
    );
    detailController = createAnalysisDetailController(
      api,
      token,
      (next) => {
        if (disposed || !detailOpen) return;
        detailReadError = !!next.error;
        selected = adoptFreshAnalysisSelection(selected, next);
      },
      () => {
        selected = null;
        detailOpen = false;
        requests.clear();
        drafts.clear();
        session.onDenied();
      },
    );
    if (tab !== 'history') void controller.load(filter);
    void loadAccounts();
    return () => {
      disposed = true;
      controller?.dispose();
      detailController?.dispose();
      abort?.abort();
      requests.clear();
      drafts.clear();
    };
  });
  $effect(() => {
    const current = controller,
      query = filter,
      currentTab = tab;
    if (currentTab !== 'history')
      untrack(() => {
        void current?.load(query);
      });
  });
  const resultNames = {
    succeeded: '解析完了',
    not_related: '非関連',
    failed: '解析失敗',
    unknown: '結果不明',
  };
  beforeNavigate(({ cancel }) => {
    if (automationFrozen || historyBusy) {
      cancel();
      navigationMessage =
        '受付状況を確認するまで画面の移動を停止しています。同じ要求で受付を確認してください。';
    }
  });
</script>

<section class="daily library-page">
  <LibraryHeader
    title={tab === 'attention'
      ? '確認が必要なメール'
      : tab === 'history'
        ? '取り込み履歴'
        : 'メールの確認'}
    eyebrow="MAIL REVIEW"
  >
    {#snippet action()}
      <a
        class="button primary"
        href={'/import' +
          (filter.account_id ? '?account_id=' + filter.account_id : '')}
        >メールを取り込む</a
      >
    {/snippet}
  </LibraryHeader>
  {#if navigationMessage && (automationFrozen || historyBusy)}<p
      class="notice"
      role="status"
    >
      {navigationMessage}
    </p>{/if}
  <LibraryPanel
    ><nav class="library-tabs" aria-label="解析結果の表示">
      {#each [['all', '最近の結果'], ['attention', '要対応'], ['registered', '登録済み'], ['history', '実行履歴']] as [key, label]}<a
          href={link({ tab: key, offset: 0, state: 'all' })}
          aria-current={tab === key ? 'page' : undefined}>{label}</a
        >{/each}
      {#if tab === 'pending'}<a
          href={link({ tab: 'pending', offset: 0 })}
          aria-current="page">未登録（旧表示）</a
        >{/if}
    </nav>
    <div class="library-toolbar analysis-toolbar">
      <div class="filters">
        <label
          >メール<select
            value={filter.account_id}
            disabled={automationFrozen || historyBusy}
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
        disabled={historyBusy}
        onclick={() =>
          tab === 'history' ? historyKey++ : controller?.load(filter)}
        >更新</button
      >
    </div>
    {#if accountError}<p role="alert" class="notice library-content">
        メールアカウントを読み込めませんでした。<button onclick={loadAccounts}
          >再読み込み</button
        >
      </p>{/if}
    {#if tab === 'history'}<div class="library-content">
        {#key historyKey}<AnalysisHistory
            account={filter.account_id}
            offset={filter.offset}
            previous={link({ offset: Math.max(0, filter.offset - 20) })}
            next={link({ offset: filter.offset + 20 })}
            onBusyChange={(value) => (historyBusy = value)}
          />{/key}
      </div>
    {:else if view.status === 'loading' && !view.page}<p
        class="notice library-content"
        role="status"
      >
        解析結果を読み込んでいます…
      </p>{:else if view.error}<div class="notice library-content" role="alert">
        <p>{processingErrors[view.error]}</p>
        <button onclick={() => controller?.load(filter)}>再読み込み</button>
      </div>{:else if view.page}
      {#if view.page.results.length}<table>
          <thead
            ><tr
              ><th>検出した情報</th><th>抽出金額</th><th
                >解析日時（日本時間）</th
              ><th>処理状況・登録先</th><th>詳細</th></tr
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
                ><td data-label="確認状況"><AnalysisResultStatus {result} /></td
                ><td
                  >{#if result.events.length || ['failed', 'unknown'].includes(result.state)}<button
                      onclick={() => {
                        selected = result;
                        detailReadError = false;
                        detailOpen = true;
                        void detailController?.load({
                          id: result.id,
                          account_id: result.account_id,
                          fallback: result,
                        });
                      }}
                      >{result.state === 'failed' ||
                      result.state === 'unknown' ||
                      result.automation_state === 'needs_review' ||
                      result.billing_automation?.some((a) =>
                        ['needs_review', 'failed'].includes(a.state),
                      )
                        ? '対応する'
                        : '詳細を見る'}</button
                    >{/if}</td
                ></tr
              >{/each}</tbody
          >
        </table>
      {:else}<div class="empty library-content">
          <h2>
            {tab === 'pending'
              ? '未確認の解析結果はありません'
              : tab === 'registered'
                ? '登録済みの解析結果はありません'
                : '解析結果はありません'}
          </h2>
          <p>
            {tab === 'attention'
              ? '対応が必要な解析結果はありません。'
              : '自動処理を始めると、ここに結果が届きます。'}
          </p>
          <div class="actions">
            <a
              href={'/import' +
                (filter.account_id ? '?account_id=' + filter.account_id : '')}
              >メールを取り込む</a
            >
          </div>
        </div>{/if}
      <div class="library-footer">
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
  </LibraryPanel>
  <details
    class="management"
    open={managementOpen || automationFrozen}
    ontoggle={(event) => {
      if (automationFrozen && !event.currentTarget.open)
        event.currentTarget.open = true;
      managementOpen = event.currentTarget.open;
    }}
  >
    <summary
      onclick={(event) => {
        if (automationFrozen) event.preventDefault();
      }}
      >取り込みの管理{#if automationFrozen}<span role="status">
          · 受付状況を確認してください</span
        >{/if}</summary
    >
    <div class="management-body">
      <MailAutomation
        account={accounts.find(
          (a) =>
            a.id ===
            (filter.account_id || page.url.searchParams.get('setup_account')),
        ) ??
          accounts[0] ??
          null}
        returnTo={page.url.pathname + page.url.search}
        onFrozen={(value) => {
          automationFrozen = value;
          if (value) managementOpen = true;
        }}
        onRefresh={() => {
          if (!saving && tab !== 'history') void controller?.load(filter);
        }}
      />
      <div class="manual-actions">
        <h2>手動解析・接続設定</h2>
        <p class="muted">
          同期済みメールの解析や、メール接続の設定を確認できます。
        </p>
        <div class="actions">
          <button disabled={automationFrozen} onclick={() => (startOpen = true)}
            >件数を指定して手動解析</button
          ><a href="/settings/mail">メール接続・手動同期</a>
        </div>
      </div>
    </div>
  </details>
</section>
<Modal
  open={detailOpen}
  title="解析結果を確認"
  busy={saving}
  onclose={() => {
    detailOpen = false;
    selected = null;
    detailReadError = false;
  }}
  >{#if detailReadError}<p class="notice" role="alert">
      最新の登録状況を取得できません。表示中の結果を保持しています。<button
        onclick={() => {
          if (selected)
            void detailController?.load({
              id: selected.id,
              account_id: selected.account_id,
            });
        }}>登録状況を再読み込み</button
      >
    </p>{/if}{#if selected}{#key selected.id}<AnalysisDetail
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

<style>
  .management {
    margin-top: 28px;
    border-top: 1px solid var(--color-border);
    padding: 18px 0;
    color: var(--color-muted);
    font-size: 12px;
  }
  .management > summary {
    cursor: pointer;
    min-height: 32px;
  }
  .management-body {
    padding-top: 20px;
    color: var(--color-text);
  }
  .manual-actions {
    margin-top: 24px;
    padding-top: 16px;
    border-top: 1px solid var(--color-border);
  }
  .manual-actions h2 {
    font-size: 14px;
  }
  .analysis-toolbar .filters {
    flex: 1 1 28rem;
    min-width: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 1rem;
  }
  .analysis-toolbar .filters > label:first-child {
    flex: 0 1 24rem;
    max-width: 100%;
    min-width: 0;
  }
  .analysis-toolbar .filters > label:not(:first-child) {
    flex: 0 0 auto;
    min-width: 0;
  }
  @media (max-width: 550px) {
    .analysis-toolbar .filters {
      display: grid;
      grid-template-columns: minmax(0, 1fr);
      flex-basis: 100%;
      width: 100%;
    }
    .analysis-toolbar .filters > label {
      width: 100%;
      min-width: 0;
    }
  }
</style>
