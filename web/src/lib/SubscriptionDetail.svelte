<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import {
    createSubscriptionsAPI,
    type Subscription,
  } from './subscriptions-api.ts';
  import { createFeed, type FeedView } from './daily-controller.ts';
  import { cycle, money } from './daily-text.ts';
  import { processingDate, processingErrors } from './mail-processing-text.ts';
  import Modal from './Modal.svelte';
  import SubscriptionForm from './SubscriptionForm.svelte';
  import EventFields from './EventFields.svelte';
  import BillingHistory from './BillingHistory.svelte';
  import ScheduleCalculator from './ScheduleCalculator.svelte';
  import type { ScheduleDrafts } from './schedule-controller.ts';
  import './daily.css';
  let { id }: { id: string } = $props();
  const session = getAppSession(),
    token = session.token,
    api = createSubscriptionsAPI(PUBLIC_API_BASE_URL);
  const scheduleDrafts: ScheduleDrafts = new Map();
  let view = $state<FeedView<Subscription>>({
      page: null,
      status: 'idle',
      error: null,
    }),
    editing = $state(false),
    saving = $state(false),
    revision = $state(0);
  let controller:
    ReturnType<typeof createFeed<Subscription, string>> | undefined;
  onMount(() => {
    controller = createFeed(
      api.get,
      token,
      (next) => (view = next),
      session.onDenied,
    );
    void controller.load(id);
    return () => {
      controller?.dispose();
      scheduleDrafts.clear();
    };
  });
</script>

<section class="daily">
  <a class="button" href="/subscriptions">← サブスク一覧</a>
  {#if view.status === 'loading'}<p role="status" class="notice">
      読み込んでいます…
    </p>{:else if view.error}<div class="notice" role="alert">
      <p>{processingErrors[view.error]}</p>
      <button onclick={() => controller?.load(id)}>再読み込み</button>
    </div>{:else if view.page}{@const s = view.page}
    <header class="titlebar">
      <div>
        <h1>{s.service}</h1>
        <p>{s.plan ?? 'プラン不明'}</p>
      </div>
      <button class="primary" onclick={() => (editing = true)}
        >契約を編集</button
      >
    </header>
    <nav class="tabs" aria-label="サブスクの詳細">
      <a
        href={`/subscriptions/${id}`}
        aria-current={page.url.searchParams.get('tab') !== 'history'
          ? 'page'
          : undefined}>概要</a
      ><a
        href={`/subscriptions/${id}?tab=history`}
        aria-current={page.url.searchParams.get('tab') === 'history'
          ? 'page'
          : undefined}>請求・支払い履歴</a
      >
    </nav>
    {#if page.url.searchParams.get('tab') === 'history'}<BillingHistory
        subscription={s}
      />{:else}
      <h2>契約内容</h2>
      <p class="muted">
        {s.provenance === 'automatic'
          ? 'メール解析から自動登録'
          : '本人が確認・保存'}。取引額と契約料金は別に管理します。
      </p>
      <dl class="field-grid">
        <div>
          <dt>料金</dt>
          <dd class="money">{money(s.amount, s.currency)}</dd>
        </div>
        <div>
          <dt>請求周期</dt>
          <dd>{cycle(s.interval_unit, s.interval_count)}</dd>
        </div>
        <div>
          <dt>次回請求日</dt>
          <dd>
            {s.next_billing_date ?? '不明'}
            {#key s.id}<ScheduleCalculator
                subscription={s}
                drafts={scheduleDrafts}
                onSaved={() => {
                  void controller?.load(id);
                }}
              />{/key}
          </dd>
        </div>
        <div>
          <dt>状態</dt>
          <dd>
            {s.status === 'active'
              ? '利用中'
              : s.status === 'ended'
                ? '終了済み'
                : '状態不明'}
          </dd>
        </div>
        <div>
          <dt>更新日時（日本時間）</dt>
          <dd>{processingDate(s.updated_at)}</dd>
        </div>
      </dl>
      <h2>元の AI 抽出情報</h2>
      <p class="muted">
        登録時に紐付けた抽出値です。契約内容を編集しても元の値を保持します。
      </p>
      {#each s.sources as source}<section class="section">
          <h3>AI 抽出 · イベント {source.event_index + 1}</h3>
          <p class="muted">
            紐付け：{source.provenance === 'automatic'
              ? '自動登録'
              : '本人確認'}
          </p>
          <EventFields event={source.original_event} />
        </section>{:else}<p class="empty">
          手動で登録された契約です。元の AI 抽出情報はありません。
        </p>{/each}
    {/if}
    <Modal
      open={editing}
      title="契約を編集"
      busy={saving}
      onclose={() => (editing = false)}
      >{#key revision}<SubscriptionForm
          initial={s}
          onBusyChange={(value) => (saving = value)}
          onSaved={() => {
            editing = false;
            revision++;
            void controller?.load(id);
          }}
        />{/key}</Modal
    >
  {/if}
</section>
