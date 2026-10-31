<script lang="ts">
  import ActionLink from './ActionLink.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { onMount, untrack } from 'svelte';
  import { page } from '$app/state';
  import { goto, beforeNavigate } from '$app/navigation';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { getWorkspace } from './workspace-context.ts';
  import { ApiError } from './api.ts';
  import {
    createApprovalAPI,
    type Candidate,
    type CandidatePage,
  } from './approval-api.ts';
  import { createFeed, type FeedView } from './daily-controller.ts';
  import type { MailAccount } from './mail-api.ts';
  import type { SubscriptionDrafts } from './subscriptions-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import CandidatesPanel from './CandidatesPanel.svelte';
  import CandidateDetail from './CandidateDetail.svelte';
  import AnalysisHistory from './AnalysisHistory.svelte';
  import Modal from './Modal.svelte';
  import './daily.css';
  const session = getAppSession(),
    workspace = getWorkspace(),
    api = createApprovalAPI(PUBLIC_API_BASE_URL),
    drafts: SubscriptionDrafts = new Map();
  let accounts = $state<MailAccount[]>([]),
    accountError = $state(false),
    loadedAccounts = $state(false),
    selected = $state<Candidate | null>(null),
    initialAction = $state<'dismiss' | null>(null),
    busy = $state(false),
    notice = $state('');
  let view = $state<FeedView<CandidatePage>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let controller =
    $state<
      ReturnType<
        typeof createFeed<
          CandidatePage,
          { account_id: string; limit: number; offset: number }
        >
      >
    >();
  let abort: AbortController | undefined,
    disposed = false;
  const history = $derived(
    page.url.searchParams.get('tab') === 'history' ||
      !!page.url.searchParams.get('setup_account'),
  );
  const account = $derived(page.url.searchParams.get('account_id') ?? '');
  const currentAccount = $derived(accounts.find((a) => a.id === account));
  const filter = $derived({
    account_id: account,
    limit: 20,
    offset: /^\d+$/.test(page.url.searchParams.get('offset') ?? '')
      ? Number(page.url.searchParams.get('offset'))
      : 0,
  });
  function link(offset = 0, id = account) {
    const p = new URLSearchParams({
      tab: history ? 'history' : 'attention',
      offset: String(offset),
    });
    if (id) p.set('account_id', id);
    return `/analysis?${p}`;
  }
  function denied() {
    selected = null;
    drafts.clear();
    session.onDenied();
  }
  async function loadAccounts() {
    abort?.abort();
    abort = new AbortController();
    accountError = false;
    try {
      const next = await session.mailAPI.list(session.token, abort.signal);
      if (!disposed) {
        accounts = next;
        loadedAccounts = true;
      }
    } catch (e) {
      if (disposed) return;
      accountError = true;
      if (
        e instanceof ApiError &&
        ['authentication_required', 'access_denied'].includes(e.code)
      )
        denied();
    }
  }
  function done() {
    selected = null;
    busy = false;
    void controller?.load(filter);
    void workspace?.refresh();
  }
  onMount(() => {
    controller = createFeed(
      api.candidates,
      session.token,
      (v) => (view = v),
      denied,
    );
    void loadAccounts();
    return () => {
      disposed = true;
      controller?.dispose();
      abort?.abort();
      drafts.clear();
    };
  });
  $effect(() => {
    const c = controller,
      f = filter;
    if (!history) untrack(() => void c?.load(f));
  });
  $effect(() => {
    if (
      view.status === 'ready' &&
      view.page &&
      !view.page.candidates.length &&
      filter.offset > 0 &&
      !selected &&
      !busy
    )
      void goto(
        link(
          Math.max(0, Math.ceil(view.page.total / filter.limit) - 1) *
            filter.limit,
        ),
      );
  });
  $effect(() => {
    if (
      history ||
      !controller ||
      ['loading', 'denied', 'disposed'].includes(view.status) ||
      busy
    )
      return;
    const timer = setTimeout(() => void controller?.load(filter), 5000);
    return () => clearTimeout(timer);
  });
  beforeNavigate(({ cancel }) => {
    if (busy) {
      cancel();
      notice =
        '受付状況を確認するまで、この画面で同じ要求の確認を続けてください。';
    }
  });
</script>

<svelte:window
  onbeforeunload={(event) => {
    if (busy) {
      event.preventDefault();
      event.returnValue = '';
    }
  }}
