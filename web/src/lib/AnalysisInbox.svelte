<script lang="ts">
  import BrandIcon from './BrandIcon.svelte';
  import Pagination from './Pagination.svelte';
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
    type CandidateFilter,
  } from './approval-api.ts';
  import { createFeed, type FeedView } from './daily-controller.ts';
  import type { MailAccount } from './mail-api.ts';
  import type { SubscriptionDrafts } from './subscriptions-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import CandidatesPanel from './CandidatesPanel.svelte';
  import ListSearchSort from './ListSearchSort.svelte';
  import {
    candidateFilter,
    candidateSorts,
    listURL,
  } from './list-search-sort.ts';
  import CandidateDetail from './CandidateDetail.svelte';
  import CandidateBulkDismiss from './CandidateBulkDismiss.svelte';
  import { loadCandidateListSelection } from './candidate-list-selection.ts';
  import {
    parseCandidateSourceURL,
    clearCandidateSourceURL,
    createCandidateSourceLink,
    type CandidateSourceView,
  } from './candidate-source-link.ts';
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
  let listSelection = $state<Candidate[]>([]),
    bulkCandidates = $state<Candidate[]>([]),
    selectingAll = $state(false),
    selectionScope = $state('');
  let selectAllAbort: AbortController | undefined;
  let view = $state<FeedView<CandidatePage>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let controller =
    $state<ReturnType<typeof createFeed<CandidatePage, CandidateFilter>>>();
  let abort: AbortController | undefined,
    disposed = false;
  let sourceController = $state<ReturnType<typeof createCandidateSourceLink>>();
  let sourceView = $state<CandidateSourceView>({
    status: 'idle',
    candidate: null,
    error: null,
  });
  let linkedSelection = false;
  const sourceTarget = $derived.by(() => {
    try {
      return { source: parseCandidateSourceURL(page.url), invalid: false };
    } catch {
      return { source: null, invalid: true };
    }
  });
  const history = $derived(
    page.url.searchParams.get('tab') === 'history' ||
      !!page.url.searchParams.get('setup_account'),
  );
  const account = $derived(page.url.searchParams.get('account_id') ?? '');
  const currentAccount = $derived(accounts.find((a) => a.id === account));
  const filter = $derived(candidateFilter(page.url));
  function link(offset = 0, id = account) {
    return listURL(page.url, {
      tab: history ? 'history' : 'attention',
      offset,
      account_id: id,
    });
  }
  function denied() {
    selected = null;
    listSelection = [];
    bulkCandidates = [];
    selectAllAbort?.abort();
    drafts.clear();
    sourceController?.dispose();
    session.onDenied();
  }
  function consumeSource() {
    if (busy) return;
    linkedSelection = false;
    if (sourceTarget.source || sourceTarget.invalid)
      void goto(clearCandidateSourceURL(page.url), {
        replace: true,
        reset: false,
      });
  }
  function closeCandidate() {
    if (busy) return;
    selected = null;
    initialAction = null;
    consumeSource();
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
    listSelection = [];
    bulkCandidates = [];
    notice = '';
    busy = false;
    consumeSource();
    void controller?.load(filter);
    void workspace?.refresh();
  }
  async function selectAllCandidates() {
    if (busy || selectingAll || bulkCandidates.length) return;
    selectAllAbort?.abort();
    const current = new AbortController();
    selectAllAbort = current;
    const currentFilter = { ...filter },
      scope = JSON.stringify(filter);
    selectingAll = true;
    notice = '';
    try {
      const rows = await loadCandidateListSelection(
        (offset) =>
          api.candidates(
            { ...currentFilter, limit: 100, offset },
            session.token,
            current.signal,
          ),
        current.signal,
      );
      if (
        !disposed &&
        !current.signal.aborted &&
        scope === JSON.stringify(filter)
      )
        listSelection = rows;
    } catch (e) {
      if (disposed || current.signal.aborted) return;
      const code = e instanceof ApiError ? e.code : 'dependency_unavailable';
      notice = processingErrors[code];
      if (code === 'authentication_required' || code === 'access_denied')
        denied();
    } finally {
      if (!disposed && selectAllAbort === current) selectingAll = false;
    }
  }
  onMount(() => {
    sourceController = createCandidateSourceLink(
      api,
      session.token,
      (next) => {
        sourceView = next;
        if (next.status === 'ready' && next.candidate) {
          if (!busy) {
            linkedSelection = true;
            initialAction = null;
            selected = next.candidate;
          }
        } else if (next.status === 'idle' && linkedSelection && !busy) {
          linkedSelection = false;
          selected = null;
        }
      },
      denied,
    );
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
      sourceController?.dispose();
      abort?.abort();
      selectAllAbort?.abort();
      drafts.clear();
    };
  });
  $effect(() => {
    const link = sourceController,
      target = sourceTarget,
      showHistory = history;
    if (link)
      untrack(
        () =>
          void link.load(showHistory || target.invalid ? null : target.source),
      );
  });
  $effect(() => {
    const next = JSON.stringify(filter);
    if (next !== selectionScope) {
      selectionScope = next;
      listSelection = [];
      selectAllAbort?.abort();
      selectingAll = false;
    }
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
      busy ||
      selectingAll ||
      listSelection.length > 0 ||
      bulkCandidates.length > 0
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
  {#if notice}<p class="notice" role="status">{notice}</p>{/if}
  {#if !history && sourceTarget.invalid}<p class="notice" role="alert">
      候補リンクの形式を確認してください。
    </p>
    <Button type="button" wrap disabled={busy} onclick={consumeSource}
      >確認一覧に戻る</Button
    >
  {:else if sourceView.status === 'loading'}<p class="notice" role="status">
      通知の確認候補を読み込んでいます…
    </p>
  {:else if sourceView.status === 'missing'}<p class="notice" role="status">
      候補は処理済みか更新されています。最新の確認一覧をご覧ください。
    </p>
    <Button type="button" wrap disabled={busy} onclick={consumeSource}
      >確認一覧に戻る</Button
    >
  {:else if sourceView.status === 'error'}<p class="notice" role="alert">
      {processingErrors[sourceView.error ?? 'dependency_unavailable']}
    </p>
    <Button
      type="button"
      wrap
      disabled={busy}
      onclick={() => sourceController?.load(sourceTarget.source)}
      >候補を再取得</Button
    >{/if}
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
                <a class="account-choice" href={link(0, item.id)}
                  ><BrandIcon
                    provider={item.provider}
                    host={item.host}
                    size="small"
                  />{item.label}</a
                >
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
      <ListSearchSort
        q={filter.q ?? ''}
        sort={filter.sort ?? 'received_desc'}
        options={candidateSorts}
        disabled={busy}
        onSearch={(q) => goto(listURL(page.url, { q }))}
        onSort={(sort) => goto(listURL(page.url, { sort }))}
        onClear={() =>
          goto(listURL(page.url, { q: '', sort: 'received_desc' }))}
      />
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
          filtered={!!filter.q}
          selectedCandidates={listSelection}
          selectionBusy={busy ||
            selectingAll ||
            sourceView.status === 'loading'}
          onSelectionChange={(rows) => {
            if (!busy && !selectingAll) listSelection = rows;
          }}
          onSelectAll={selectAllCandidates}
          onBulkDismiss={() => {
            if (listSelection.length && !busy)
              bulkCandidates = $state.snapshot(listSelection);
          }}
          onClear={() =>
            goto(listURL(page.url, { q: '', sort: 'received_desc' }))}
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
          <Pagination
            total={view.page.total}
            offset={view.page.offset}
            limit={view.page.limit}
            label="確認待ち候補のページ"
            href={(next) => link(next)}
          />
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
  onclose={closeCandidate}
>
  {#if selected}{#key `${selected.result_id}/${selected.event_index}`}<CandidateDetail
        candidate={selected}
        {initialAction}
        {drafts}
        onDone={done}
        onBusyChange={(value) => (busy = value)}
      />{/key}{/if}
</Modal>
<Modal
  open={bulkCandidates.length > 0}
  title="選択した候補を除外"
  {busy}
  onclose={() => {
    bulkCandidates = [];
  }}
>
  {#if bulkCandidates.length}<CandidateBulkDismiss
      candidates={bulkCandidates}
      onDone={done}
      onCancel={() => {
        bulkCandidates = [];
      }}
      onDenied={denied}
      onBusyChange={(value) => (busy = value)}
    />{/if}
</Modal>

<style>
  .account-choice {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .review-note {
    font-size: 12px;
    color: var(--color-muted);
    padding-bottom: 0;
  }
</style>
