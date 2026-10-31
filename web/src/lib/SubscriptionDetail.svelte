<script lang="ts">
  import Button from './Button.svelte';
  import ActionLink from './ActionLink.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { onMount, tick } from 'svelte';
  import { page } from '$app/state';
  import { beforeNavigate, goto } from '$app/navigation';
  import {
    createDraftNavigation,
    hasDraftChanges,
  } from './draft-navigation.ts';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { getWorkspace } from './workspace-context.ts';
  import {
    createSubscriptionDelete,
    type DeleteView,
  } from './subscription-delete.ts';
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
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import './daily.css';
  let {
    id,
    embedded = false,
    drafts = new Map(),
    scheduleDrafts = new Map(),
    billingDrafts = new Map(),
    onFrozen = () => {},
    onSaved = () => {},
    onDeleted = () => {},
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
    onDeleted?: () => void;
  } = $props();
  const session = getAppSession(),
    workspace = getWorkspace(),
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
  let deleting = $state(false);
  let deleted = $state(false);
  let deleteView = $state<DeleteView>({
    busy: false,
    uncertain: false,
    locked: false,
    error: null,
  });
  let deleteController: ReturnType<typeof createSubscriptionDelete> | undefined;
  function openDelete() {
    if (!view.page || frozen || childFrozen || deleteView.locked) return;
    const subscription = view.page;
    deleteView = { busy: false, uncertain: false, locked: false, error: null };
    deleteController = createSubscriptionDelete(
      api,
      token,
      id,
      subscription.version,
      (next) => {
        deleteView = next;
        onFrozen({
          status: next.locked ? 'saving' : 'ready',
          uncertain: next.uncertain,
          dirty,
        });
      },
      async () => {
        deleted = true;
        await tick();
        deleting = false;
        drafts.delete(id);
        scheduleDrafts.delete(id);
        for (const [key, draft] of billingDrafts)
          if (draft.subscription_id === id) billingDrafts.delete(key);
        dirty = false;
        onFrozen({ status: 'ready', uncertain: false, dirty: false });
        void workspace?.refresh();
        if (embedded) onDeleted();
        else void goto(`/subscriptions${sourceQuery}`);
      },
      () => {
        deleting = false;
        drafts.clear();
        scheduleDrafts.clear();
        billingDrafts.clear();
        session.onDenied();
      },
    );
    deleting = true;
  }
  const hasUnsaved = () =>
    dirty ||
    hasDraftChanges(
      drafts.values(),
      scheduleDrafts.values(),
      billingDrafts.values(),
    );
  const navigation = createDraftNavigation(
    hasUnsaved,
    () => deleteView.locked || frozen || childFrozen,
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
    if (
      deleteView.locked ||
      frozen ||
      childFrozen ||
      (!embedded && hasUnsaved())
    ) {
      event.preventDefault();
      event.returnValue = '';
    }
  }}
/>