/>

<section class="daily library-page">
  <LibraryHeader
    title={history ? (currentAccount?.label ?? 'メールを選択') : '確認が必要'}
    eyebrow={history ? 'MAIL IMPORT' : 'REVIEW'}
  >
    {#snippet action()}{#if history && currentAccount}<ActionLink
          wrap
          class={['button']}
          href={`/settings/mail?account_id=${currentAccount.id}`}
          >接続設定</ActionLink
        >{:else if !history}<ActionLink wrap class={['button']} href="/import"
          >メールを取り込む</ActionLink
        >{/if}{/snippet}
  </LibraryHeader>
  {#if notice && busy}<p class="notice" role="status">{notice}</p>{/if}
  {#if accountError}<p class="notice" role="alert">
      メール接続を読み込めませんでした。<Button wrap onclick={loadAccounts}
        >再読み込み</Button
      >
    </p>{/if}
  {#if history}
    {#if currentAccount}{#key currentAccount.id}<AnalysisHistory
          account={currentAccount.id}
          accounts={[currentAccount]}
          onBusyChange={(value) => (busy = value)}
        />{/key}
    {:else}<LibraryPanel
        ><div class="library-content">
          <h2>取り込み履歴を確認するメールを選択</h2>
          <p class="muted">
            取り込み状況と履歴は、メール接続ごとに確認できます。
          </p>
          <ul class="compact-list">
            {#each accounts as item}<li>
                <a href={link(0, item.id)}>{item.label}</a>
              </li>{/each}
          </ul>
          {#if loadedAccounts && !accounts.length}<ActionLink
              wrap
              variant="primary"
              class={['button primary']}
              href="/import?connect=true">メールを接続する</ActionLink
            >{/if}
        </div></LibraryPanel
      >{/if}
  {:else}
    <LibraryPanel>
      <div class="library-toolbar">
        <label
          >メール接続<select
            value={account}
            onchange={(e) => goto(link(0, e.currentTarget.value))}
            ><option value="">すべてのメール接続</option
            >{#each accounts as item}<option value={item.id}
                >{item.label}</option
              >{/each}</select
          ></label
        ><Button wrap onclick={() => controller?.load(filter)}>更新</Button>
      </div>
      <p class="library-content review-note">
        見つかった契約情報を確認してください。承認したものがサブスクに表示されます。
      </p>
      {#if view.error}<p class="notice library-content" role="alert">
          {processingErrors[view.error]}<Button
            wrap
            onclick={() => controller?.load(filter)}>再読み込み</Button
          >
        </p>{/if}
      {#if view.page}<CandidatesPanel
          page={view.page}
          onSelect={(candidate) => {
            initialAction = null;
            selected = candidate;
          }}
          onDismiss={(candidate) => {
            initialAction = 'dismiss';
            selected = candidate;
          }}
        />
        <ActionGroup
          class={['library-footer', 'part-AnalysisInbox-library-footer']}
        >
          <span>確認待ち {view.page.total.toLocaleString('ja-JP')} 件</span>
          <ActionGroup class={['actions', 'part-AnalysisInbox-actions']}>
            {#if filter.offset > 0}<ActionLink
                wrap
                class={['button']}
                href={link(Math.max(0, filter.offset - 20))}>前へ</ActionLink
              >{/if}{#if filter.offset + 20 < view.page.total}<ActionLink
                wrap
                class={['button']}
                href={link(filter.offset + 20)}>次へ</ActionLink
              >{/if}
          </ActionGroup>
        </ActionGroup>
      {:else if view.status === 'loading'}<p
          class="notice library-content"
          role="status"
        >
          契約候補を読み込んでいます…
        </p>{/if}
    </LibraryPanel>
  {/if}
</section>
<Modal
  open={!!selected}
  title={selected
    ? (selected.event.service ?? 'サービス名未確認')
    : '契約候補を確認'}
  {busy}
  onclose={() => (selected = null)}
>
  {#if selected}{#key `${selected.result_id}/${selected.event_index}`}<CandidateDetail
        candidate={selected}
        {initialAction}
        {drafts}
        onDone={done}
        onBusyChange={(value) => (busy = value)}
      />{/key}{/if}
</Modal>

<style>
  .review-note {
    font-size: 12px;
    color: var(--color-muted);
    padding-bottom: 0;
  }
</style>
