<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { beforeNavigate, goto } from '$app/navigation';
  import {
    createDraftNavigation,
    hasDraftChanges,
  } from './draft-navigation.ts';
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
  import type { BillingDrafts } from './billing-controller.ts';
  import type {
    SubscriptionDrafts,
    EditorView,
  } from './subscriptions-controller.ts';
  import ServiceAvatar from './ServiceAvatar.svelte';
  import './daily.css';
  let {
    id,
    embedded = false,
    drafts = new Map(),
    scheduleDrafts = new Map(),
    billingDrafts = new Map(),
    onFrozen = () => {},
    onSaved = () => {},
  }: {
    id: string;
    embedded?: boolean;
    drafts?: SubscriptionDrafts;
    scheduleDrafts?: ScheduleDrafts;
    billingDrafts?: BillingDrafts;
    onFrozen?: (
      state: Pick<EditorView, 'status' | 'uncertain' | 'dirty'>,
    ) => void;
    onSaved?: () => void;
  } = $props();
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
  let localTab = $state('overview'),
    frozen = $state(false),
    dirty = $state(false),
    childFrozen = $state(false);
  let leaveTo = $state('');
  const hasUnsaved = () =>
    dirty ||
    hasDraftChanges(
      drafts.values(),
      scheduleDrafts.values(),
      billingDrafts.values(),
    );
  const navigation = createDraftNavigation(
    hasUnsaved,
    () => frozen || childFrozen,
    () => {
      drafts.clear();
      scheduleDrafts.clear();
      billingDrafts.clear();
      dirty = false;
    },
    goto,
    (target) => (leaveTo = target),
  );
  const tab = $derived(
    embedded ? localTab : (page.url.searchParams.get('tab') ?? 'overview'),
  );
  const sourceQuery = $derived(
    page.url.searchParams.get('account_id')
      ? `?account_id=${encodeURIComponent(page.url.searchParams.get('account_id')!)}`
      : '',
  );
  function editorState(
    next: Pick<EditorView, 'status' | 'uncertain' | 'dirty'>,
  ) {
    frozen = next.status === 'saving' || next.uncertain;
    dirty = next.dirty;
    onFrozen(next);
  }
  function childState(value: boolean) {
    childFrozen = value;
    onFrozen({ status: value ? 'saving' : 'ready', uncertain: false, dirty });
  }
  function childDirty(value: boolean) {
    dirty = value;
    onFrozen({
      status: frozen || childFrozen ? 'saving' : 'ready',
      uncertain: false,
      dirty: value,
    });
  }
  beforeNavigate(({ cancel, to }) => {
    if (embedded) return;
    if (!navigation.request(page.url.href, to?.url.href)) cancel();
  });
  let controller:
    ReturnType<typeof createFeed<Subscription, string>> | undefined;
  onMount(() => {
    controller = createFeed(
      api.get,
      token,
      (next) => (view = next),
      () => {
        drafts.clear();
        scheduleDrafts.clear();
        billingDrafts.clear();
        session.onDenied();
      },
    );
    void controller.load(id);
    return () => {
      controller?.dispose();
    };
  });
</script>

<svelte:window
  onbeforeunload={(event) => {
    if (frozen || childFrozen || (!embedded && hasUnsaved())) {
      event.preventDefault();
      event.returnValue = '';
    }
  }}
/>

