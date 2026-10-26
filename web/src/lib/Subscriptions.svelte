<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { createAnalysisAPI, type AnalysisPage } from './analysis-api.ts';
  import { createAnalysisController } from './analysis-controller.ts';
  import { getAppSession } from './app-context.ts';
  import {
    createSubscriptionsAPI,
    type SubscriptionFilter,
    type SubscriptionPage,
  } from './subscriptions-api.ts';
  import { createSubscriptionList } from './subscriptions-controller.ts';
  import type { FeedView } from './daily-controller.ts';
  import { cycle, money } from './daily-text.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import Modal from './Modal.svelte';
  import SubscriptionForm from './SubscriptionForm.svelte';
  import './daily.css';
  const session = getAppSession(),
    token = session.token,
    api = createSubscriptionsAPI(PUBLIC_API_BASE_URL);
  let controller: ReturnType<typeof createSubscriptionList> | undefined;
  let pendingController:
    ReturnType<typeof createAnalysisController> | undefined;
  let pending = $state<FeedView<AnalysisPage>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let view = $state<FeedView<SubscriptionPage>>({
      page: null,
      status: 'idle',
      error: null,
    }),
    adding = $state(false),
    saving = $state(false),
    formKey = $state(0),
    search = $state('');
  const filter = $derived.by(() => {
    const p = page.url.searchParams;
    return {
      q: p.get('q') ?? '',
      status: ['active', 'ended'].includes(p.get('status') ?? '')
        ? p.get('status')
        : 'all',
      sort: ['service', 'updated'].includes(p.get('sort') ?? '')
        ? p.get('sort')
        : 'next_billing',
      limit: 20,
      offset: /^\d+$/.test(p.get('offset') ?? '') ? Number(p.get('offset')) : 0,
    } as SubscriptionFilter;
  });
  function link(patch: Partial<SubscriptionFilter>) {
    const f = { ...filter, ...patch };
    return `/subscriptions?${new URLSearchParams({ q: f.q, status: f.status, sort: f.sort, offset: String(f.offset) })}`;
  }
  onMount(() => {
    pendingController = createAnalysisController(
      createAnalysisAPI(PUBLIC_API_BASE_URL),
      token,
      (next) => (pending = next),
      session.onDenied,
    );
    void pendingController.load({
      account_id: '',
      state: 'related',
      review: 'pending',
      limit: 1,
      offset: 0,
    });
    controller = createSubscriptionList(
      api,
      token,
      (next) => (view = next),
      session.onDenied,
    );
    void controller.load(filter);
    return () => {
      controller?.dispose();
      pendingController?.dispose();
    };
  });
  $effect(() => {
    search = filter.q;
    void controller?.load(filter);
  });
</script>

<section class="daily">
  <header class="titlebar">
    <div>
      <h1>サブスク</h1>
      <p>確認した契約と、次回の請求を管理します。</p>
    </div>
    <button class="primary" onclick={() => (adding = true)}
      >＋ サブスクを追加</button
    >
  </header>
  {#if pending.page && pending.page.total > 0}<aside class="notice">
      <strong>確認待ちの解析結果 {pending.page.total} 件（メール単位）</strong>
      <div class="actions"><a href="/analysis">確認する →</a></div>
    </aside>{/if}
  <form
    class="toolbar"
    onsubmit={(e) => {
      e.preventDefault();
      void goto(link({ q: search, offset: 0 }));
    }}
  >
    <label class="search"
      >サービス・プランを検索<input type="search" bind:value={search} /></label
    >
    <div class="filters">
      <label
        >状態<select
          value={filter.status}
          onchange={(e) =>
            goto(
              link({
                q: search,
                status: e.currentTarget.value as SubscriptionFilter['status'],
                offset: 0,
              }),
            )}
          ><option value="all">すべて</option><option value="active"
            >利用中</option
          ><option value="ended">終了済み</option></select
        ></label
      ><label
        >並び順<select
          value={filter.sort}
          onchange={(e) =>
            goto(
              link({
                q: search,
                sort: e.currentTarget.value as SubscriptionFilter['sort'],
                offset: 0,
              }),
            )}
          ><option value="next_billing">次回請求日</option><option
            value="service">サービス名</option
          ><option value="updated">更新順</option></select
        ></label
      ><button type="submit">検索</button>
    </div>
  </form>
  {#if view.status === 'loading'}<p role="status" class="notice">
      サブスクを読み込んでいます…
    </p>{:else if view.error}<div class="notice" role="alert">
      <p>{processingErrors[view.error]}</p>
      <button onclick={() => controller?.load(filter)}>再読み込み</button>
    </div>{:else if view.page}
    {#if view.page.subscriptions.length}<table>
        <thead
          ><tr
            ><th>サービス / プラン</th><th>料金 / 周期</th><th>次回請求日</th
            ><th>状態</th><th>詳細</th></tr
          ></thead
        ><tbody
          >{#each view.page.subscriptions as item (item.id)}<tr
              ><td
                ><strong>{item.service}</strong><span class="secondary"
                  >{item.plan ?? 'プラン不明'}</span
                ></td
              ><td data-label="料金" class="money"
                >{money(item.amount, item.currency)}<span class="secondary"
                  >{cycle(item.interval_unit, item.interval_count)}</span
                ></td
              ><td data-label="次回請求日"
                >{item.next_billing_date ?? '不明'}</td
              ><td data-label="状態"
                ><span class="badge"
                  >{item.status === 'active' ? '利用中' : '終了済み'}</span
                ></td
              ><td
                ><a class="button" href={`/subscriptions/${item.id}`}
                  >詳細を見る</a
                ></td
              ></tr
            >{/each}</tbody
        >
      </table>
    {:else}<div class="empty">
        <h2>
          {filter.q || filter.status !== 'all'
            ? '条件に一致するサブスクはありません'
            : 'サブスクはまだありません'}
        </h2>
        <p>契約を追加するか、メールを連携して解析結果から登録できます。</p>
        <div class="actions">
          <a href="/settings/mail">メールを連携する</a><a href="/analysis"
            >メール解析を開く</a
          >
        </div>
      </div>{/if}
    <div class="pager">
      <span
        >全 {view.page.total} 件{#if view.page.total}
          · {view.page.offset + 1}〜{view.page.offset +
            view.page.subscriptions.length} 件{/if}</span
      >
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
  open={adding}
  title="サブスクを追加"
  busy={saving}
  onclose={() => (adding = false)}
  >{#key formKey}<SubscriptionForm
      onBusyChange={(value) => (saving = value)}
      onSaved={() => {
        adding = false;
        formKey++;
        void controller?.load(filter);
      }}
    />{/key}</Modal
>
