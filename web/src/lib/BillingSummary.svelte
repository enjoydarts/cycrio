<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { createBillingAPI, billingMonth } from './billing-api.ts';
  import { createSubscriptionsAPI } from './subscriptions-api.ts';
  import {
    createBillingSummaryController,
    currentBillingMonth,
    initialBillingSummaryView,
    type SummaryFilter,
  } from './billing-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import BillingSummaryTable from './BillingSummaryTable.svelte';
  import Modal from './Modal.svelte';
  import './daily.css';
  import './billing.css';
  const session = getAppSession(),
    token = session.token,
    api = createBillingAPI(PUBLIC_API_BASE_URL),
    subscriptions = createSubscriptionsAPI(PUBLIC_API_BASE_URL);
  let controller: ReturnType<typeof createBillingSummaryController> | undefined;
  let view = $state(initialBillingSummaryView()),
    picking = $state(false),
    search = $state('');
  const filter = $derived.by(() => {
    let month = currentBillingMonth();
    try {
      if (page.url.searchParams.get('month'))
        month = billingMonth(page.url.searchParams.get('month'));
    } catch {
      /* Retain a valid default for malformed URLs. */
    }
    return {
      month,
      subscription_id: page.url.searchParams.get('subscription_id') || null,
    };
  });
  function link(patch: Partial<SummaryFilter>) {
    const next = { ...filter, ...patch };
    return `/subscriptions/summary?${new URLSearchParams({ month: next.month, ...(next.subscription_id ? { subscription_id: next.subscription_id } : {}) })}`;
  }
  function choose(id: string | null) {
    picking = false;
    void goto(link({ subscription_id: id }));
  }
  function openPicker() {
    picking = true;
    search = '';
    void controller?.search('', 0);
  }
  onMount(() => {
    controller = createBillingSummaryController(
      api,
      subscriptions,
      token,
      (next) => {
        view = next;
        if (next.status === 'denied' || next.status === 'disposed') {
          search = '';
          picking = false;
        }
      },
      session.onDenied,
    );
    void controller.load(filter);
    return () => controller?.dispose();
  });
  $effect(() => {
    void controller?.load(filter);
  });
</script>

<section class="daily billing" aria-labelledby="billing-summary-heading">
  <a class="button" href="/subscriptions">← サブスク一覧</a>
  <header class="titlebar">
    <div>
      <h1 id="billing-summary-heading">確認済み明細の集計</h1>
      <p>記録した支払い・返金を月ごとに確認します。</p>
    </div>
  </header>
  <div class="toolbar">
    <label
      >集計月（日本時間）<input
        type="month"
        required
        value={filter.month}
        onchange={(event) => {
          if (event.currentTarget.validity.valid && event.currentTarget.value)
            void goto(link({ month: event.currentTarget.value }));
        }}
      /></label
    ><button type="button" onclick={openPicker}>サブスクを選択</button>
  </div>
  <div class="billing-scope">
    <div>
      <strong>集計対象</strong>
      <p>
        {filter.subscription_id
          ? (view.selected?.service ?? '選択したサブスクを確認中…')
          : '自分のすべてのサブスク（終了済みを含む）'}
      </p>
      {#if view.selected}<p class="muted">
          {view.selected.plan ?? 'プラン不明'} · {view.selected.status ===
          'active'
            ? '利用中'
            : '終了済み'}
        </p>
        <p class="billing-id">{view.selected.id}</p>{/if}
    </div>
    {#if filter.subscription_id}<button
        type="button"
        onclick={() => choose(null)}>選択を解除してすべてを集計</button
      ><a
        class="button"
        href={`/subscriptions/${filter.subscription_id}?tab=history&month=${filter.month}`}
        >このサブスクの履歴</a
      >{/if}
  </div>
  {#if view.status === 'idle' || view.status === 'loading'}<p
      role="status"
      class="notice"
    >
      確認済み明細を集計しています…
    </p>{:else if view.error}<div class="notice" role="alert">
      <p>{processingErrors[view.error]}</p>
      <button onclick={() => controller?.load(filter)}>集計を再読み込み</button>
    </div>{:else if view.summary}<BillingSummaryTable
      summary={view.summary}
    />{/if}
  {#if picking}<Modal
      open
      title="集計するサブスクを選択"
      onclose={() => (picking = false)}
      ><div class="daily billing">
        <p class="muted">
          終了済みの契約も検索できます。選択を解除すると、すべてのサブスクが集計対象になります。
        </p>
        <button onclick={() => choose(null)}>すべてのサブスクを集計</button>
        <form
          class="toolbar"
          onsubmit={(event) => {
            event.preventDefault();
            void controller?.search(search, 0);
          }}
        >
          <label class="search"
            >サービス・プランを検索<input
              type="search"
              bind:value={search}
            /></label
          ><button type="submit">サブスクを検索</button>
        </form>
        {#if view.pickerLoading}<p role="status">
            サブスクを読み込んでいます…
          </p>{:else if view.pickerError}<p role="alert" class="notice">
            {processingErrors[view.pickerError]}
          </p>
          <button onclick={() => controller?.search(search, 0)}
            >検索を再試行</button
          >{:else if view.contracts}{#if view.contracts.subscriptions.length}<table
            >
              <thead
                ><tr><th>サービス / プラン</th><th>状態</th><th>選択</th></tr
                ></thead
              ><tbody
                >{#each view.contracts.subscriptions as s (s.id)}<tr
                    ><td
                      ><strong>{s.service}</strong><span class="secondary"
                        >{s.plan ?? 'プラン不明'}</span
                      ><span class="secondary billing-id">{s.id}</span></td
                    ><td data-label="状態"
                      >{s.status === 'active' ? '利用中' : '終了済み'}</td
                    ><td
                      ><button onclick={() => choose(s.id)}
                        >このサブスクを集計</button
                      ></td
                    ></tr
                  >{/each}</tbody
              >
            </table>{:else}<p class="empty">
              一致するサブスクはありません。
            </p>{/if}
          <nav class="pager" aria-label="サブスク選択のページ">
            <button
              disabled={view.contracts.offset === 0}
              onclick={() =>
                controller?.search(
                  view.query,
                  Math.max(0, view.contracts!.offset - 20),
                )}>前のサブスク</button
            ><span>全 {view.contracts.total} 件</span><button
              disabled={view.contracts.offset + 20 >= view.contracts.total}
              onclick={() =>
                controller?.search(view.query, view.contracts!.offset + 20)}
              >次のサブスク</button
            >
          </nav>{/if}
      </div></Modal
    >{/if}
</section>
