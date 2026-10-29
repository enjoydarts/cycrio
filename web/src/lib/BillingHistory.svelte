<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { page } from '$app/state';
  import { beforeNavigate, goto } from '$app/navigation';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { ApiError } from './api.ts';
  import { findBillingSource } from './billing-source.ts';
  import { uuid, integer } from './daily-api.ts';
  import {
    createBillingAPI,
    billingMonth,
    type BillingEntry,
    type BillingFilter,
    type BillingPage,
    type BillingSource,
  } from './billing-api.ts';
  import {
    createBillingFeed,
    currentBillingMonth,
    type BillingDrafts,
  } from './billing-controller.ts';
  import type { FeedView } from './daily-controller.ts';
  import type { Subscription } from './subscriptions-api.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import BillingTable from './BillingTable.svelte';
  import BillingEditor from './BillingEditor.svelte';
  import Modal from './Modal.svelte';
  import './billing.css';
  let {
    subscription,
    onFrozen = () => {},
    drafts: providedDrafts,
    onDirty = () => {},
  }: {
    subscription: Subscription;
    onFrozen?: (frozen: boolean) => void;
    drafts?: BillingDrafts;
    onDirty?: (dirty: boolean) => void;
  } = $props();
  const session = getAppSession(),
    token = session.token,
    api = createBillingAPI(PUBLIC_API_BASE_URL);
  const drafts = untrack(() => providedDrafts ?? (new Map() as BillingDrafts));
  let controller:
    | ReturnType<typeof createBillingFeed<BillingPage, BillingFilter>>
    | undefined;
  let view = $state<FeedView<BillingPage>>({
      page: null,
      status: 'idle',
      error: null,
    }),
    editor = $state<{
      key: string;
      targetKey: string;
      subscription: Subscription;
      initial: BillingEntry | null;
      source: BillingSource | null;
      onclose: () => void;
      onBusyChange: (busy: boolean) => void;
    } | null>(null),
    busy = $state(false),
    sourceMessage = $state(''),
    navigationMessage = $state('');
  let handledSource = '';
  $effect(() => {
    onFrozen(busy);
  });
  let editorSequence = 0;
  let sourceSequence = 0,
    sourceAbort: AbortController | undefined,
    disposed = false;
  const filter = $derived.by(() => {
    const params = page.url.searchParams;
    let month = '';
    try {
      if (params.get('month')) month = billingMonth(params.get('month'));
    } catch {
      /* An invalid URL cannot send an invalid API filter. */
    }
    const offset = Number(params.get('offset') ?? 0);
    return {
      kind: ['invoice', 'payment', 'refund'].includes(params.get('kind') ?? '')
        ? params.get('kind')
        : 'all',
      state: ['active', 'voided'].includes(params.get('state') ?? '')
        ? params.get('state')
        : 'all',
      month,
      limit: 20,
      offset: Number.isSafeInteger(offset) && offset >= 0 ? offset : 0,
    } as BillingFilter;
  });
  const sourceQuery = $derived(
    `${page.url.searchParams.get('source_result_id') ?? ''}/${page.url.searchParams.get('source_event_index') ?? ''}`,
  );
  function link(patch: Partial<BillingFilter>) {
    const next = { ...filter, ...patch };
    return `/subscriptions/${subscription.id}?${new URLSearchParams({ tab: 'history', ...(next.kind !== 'all' ? { kind: next.kind } : {}), ...(next.state !== 'all' ? { state: next.state } : {}), ...(next.month ? { month: next.month } : {}), offset: String(next.offset) })}`;
  }
  function open(
    initial: BillingEntry | null = null,
    source: BillingSource | null = null,
  ) {
    if (busy || (initial && initial.subscription_id !== subscription.id))
      return false;
    sourceSequence++;
    sourceAbort?.abort();
    const targetKey = `${subscription.id}/${source ? `source/${source.result_id}/${source.event_index}` : initial ? `event/${initial.id}` : 'manual'}`;
    if (editor?.targetKey === targetKey) return true;
    const key = `${targetKey}/${++editorSequence}`;
    editor = {
      key,
      targetKey,
      subscription,
      initial,
      source,
      onclose: () => close(key),
      onBusyChange: (value) => {
        if (editor?.key === key) busy = value;
      },
    };
    navigationMessage = '';
    return true;
  }
  function openRequestedSource() {
    if (busy) return;
    if (sourceQuery === '/') {
      sourceSequence++;
      sourceAbort?.abort();
      const leftSourceURL = handledSource !== '';
      handledSource = '';
      if (leftSourceURL && untrack(() => editor?.source)) editor = null;
      return;
    }
    if (handledSource === sourceQuery) return;
    handledSource = sourceQuery;
    const [result, index] = sourceQuery.split('/');
    try {
      if (!/^\d+$/.test(index)) throw new Error();
      void openSource({
        result_id: uuid(result),
        event_index: integer(Number(index), 0, 31),
      });
    } catch {
      editor = null;
      sourceMessage = '根拠の指定を確認できません。';
    }
  }
  async function openSource(reference: {
    result_id: string;
    event_index: number;
  }) {
    if (busy || disposed) return;
    sourceAbort?.abort();
    sourceAbort = new AbortController();
    const seq = ++sourceSequence,
      signal = sourceAbort.signal;
    sourceMessage = '記録済みの履歴を確認しています…';
    try {
      const entry = await findBillingSource(
        api,
        subscription.id,
        reference,
        token,
        signal,
      );
      if (disposed || signal.aborted || seq !== sourceSequence) return;
      sourceMessage = '';
      if (entry) {
        open(entry);
        return;
      }
      const source = subscription.sources.find(
        (s) =>
          s.result_id === reference.result_id &&
          s.event_index === reference.event_index,
      );
      if (source) open(null, source);
      else {
        editor = null;
        sourceMessage =
          'この根拠は契約に紐付いていません。契約の元の AI 抽出情報を確認してください。根拠は自動で追加しません。';
      }
    } catch (error) {
      if (disposed || signal.aborted || seq !== sourceSequence) return;
      if (
        error instanceof ApiError &&
        ['authentication_required', 'access_denied'].includes(error.code)
      ) {
        sourceSequence++;
        sourceAbort?.abort();
        editor = null;
        sourceMessage = '';
        drafts.clear();
        session.onDenied();
        return;
      }
      sourceMessage =
        '記録済みの履歴を確認できません。再読み込みしてから操作してください。';
      handledSource = '';
    }
  }
  function close(key: string) {
    if (busy || editor?.key !== key) return;
    editor = null;
    void controller?.load(filter);
  }
  beforeNavigate(({ cancel }) => {
    if (busy) {
      cancel();
      navigationMessage =
        '保存中の画面移動を停止しました。受付状況を確認してから、もう一度移動してください。';
    } else navigationMessage = '';
  });
  onMount(() => {
    controller = createBillingFeed(
      (filter, token, signal) =>
        api.list(subscription.id, filter, token, signal),
      token,
      (next) => (view = next),
      () => {
        drafts.clear();
        editor = null;
        sourceMessage = '';
        session.onDenied();
      },
    );
    void controller.load(filter);
    openRequestedSource();
    return () => {
      disposed = true;
      sourceSequence++;
      sourceAbort?.abort();
      controller?.dispose();
      if (!providedDrafts) drafts.clear();
    };
  });
  $effect(() => {
    void controller?.load(filter);
  });
  $effect(() => {
    sourceQuery;
    openRequestedSource();
  });
