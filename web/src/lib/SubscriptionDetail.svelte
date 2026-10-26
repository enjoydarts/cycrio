<script lang="ts">
  import { onMount } from 'svelte';
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
  import './daily.css';
  let { id }: { id: string } = $props();
  const session = getAppSession(),
    token = session.token,
    api = createSubscriptionsAPI(PUBLIC_API_BASE_URL);
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
    return () => controller?.dispose();
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
    <h2>確認済みの契約内容</h2>
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
        <dd>{s.next_billing_date ?? '不明'}</dd>
      </div>
      <div>
        <dt>状態</dt>
        <dd>{s.status === 'active' ? '利用中' : '終了済み'}</dd>
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
        <EventFields event={source.original_event} />
      </section>{:else}<p class="empty">
        手動で登録された契約です。元の AI 抽出情報はありません。
      </p>{/each}
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