{#snippet detailBody()}
  {#if deleted}<p role="status" class="notice">削除しました。</p>
  {:else if view.status === 'loading'}<p role="status" class="notice">
      読み込んでいます…
    </p>{:else if view.error}<div class="notice" role="alert">
      <p>{processingErrors[view.error]}</p>
      <Button wrap onclick={() => controller?.load(id)}>再読み込み</Button>
    </div>{:else if view.page}{@const s = view.page}
    {#if embedded}<header class="titlebar">
        <div>
          {#if embedded}<ServiceAvatar name={s.service} />{/if}
          <h1>{s.service}</h1>
          <p>{s.plan ?? 'プラン不明'}</p>
        </div>
        <ActionGroup
          ><Button
            wrap
            variant="primary"
            type="button"
            class={['primary']}
            disabled={frozen || childFrozen}
            onclick={() => {
              editing = true;
              localTab = 'edit';
            }}>契約を編集</Button
          ><Button
            wrap
            type="button"
            disabled={frozen || childFrozen || deleteView.locked}
            onclick={openDelete}>削除</Button
          ></ActionGroup
        >
      </header>{/if}
    <nav class={embedded ? 'tabs' : 'library-tabs'} aria-label="サブスクの詳細">
      {#if embedded}{#each [['overview', '契約情報'], ['history', '請求履歴'], ['schedule', '請求予定'], ['sources', '取得元'], ['edit', '編集']] as [value, label]}<Button
            wrap
            variant="tab"
            type="button"
            disabled={frozen || childFrozen}
            aria-current={tab === value ? 'page' : undefined}
            onclick={() => (localTab = value)}>{label}</Button
          >{/each}{:else}
        <ActionLink
          wrap
          variant="tab"
          href={`/subscriptions/${id}${sourceQuery}`}
          aria-current={page.url.searchParams.get('tab') !== 'history'
            ? 'page'
            : undefined}>概要</ActionLink
        ><ActionLink
          wrap
          variant="tab"
          href={`/subscriptions/${id}?tab=history${sourceQuery ? `&${sourceQuery.slice(1)}` : ''}`}
          aria-current={page.url.searchParams.get('tab') === 'history'
            ? 'page'
            : undefined}>請求・支払い履歴</ActionLink
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
      />{:else if embedded && tab === 'schedule' && s.contract_kind === 'fixed_term'}<p
        class="notice"
      >
        期間限定契約には請求予定を設定しません。
      </p>
    {:else if embedded && tab === 'schedule'}<ScheduleCalculator
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
          <dt>契約種別</dt>
          <dd>
            {s.contract_kind === 'fixed_term'
              ? '期間限定（自動更新なし）'
              : s.contract_kind === 'recurring'
                ? '継続契約'
                : '不明'}
          </dd>
        </div>
        <div>
          <dt>効力日</dt>
          <dd>{s.effective_date ?? '不明'}</dd>
        </div>
        <div>
          <dt>利用終了日</dt>
          <dd>{s.access_end_date ?? '不明'}</dd>
        </div>
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
            {#if s.contract_kind === 'fixed_term'}期間限定契約には請求予定を設定しません。{:else}{#key s.id}<ScheduleCalculator
                  subscription={s}
                  drafts={scheduleDrafts}
                  onFrozen={childState}
                  onDirty={childDirty}
                  onSaved={() => {
                    onSaved();
                    void controller?.load(id);
                  }}
                />{/key}{/if}
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
{/snippet}

<section class="daily" class:embedded class:library-page={!embedded}>
  {#if embedded}
    {@render detailBody()}
  {:else}
    <LibraryHeader
      title={view.status === 'ready' && view.page
        ? view.page.service
        : 'サブスクの詳細'}
      description={view.status === 'ready' && view.page
        ? (view.page.plan ?? 'プラン不明')
        : undefined}
      eyebrow="MY LIBRARY"
    >
      {#snippet action()}
        <ActionLink
          wrap
          class={['button']}
          href={`/subscriptions${sourceQuery}`}>← サブスク一覧</ActionLink
        >
        {#if view.status === 'ready' && view.page}<Button
            wrap
            variant="primary"
            type="button"
            class={['primary']}
            disabled={frozen || childFrozen}
            onclick={() => {
              editing = true;
              localTab = 'edit';
            }}>契約を編集</Button
          ><Button
            wrap
            type="button"
            disabled={frozen || childFrozen || deleteView.locked}
            onclick={openDelete}>削除</Button
          >{/if}
      {/snippet}
    </LibraryHeader>
    <LibraryPanel
      ><div class="library-content">{@render detailBody()}</div></LibraryPanel
    >
  {/if}
</section>
<Modal
  open={deleting}
  title="Cycrioから削除"
  busy={deleteView.locked}
  onclose={() => {
    if (!deleteView.locked) deleting = false;
  }}
>
  <p>
    「{view.page
      ?.service}」をCycrioから削除します。サービス自体の解約は行いません。
  </p>
  <p>
    過去の支払い・返金履歴は残ります。サブスク一覧と今後の請求予定からは表示されなくなります。
  </p>
  <p>このサブスクの未保存編集は破棄されます。</p>
  {#if deleteView.error}<p class="notice" role="alert">
      {processingErrors[deleteView.error]}
    </p>{/if}
  {#if deleteView.uncertain}<p class="notice" role="status">
      削除の受付状況が不明です。この画面で同じ要求を再確認してください。
    </p>{/if}
  <ActionGroup>
    <Button
      wrap
      type="button"
      disabled={deleteView.locked}
      onclick={() => (deleting = false)}>キャンセル</Button
    >
    {#if deleteView.error === 'conflict'}
      <Button
        wrap
        type="button"
        onclick={() => {
          deleting = false;
          void controller?.load(id);
        }}>最新の契約を読み込む</Button
      >
    {:else}
      <Button
        wrap
        type="button"
        disabled={deleteView.busy ||
          (!!deleteView.error && !deleteView.uncertain)}
        onclick={() => deleteController?.submit()}
        >{deleteView.uncertain
          ? '同じ要求で削除を確認'
          : 'Cycrioから削除する'}</Button
      >
    {/if}
  </ActionGroup>
</Modal>
{#if !embedded}<Modal
    open={!!leaveTo}
    title="未保存の編集があります"
    busy={frozen || childFrozen}
    onclose={() => navigation.stay()}
    ><p>この画面を離れると、保持している下書きは破棄されます。</p>
    <ActionGroup class={['actions', 'part-SubscriptionDetail-actions']}>
      <Button
        wrap
        type="button"
        disabled={frozen || childFrozen}
        onclick={() => navigation.stay()}>編集を続ける</Button
      ><Button
        wrap
        type="button"
        disabled={frozen || childFrozen}
        onclick={() => navigation.discardAndLeave()}>破棄して移動</Button
      >
    </ActionGroup></Modal
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
