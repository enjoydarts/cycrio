<script lang="ts">
  import ActionLink from './ActionLink.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto, beforeNavigate } from '$app/navigation';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { getWorkspace } from './workspace-context.ts';
  import {
    createDraftNavigation,
    hasDraftChanges,
  } from './draft-navigation.ts';
  import type { ScheduleDrafts } from './schedule-controller.ts';
  import type { BillingDrafts } from './billing-controller.ts';
  import {
    createSubscriptionsAPI,
    type SubscriptionFilter,
    type SubscriptionPage,
  } from './subscriptions-api.ts';
  import {
    createSubscriptionList,
    type SubscriptionDrafts,
    type EditorView,
  } from './subscriptions-controller.ts';
  import {
    createOverviewAPI,
    createOverviewController,
    type OverviewReport,
  } from './overview-api.ts';
  import type { FeedView } from './daily-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import Modal from './Modal.svelte';
  import SubscriptionForm from './SubscriptionForm.svelte';
  import SubscriptionDetail from './SubscriptionDetail.svelte';
  import SubscriptionDrawer from './SubscriptionDrawer.svelte';
  import SubscriptionListPanel from './SubscriptionListPanel.svelte';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import './daily.css';
  const session = getAppSession(),
    workspace = getWorkspace(),
    drafts: SubscriptionDrafts = new Map(),
    scheduleDrafts: ScheduleDrafts = new Map(),
    billingDrafts: BillingDrafts = new Map();
  let controller: ReturnType<typeof createSubscriptionList> | undefined;
  let overviewController:
    ReturnType<typeof createOverviewController> | undefined;
  let fallback = $state<FeedView<OverviewReport>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let view = $state<FeedView<SubscriptionPage>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let adding = $state(false),
    selected = $state<string | null>(null),
    frozen = $state(false),
    dirty = $state(false),
    formKey = $state(0),
    search = $state(''),
    layout = $state<'list' | 'cards'>('list'),
    navigationMessage = $state('');
  let leaveTo = $state('');
  const hasUnsaved = () =>
    dirty ||
    hasDraftChanges(
      drafts.values(),
      scheduleDrafts.values(),
      billingDrafts.values(),
    );
  function clearDrafts() {
    drafts.clear();
    scheduleDrafts.clear();
    billingDrafts.clear();
    dirty = false;
  }
  const navigation = createDraftNavigation(
    hasUnsaved,
    () => frozen,
    clearDrafts,
    goto,
    (target) => (leaveTo = target),
  );
  const filter = $derived.by((): SubscriptionFilter => {
    const p = page.url.searchParams,
      offset = Number(p.get('offset') ?? 0);
    return {
      q: p.get('q') ?? '',
      status: ['active', 'ended', 'unknown'].includes(p.get('status') ?? '')
        ? (p.get('status') as SubscriptionFilter['status'])
        : 'all',
      sort: ['service', 'updated'].includes(p.get('sort') ?? '')
        ? (p.get('sort') as SubscriptionFilter['sort'])
        : 'next_billing',
      account_id: p.get('account_id') || undefined,
      limit: 20,
      offset: Number.isSafeInteger(offset) && offset >= 0 ? offset : 0,
    };
  });
  const overview = $derived(
    workspace
      ? workspace.view.overview.status === 'ready'
        ? workspace.view.overview.value
        : null
      : fallback.status === 'ready'
        ? fallback.page
        : null,
  );
  const counts = $derived(
    !filter.account_id && !filter.q ? overview?.subscriptions : undefined,
  );
  const account = $derived(
    workspace?.view.accounts.value?.find((x) => x.id === filter.account_id),
  );
  function link(patch: Partial<SubscriptionFilter>) {
    const f = { ...filter, ...patch };
    const params = new URLSearchParams({
      q: f.q,
      status: f.status,
      sort: f.sort,
      offset: String(f.offset),
    });
    if (f.account_id) params.set('account_id', f.account_id);
    return `/subscriptions?${params}`;
  }
  const importURL = $derived(
    `/import${filter.account_id ? `?account_id=${encodeURIComponent(filter.account_id)}` : ''}`,
  );
  function refresh() {
    void controller?.load(filter);
    if (workspace) void workspace.refresh();
    else void overviewController?.load();
  }
  function editorState(
    next: Pick<EditorView, 'status' | 'uncertain' | 'dirty'>,
  ) {
    frozen = next.status === 'saving' || next.uncertain;
    dirty = next.dirty;
  }
  function close() {
    if (frozen) return;
    adding = false;
    selected = null;
    dirty = false;
  }
  function select(id: string) {
    if (frozen) return;
    selected = id;
    dirty = false;
  }
  beforeNavigate(({ cancel, to }) => {
    if (!navigation.request(page.url.href, to?.url.href)) {
      cancel();
      if (frozen)
        navigationMessage = '保存の受付状況を確認してから移動してください。';
    }
  });
  onMount(() => {
    controller = createSubscriptionList(
      createSubscriptionsAPI(PUBLIC_API_BASE_URL),
      session.token,
      (next) => (view = next),
      () => {
        clearDrafts();
        session.onDenied();
      },
    );
    void controller.load(filter);
    if (!workspace) {
      overviewController = createOverviewController(
        createOverviewAPI(PUBLIC_API_BASE_URL),
        session.token,
        (next) => (fallback = next),
        session.onDenied,
      );
      void overviewController.load();
    }
    return () => {
      controller?.dispose();
      overviewController?.dispose();
      clearDrafts();
    };
  });
  $effect(() => {
    search = filter.q;
    void controller?.load(filter);
  });
  $effect(() => {
    if (
      view.status === 'ready' &&
      view.page &&
      !view.page.subscriptions.length &&
      filter.offset > 0 &&
      !selected &&
      !frozen
    )
      void goto(
        link({
          offset:
            Math.max(0, Math.ceil(view.page.total / filter.limit) - 1) *
            filter.limit,
        }),
      );
  });