</script>

<section class="billing" aria-labelledby="billing-history-heading">
  <header class="titlebar">
    <div>
      <h2 id="billing-history-heading">請求・支払い履歴</h2>
      <p>
        メールからの自動記録と、本人が保存した請求・支払い・返金です。契約の料金や次回請求日は変更しません。
      </p>
    </div>
    <button class="primary" onclick={() => open()}>＋ 履歴を記録</button>
  </header>
  <div class="actions">
    <a
      class="button"
      href={`/subscriptions/summary?month=${filter.month || currentBillingMonth()}&subscription_id=${subscription.id}`}
      >このサブスクの月次集計</a
    >
  </div>
  {#if sourceMessage}<p class="notice" role="alert">{sourceMessage}</p>{/if}
  <form class="toolbar" onsubmit={(event) => event.preventDefault()}>
    <div class="filters">
      <label
        >種別<select
          value={filter.kind}
          onchange={(event) =>
            goto(
              link({
                kind: event.currentTarget.value as BillingFilter['kind'],
                offset: 0,
              }),
            )}
          ><option value="all">すべての種別</option><option value="invoice"
            >請求</option
          ><option value="payment">支払い</option><option value="refund"
            >返金</option
          ></select
        ></label
      ><label
        >状態<select
          value={filter.state}
          onchange={(event) =>
            goto(
              link({
                state: event.currentTarget.value as BillingFilter['state'],
                offset: 0,
              }),
            )}
          ><option value="all">すべての状態</option><option value="active"
            >有効</option
          ><option value="voided">無効</option></select
        ></label
      ><label
        >明細の月<input
          type="month"
          value={filter.month}
          onchange={(event) =>
            goto(link({ month: event.currentTarget.value, offset: 0 }))}
        /></label
      ><button
        type="button"
        onclick={() =>
          goto(link({ kind: 'all', state: 'all', month: '', offset: 0 }))}
        >絞り込みを解除</button
      >
    </div>
  </form>
  {#if view.status === 'idle' || view.status === 'loading'}<p
      class="notice"
      role="status"
    >
      履歴を読み込んでいます…
    </p>{:else if view.error}<div class="notice" role="alert">
      <p>{processingErrors[view.error]}</p>
      <button onclick={() => controller?.load(filter)}>履歴を再読み込み</button>
    </div>{:else if view.page}
    {#if view.page.events.length}<BillingTable
        entries={view.page.events}
        onselect={(entry) => open(entry)}
      />{:else if filter.kind !== 'all' || filter.state !== 'all' || filter.month}<p
        class="empty"
      >
        絞り込みに一致する履歴はありません。
      </p>{:else}<p class="empty">
        履歴はまだありません。確認した明細を記録してください。
      </p>{/if}
    <nav class="pager" aria-label="履歴のページ">
      <a
        class="button"
        href={link({ offset: Math.max(0, filter.offset - 20) })}
        aria-disabled={filter.offset === 0}
        onclick={(event) => {
          if (filter.offset === 0) event.preventDefault();
        }}>前のページ</a
      ><span
        >全 {view.page.total} 件{view.page.events.length
          ? ` · ${filter.offset + 1}〜${filter.offset + view.page.events.length} 件`
          : ''}</span
      ><a
        class="button"
        href={link({ offset: filter.offset + 20 })}
        aria-disabled={filter.offset + 20 >= view.page.total}
        onclick={(event) => {
          if (filter.offset + 20 >= view.page!.total) event.preventDefault();
        }}>次のページ</a
      >
    </nav>
  {/if}
  {#if subscription.sources.length}<details class="section">
      <summary>契約に紐付いた AI 抽出から履歴に記録</summary>
      <p class="muted">
        抽出内容を確認してから記録します。すでに履歴へ登録されている根拠は登録先を開きます。
      </p>
      <div class="billing-source-picker">
        {#each subscription.sources as source}<button
            onclick={() => openSource(source)}
            >{source.original_event.service} · {source.original_event
              .billed_at ?? '日付不明'} · イベント {source.event_index +
              1}</button
          >{/each}
      </div>
    </details>{/if}
  {#if editor}{#key editor.key}{@const target = untrack(() => editor!)}<Modal
        open
        title={target.initial ? '履歴の詳細・編集' : '履歴を記録'}
        {busy}
        onclose={target.onclose}
        >{#if navigationMessage}<p class="notice" role="status">
            {navigationMessage}
          </p>{/if}<BillingEditor
          subscription={target.subscription}
          initial={target.initial}
          source={target.source}
          {drafts}
          onBusyChange={target.onBusyChange}
          {onDirty}
          onSaved={target.onclose}
        /></Modal
      >{/key}{/if}
</section>