<section class="daily" class:embedded>
  {#if !embedded}<a class="button" href={`/subscriptions${sourceQuery}`}
      >← サブスク一覧</a
    >{/if}
  {#if view.status === 'loading'}<p role="status" class="notice">
      読み込んでいます…
    </p>{:else if view.error}<div class="notice" role="alert">
      <p>{processingErrors[view.error]}</p>
      <button onclick={() => controller?.load(id)}>再読み込み</button>
    </div>{:else if view.page}{@const s = view.page}
    <header class="titlebar">
      <div>
        {#if embedded}<ServiceAvatar name={s.service} />{/if}
        <h1>{s.service}</h1>
        <p>{s.plan ?? 'プラン不明'}</p>
      </div>
      <button
        class="primary"
        disabled={frozen || childFrozen}
        onclick={() => {
          editing = true;
          localTab = 'edit';
        }}>契約を編集</button
      >
    </header>
    <nav class="tabs" aria-label="サブスクの詳細">
      {#if embedded}{#each [['overview', '契約情報'], ['history', '請求履歴'], ['schedule', '請求予定'], ['sources', '取得元'], ['edit', '編集']] as [value, label]}<button
            type="button"
            disabled={frozen || childFrozen}
            aria-current={tab === value ? 'page' : undefined}
            onclick={() => (localTab = value)}>{label}</button
          >{/each}{:else}
        <a
          href={`/subscriptions/${id}${sourceQuery}`}
          aria-current={page.url.searchParams.get('tab') !== 'history'
            ? 'page'
            : undefined}>概要</a
        ><a
          href={`/subscriptions/${id}?tab=history${sourceQuery ? `&${sourceQuery.slice(1)}` : ''}`}
          aria-current={page.url.searchParams.get('tab') === 'history'
            ? 'page'
            : undefined}>請求・支払い履歴</a
        >
      {/if}
    </nav>
    {#if tab === 'history'}<BillingHistory
        subscription={s}
        drafts={billingDrafts}
        onFrozen={childState}
        onDirty={childDirty}
      />{:else if embedded && tab === 'edit'}<SubscriptionForm
        initial={s}
        {drafts}
        onFrozen={editorState}
        onSaved={() => {
          frozen = false;
          dirty = false;
          localTab = 'overview';
          onSaved();
          void controller?.load(id);
        }}
      />{:else if embedded && tab === 'schedule'}<ScheduleCalculator
        subscription={s}
        drafts={scheduleDrafts}
        onFrozen={childState}
        onDirty={childDirty}
        onSaved={() => {
          onSaved();
          void controller?.load(id);
        }}
      />{:else if tab !== 'sources'}
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
                onFrozen={childState}
                onDirty={childDirty}
                onSaved={() => {
                  onSaved();
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
    {/if}
    {#if tab === 'sources' || (!embedded && tab !== 'history')}
      <h2>元の AI 抽出情報</h2>
      <p class="muted">
        登録時に紐付けた抽出値です。契約内容を編集しても元の値を保持します。
      </p>
      {#each s.sources as source}<section class="section">
          <h3>AI 抽出 · イベント {source.event_index + 1}</h3>
          {#if source.account_label}<p>
              {source.account_label} · {source.provider}
            </p>{/if}
          <p class="muted">
            紐付け：{source.provenance === 'automatic'
              ? '自動登録'
              : '本人確認'}
          </p>
          <EventFields event={source.original_event} />
        </section>{:else}<p class="empty">
          元の AI 抽出情報はありません。
        </p>{/each}
    {/if}
    {#if embedded}<a
        class="full-link"
        href={`/subscriptions/${id}${sourceQuery}`}>詳細ページを開く →</a
      >{/if}
    {#if editing && !embedded}<Modal
        open={editing && !embedded}
        title="契約を編集"
        busy={saving || frozen}
        onclose={() => (editing = false)}
        >{#key revision}<SubscriptionForm
            initial={s}
            {drafts}
            onFrozen={editorState}
            onBusyChange={(value) => (saving = value)}
            onSaved={() => {
              editing = false;
              revision++;
              onSaved();
              void controller?.load(id);
            }}
          />{/key}</Modal
      >{/if}
  {/if}
</section>
{#if !embedded}<Modal
    open={!!leaveTo}
    title="未保存の編集があります"
    busy={frozen || childFrozen}
    onclose={() => navigation.stay()}
    ><p>この画面を離れると、保持している下書きは破棄されます。</p>
    <div class="actions">
      <button
        type="button"
        disabled={frozen || childFrozen}
        onclick={() => navigation.stay()}>編集を続ける</button
      ><button
        type="button"
        disabled={frozen || childFrozen}
        onclick={() => navigation.discardAndLeave()}>破棄して移動</button
      >
    </div></Modal
  >{/if}

<style>
  .embedded {
    padding: 0;
  }
  .embedded .titlebar {
    margin-top: 0;
    align-items: flex-start;
  }
  .embedded h1 {
    font-size: 24px;
    margin: 16px 0 6px;
  }
  .embedded .tabs {
    display: flex;
    gap: 14px;
    overflow: auto;
  }
  .tabs button {
    background: none;
    border: 0;
    border-radius: 0;
    padding: 12px 0;
    white-space: nowrap;
    font-size: 12px;
    color: var(--color-muted);
    border-bottom: 2px solid transparent;
  }
  .tabs button[aria-current] {
    color: var(--brand, #7863cf);
    border-color: var(--brand, #7863cf);
  }
  .embedded h2 {
    font-size: 15px;
  }
  .full-link {
    display: inline-block;
    margin-top: 24px;
    font-size: 12px;
    color: var(--color-muted);
  }
</style>