</script>

<svelte:window
  onbeforeunload={(event) => {
    if (frozen || hasUnsaved()) {
      event.preventDefault();
      event.returnValue = '';
    }
  }}
/>
<section class="library daily library-page">
  <LibraryHeader
    title={account ? `${account.label} のサブスク` : 'サブスクリプション'}
    eyebrow="MY LIBRARY"
    description="メールから見つけたサブスクを、ひとつに。"
    >{#snippet action()}<ActionLink
        wrap
        variant="primary"
        class={['primary button']}
        href={importURL}>＋ メールから取り込む</ActionLink
      >{/snippet}</LibraryHeader
  >
  {#if navigationMessage && frozen}<p class="notice" role="status">
      {navigationMessage}
    </p>{/if}
  {#if overview && !filter.account_id}<div class="summary">
      <div class="metric">
        <span>登録したサブスク</span><strong
          >{overview.subscriptions.total}<small>件</small></strong
        >
        <p>接続したメールをまとめて表示</p>
      </div>
      <div class="metric">
        <span>確認が必要なメール</span><strong class="attention"
          >{overview.attention_messages}<small>件</small></strong
        >
        <p><a href="/analysis?tab=attention">解析結果を確認する →</a></p>
      </div>
    </div>{/if}
  <LibraryPanel>
    <div class="panel-heading">
      <nav class="library-tabs" aria-label="サブスクの状態">
        {#each [['all', 'すべて'], ['active', '利用中'], ['ended', '終了済み'], ['unknown', '状態不明']] as [value, label]}<ActionLink
            wrap
            variant="tab"
            href={link({
              status: value as SubscriptionFilter['status'],
              offset: 0,
            })}
            aria-current={filter.status === value ? 'page' : undefined}
            >{label}{#if counts}<span
                >{counts[
                  value === 'all'
                    ? 'total'
                    : (value as 'active' | 'ended' | 'unknown')
                ]}</span
              >{/if}</ActionLink
          >{/each}
      </nav>
      <Button
        wrap
        class={['manual', 'part-Subscriptions-manual']}
        disabled={frozen}
        onclick={() => {
          adding = true;
          selected = null;
        }}>手動で追加</Button
      >
    </div>
    <form
      class="library-toolbar"
      onsubmit={(event) => {
        event.preventDefault();
        void goto(link({ q: search, offset: 0 }));
      }}
    >
      <label class="search"
        ><span class="sr">サービス・プランを検索</span><input
          type="search"
          placeholder="サービス名やプランを検索"
          bind:value={search}
        /></label
      ><Button wrap type="submit">検索</Button><label
        ><span class="sr">並び順</span><select
          value={filter.sort}
          onchange={(event) =>
            goto(
              link({
                sort: event.currentTarget.value as SubscriptionFilter['sort'],
                offset: 0,
              }),
            )}
          ><option value="next_billing">次回の請求日順</option><option
            value="service">サービス名順</option
          ><option value="updated">更新順</option></select
        ></label
      >
      <div class="view-switch" aria-label="表示方法">
        <Button
          wrap
          variant="tab"
          type="button"
          aria-label="一覧表示"
          aria-pressed={layout === 'list'}
          onclick={() => (layout = 'list')}>☰</Button
        ><Button
          wrap
          variant="tab"
          type="button"
          aria-label="カード表示"
          aria-pressed={layout === 'cards'}
          onclick={() => (layout = 'cards')}>▦</Button
        >
      </div>
    </form>
    {#if view.status === 'loading'}<p role="status" class="empty">
        サブスクを読み込んでいます…
      </p>{:else if view.error}<div class="empty" role="alert">
        <p>{processingErrors[view.error]}</p>
        <Button wrap onclick={refresh}>再読み込み</Button>
      </div>{:else if view.page}{#if view.page.subscriptions.length}<SubscriptionListPanel
          items={view.page.subscriptions}
          {layout}
          onSelect={select}
          disabled={frozen}
        />{:else}<div class="empty">
          <h2>
            {filter.q || filter.status !== 'all'
              ? '条件に一致するサブスクはありません'
              : 'サブスクを見つけましょう'}
          </h2>
          <p>
            サービス名や状態を変更するか、メールから契約情報を取り込めます。
          </p>
          <ActionLink
            wrap
            class={['button']}
            href={filter.q || filter.status !== 'all'
              ? link({ q: '', status: 'all', offset: 0 })
              : importURL}
            >{filter.q || filter.status !== 'all'
              ? '条件を解除する'
              : 'メールから取り込む'}</ActionLink
          >
        </div>{/if}
      <footer class="list-footer">
        <span
          >全 {view.page.total} 件{#if view.page.subscriptions.length}
            · {view.page.offset + 1}〜{view.page.offset +
              view.page.subscriptions.length} 件{/if}</span
        >
        <div>
          {#if filter.offset > 0}<a
              href={link({ offset: Math.max(0, filter.offset - 20) })}>前へ</a
            >{/if}{#if filter.offset + 20 < view.page.total}<a
              href={link({ offset: filter.offset + 20 })}>次へ</a
            >{/if}
        </div>
      </footer>{/if}
  </LibraryPanel>
  <div class="footnote">
    <Button
      wrap
      disabled={view.status === 'loading' || frozen}
      onclick={refresh}>一覧を更新</Button
    >
  </div>
</section>
<Modal
  open={!!leaveTo}
  title="未保存の編集があります"
  busy={frozen}
  onclose={() => navigation.stay()}
  ><p>この画面を離れると、保持している下書きは破棄されます。</p>
  <ActionGroup class={['actions', 'part-Subscriptions-actions']}>
    <Button
      wrap
      type="button"
      disabled={frozen}
      onclick={() => navigation.stay()}>編集を続ける</Button
    ><Button
      wrap
      type="button"
      disabled={frozen}
      onclick={() => navigation.discardAndLeave()}>破棄して移動</Button
    >
  </ActionGroup></Modal
>
<SubscriptionDrawer open={selected !== null} busy={frozen} onclose={close}
  >{#if selected}{#key selected}<SubscriptionDetail
        id={selected}
        embedded
        {drafts}
        {scheduleDrafts}
        {billingDrafts}
        onFrozen={editorState}
        onSaved={refresh}
        onDeleted={() => {
          selected = null;
          frozen = false;
          dirty = false;
          refresh();
        }}
      />{/key}{#if dirty}<p class="draft-note">
        入力は下書きとして保持されます。
      </p>{/if}{/if}</SubscriptionDrawer
>
{#if adding}<Modal
    open={adding}
    title="サブスクを追加"
    busy={frozen}
    onclose={close}
    >{#key formKey}<SubscriptionForm
        {drafts}
        onFrozen={editorState}
        onSaved={() => {
          adding = false;
          frozen = false;
          dirty = false;
          formKey++;
          refresh();
        }}
      />{/key}{#if dirty}<p>
        閉じても入力は下書きとして保持されます。
      </p>{/if}</Modal
  >{/if}

<style>
  .library {
    max-width: 1280px;
    margin: 0 auto;
  }
  .summary {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    border: 1px solid var(--color-border);
    border-radius: 12px;
    background: var(--color-surface);
    padding: 24px 0;
    margin-bottom: 28px;
  }
  .metric {
    padding: 0 26px;
    border-right: 1px solid var(--color-border);
    min-width: 0;
  }
  .metric:last-child {
    border: 0;
  }
  .metric > span {
    font-size: 12px;
    color: var(--color-muted);
  }
  .metric strong {
    display: block;
    font-size: 28px;
    font-weight: 600;
    margin: 12px 0 9px;
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
  }
  .metric small {
    font-size: 12px;
    font-weight: 400;
    margin-left: 6px;
    color: var(--color-muted);
  }
  .metric p {
    font-size: 10px;
    margin: 0;
    color: var(--color-muted);
  }
  .metric a {
    color: inherit;
  }
  .attention {
    color: #c98538;
  }
  .panel-heading {
    display: flex;
    justify-content: space-between;
    padding: 0;
    gap: 12px;
    border-bottom: 1px solid var(--color-border);
  }
  nav {
    display: flex;
    gap: 22px;
    min-width: 0;
    overflow: auto;
  }
  nav :global(.ui-tab) {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--color-muted);
    text-decoration: none;
    padding: 20px 0 17px;
    white-space: nowrap;
    border-bottom: 2px solid transparent;
    font-size: 12px;
  }
  nav :global(.ui-tab[aria-current]) {
    color: var(--color-action);
    border-color: var(--color-action);
  }
  nav span {
    font-size: 10px;
    background: var(--color-surface-soft);
    padding: 2px 5px;
    border-radius: 5px;
  }
  :global(.part-Subscriptions-manual) {
    border: 0;
    background: transparent;
    font-size: 11px;
    color: var(--color-muted);
    white-space: nowrap;
    margin-right: 22px;
  }
  .library-toolbar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 18px 22px;
  }
  .search {
    flex: 1;
    max-width: 390px;
  }
  .search input {
    width: 100%;
  }
  .view-switch {
    display: flex;
    margin-left: auto;
  }
  .list-footer {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 16px 22px;
    border-top: 1px solid var(--color-border);
    font-size: 11px;
    color: var(--color-muted);
  }
  .list-footer div {
    display: flex;
    gap: 20px;
  }
  .footnote {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 16px;
    margin-top: 18px;
    font-size: 11px;
    color: var(--color-muted);
  }
  .empty {
    text-align: center;
    padding: 56px 22px;
  }
  .empty h2 {
    font-size: 18px;
  }
  .empty p {
    color: var(--color-muted);
    font-size: 12px;
  }
  .draft-note {
    color: var(--color-muted);
    font-size: 12px;
  }
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
  @media (max-width: 650px) {
    .summary {
      padding: 18px 0;
      margin-bottom: 22px;
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    .metric {
      padding: 0 12px;
    }
    .metric > span {
      font-size: 9px;
    }
    .metric strong {
      font-size: 19px;
    }
    .metric p {
      font-size: 9px;
      line-height: 1.6;
    }
    .panel-heading {
      padding: 0;
    }
    nav {
      gap: 15px;
    }
    nav :global(.ui-tab) {
      font-size: 10px;
      padding: 16px 0;
    }
    :global(.part-Subscriptions-manual) {
      font-size: 10px;
    }
    .library-toolbar {
      flex-wrap: wrap;
      padding: 14px;
      gap: 8px;
    }
    .search {
      flex-basis: calc(100% - 60px);
      max-width: none;
    }
    .footnote {
      font-size: 10px;
    }
  }
</style>
