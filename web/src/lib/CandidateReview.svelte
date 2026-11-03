<script lang="ts">
  import Button from './Button.svelte';
  import MailReceivedDate from './MailReceivedDate.svelte';
  import { untrack, onMount, onDestroy } from 'svelte';
  import Pagination from './Pagination.svelte';
  import DataTable from './DataTable.svelte';
  import { money, cycle, eventNames } from './daily-text.ts';
  import { createFeed, type FeedView } from './daily-controller.ts';
  import {
    createCandidateSelection,
    candidateSourceKey,
    loadCandidateGroup,
    type CandidateSources,
  } from './candidate-selection.ts';
  import ActionGroup from './ActionGroup.svelte';
  import SelectionToolbar from './SelectionToolbar.svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { ApiError } from './api.ts';
  import {
    createApprovalAPI,
    type Candidate,
    type CandidatePage,
  } from './approval-api.ts';
  import type { SubscriptionDrafts } from './subscriptions-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import EventFields from './EventFields.svelte';
  import SubscriptionForm from './SubscriptionForm.svelte';
  import AnalysisAttach from './AnalysisAttach.svelte';
  let {
    candidate,
    drafts,
    onDone,
    onBusyChange,
    initialAction = null,
  }: {
    candidate: Candidate;
    drafts: SubscriptionDrafts;
    onDone: () => void;
    onBusyChange: (busy: boolean) => void;
    initialAction?: 'dismiss' | null;
  } = $props();
  const session = getAppSession(),
    api = createApprovalAPI(PUBLIC_API_BASE_URL);
  const selection = createCandidateSelection(untrack(() => candidate));
  let selected = $state(selection.sources()),
    source = $state<CandidateSources | null>(null),
    groupConfirmed = $state(false),
    selectionError = $state(false),
    related = $state<FeedView<CandidatePage>>({
      page: null,
      status: 'idle',
      error: null,
    });
  const feed = createFeed(
    (offset: number, token, signal) =>
      api.related(candidate, 50, offset, token, signal),
    session.token,
    (view) => {
      related = view;
    },
    session.onDenied,
  );
  let disposed = false,
    dismissAbort: AbortController | undefined,
    selectionAbort: AbortController | undefined;
  let selecting = $state(false),
    selectionLoadError = $state<string | null>(null);
  onMount(() => {
    if ((candidate.related_count ?? 1) > 1) void feed.load(0);
  });
  onDestroy(() => {
    disposed = true;
    feed.dispose();
    dismissAbort?.abort();
    selectionAbort?.abort();
    onBusyChange(false);
  });
  function changedSelection(ok: boolean) {
    selectionError = !ok;
    selected = selection.sources();
    groupConfirmed = false;
  }
  function choose(next: 'create' | 'attach' | 'dismiss') {
    if (
      busy ||
      selecting ||
      uncertain ||
      selected.length === 0 ||
      (next !== 'dismiss' && selected.length > 1 && !groupConfirmed)
    )
      return;
    source = selection.snapshot();
    action = next;
    error = null;
  }
  const known = new Map<string, Candidate>([
    [candidateSourceKey(untrack(() => candidate)), untrack(() => candidate)],
  ]);
  $effect(() => {
    for (const row of related.page?.candidates ?? [])
      known.set(candidateSourceKey(row), row);
  });
  const primaryEvent = $derived(
    source
      ? (known.get(`${source.result_id}/${source.event_index}`)?.event ??
          candidate.event)
      : candidate.event,
  );
  let action = $state<'create' | 'attach' | 'dismiss' | null>(
      untrack(() =>
        (candidate.related_count ?? 1) > 1 ? null : initialAction,
      ),
    ),
    busy = $state(false),
    uncertain = $state(false),
    error = $state<string | null>(null);
  const selectionBlocked = $derived(
    busy || selecting || uncertain || action !== null,
  );
  const pageSelectedCount = $derived(
    related.page?.candidates.filter((row) =>
      selected.some(
        (item) => candidateSourceKey(item) === candidateSourceKey(row),
      ),
    ).length ?? 0,
  );
  async function selectAllRelated() {
    if (selectionBlocked) return;
    selecting = true;
    selectionLoadError = null;
    selectionAbort = new AbortController();
    onBusyChange(true);
    try {
      const rows = await loadCandidateGroup(
        (offset, signal) =>
          api.related(candidate, 50, offset, session.token, signal),
        selectionAbort.signal,
      );
      if (disposed) return;
      for (const row of rows) known.set(candidateSourceKey(row), row);
      changedSelection(selection.selectPage(rows));
    } catch (error) {
      if (disposed) return;
      if (error instanceof Error && error.message === 'selection_limit')
        selectionError = true;
      else {
        const code =
          error instanceof ApiError ? error.code : 'dependency_unavailable';
        selectionLoadError = processingErrors[code];
        if (['authentication_required', 'access_denied'].includes(code))
          session.onDenied();
      }
    } finally {
      if (!disposed) {
        selecting = false;
        onBusyChange(false);
      }
    }
  }
  function frozen(value: boolean) {
    busy = value;
    onBusyChange(value);
  }
  async function dismiss() {
    if (busy) return;
    const snapshot = source ?? selection.snapshot();
    if (!snapshot) return;
    frozen(true);
    error = null;
    try {
      dismissAbort = new AbortController();
      await api.dismiss(snapshot, session.token, dismissAbort.signal);
      if (disposed) return;
      uncertain = false;
      onDone();
    } catch (e) {
      if (disposed) return;
      const code = e instanceof ApiError ? e.code : 'dependency_unavailable';
      error = processingErrors[code];
      uncertain = code === 'dependency_unavailable';
      if (['authentication_required', 'access_denied'].includes(code))
        session.onDenied();
    } finally {
      if (!disposed) {
        busy = false;
        onBusyChange(uncertain);
      }
    }
  }
</script>

<div class="daily">
  {#if action === null}
    <div class="review-summary">
      <strong>{candidate.event.service ?? 'サービス名未確認'}</strong>
      <p>
        {money(candidate.event.amount, candidate.event.currency)} · {cycle(
          candidate.event.interval_unit,
          candidate.event.interval_count,
        )}
      </p>
      <p class="muted">
        {candidate.account_label} · {eventNames[candidate.event.event_type]}
      </p>
      <p class="muted">
        <MailReceivedDate receivedAt={candidate.received_at} />
      </p>
    </div>
    <details>
      <summary>このメールの詳細</summary><EventFields event={candidate.event} />
    </details>
  {/if}
  {#if (candidate.related_count ?? 1) > 1 && action === null}
    <section class="related" aria-label="関連メールの比較">
      <p role="status">選択中 {selected.length} 件</p>
      <details>
        <summary
          >{candidate.mail_count > 1
            ? '関連メールを比較・選択'
            : 'このメール内の情報を比較・選択'}（{candidate.related_count}件）</summary
        >
        <p class="notice">
          まとめて登録する場合は、同じ契約の情報か確認して選択してください。
        </p>
        {#if related.status === 'loading'}<p role="status">
            関連メールを読み込み中…
          </p>
        {:else if related.error}<p role="alert">
            {processingErrors[related.error]}
          </p>
          <Button
            type="button"
            wrap
            onclick={() => feed.load(related.page?.offset ?? 0)}
            >関連メールを再読み込み</Button
          >
        {:else if related.page}
          <SelectionToolbar
            selectedCount={selected.length}
            pageCount={related.page.candidates.length}
            allPageSelected={related.page.candidates.length > 0 &&
              pageSelectedCount === related.page.candidates.length}
            partialPageSelected={pageSelectedCount > 0 &&
              pageSelectedCount < related.page.candidates.length}
            disabled={selectionBlocked}
            busy={selecting}
            totalCount={related.page.total}
            onTogglePage={(checked) =>
              changedSelection(
                selection.togglePage(related.page!.candidates, checked),
              )}
            onClear={() => {
              selection.clear();
              changedSelection(true);
            }}
            onSelectAll={related.page.total <= 100
              ? selectAllRelated
              : undefined}
          />
          {#if related.page.total > 100}<p class="muted">
              一度に選択できるのは100件までです。ページごとに選択してください。
            </p>{/if}
          <DataTable responsive="cards" class="related-list">
            <thead
              ><tr
                ><th>メールの選択</th><th>受信日時・接続</th><th
                  >抽出金額・周期</th
                ><th>抽出日付</th></tr
              ></thead
            >
            <tbody>
              {#each related.page.candidates as row (`${row.result_id}/${row.event_index}`)}
                <tr
                  ><td data-primary>
                    <label class="check"
                      ><input
                        type="checkbox"
                        checked={selected.some(
                          (s) =>
                            candidateSourceKey(s) === candidateSourceKey(row),
                        )}
                        disabled={selectionBlocked}
                        onchange={(e) =>
                          changedSelection(
                            selection.toggle(row, e.currentTarget.checked),
                          )}
                      />このメールを選択</label
                    >
                    <strong>{row.event.service ?? 'サービス名未確認'}</strong>
                    <span class="secondary"
                      >{row.event.plan ?? 'プラン不明'} · {eventNames[
                        row.event.event_type
                      ]}</span
                    >
                  </td><td data-label="受信日時・接続">
                    <span class="secondary">{row.account_label}</span
                    ><MailReceivedDate receivedAt={row.received_at} />
                  </td><td data-label="抽出金額・周期">
                    <span class="secondary"
                      >取引金額 {money(
                        row.event.amount,
                        row.event.currency,
                      )}</span
                    >
                    <span class="secondary"
                      >通常継続料金 {money(
                        row.event.recurring_amount ?? null,
                        row.event.recurring_currency ?? null,
                      )}</span
                    >
                    <span class="secondary"
                      >{cycle(
                        row.event.interval_unit,
                        row.event.interval_count,
                      )}</span
                    >
                  </td><td data-label="抽出日付">
                    <span class="secondary"
                      >請求日 {row.event.billed_at ?? '不明'}</span
                    >
                    <span class="secondary"
                      >次回請求日 {row.event.next_billing_at ?? '不明'}</span
                    >
                    {#if row.event.period_start || row.event.period_end}<span
                        class="secondary"
                        >利用期間 {row.event.period_start ?? '不明'} 〜 {row
                          .event.period_end ?? '不明'}</span
                      >{/if}
                    {#if row.event.effective_date}<span class="secondary"
                        >効力日 {row.event.effective_date}</span
                      >{/if}
                    {#if row.event.access_end_date}<span class="secondary"
                        >利用終了日 {row.event.access_end_date}</span
                      >{/if}
                  </td></tr
                >
              {/each}
            </tbody>
          </DataTable>
          <Pagination
            total={related.page.total}
            offset={related.page.offset}
            limit={related.page.limit}
            disabled={selectionBlocked}
            label="関連メールのページ"
            onchange={(offset) => feed.load(offset)}
          />
        {/if}
      </details>
      {#if selectionLoadError}<p role="alert">{selectionLoadError}</p>{/if}
      {#if selectionError}<p role="alert">
          一度に選択できるのは100件までです。
        </p>{/if}
      {#if selected.length > 1}<label class="check"
          ><input
            type="checkbox"
            bind:checked={groupConfirmed}
            disabled={selectionBlocked}
          />選択したメールは同じ契約の情報であることを確認しました</label
        >{/if}
    </section>
  {/if}
  {#if source && action}<p class="notice">
      選択したメール：{1 + (source.additional_sources?.length ?? 0)} 件
    </p>{/if}
  {#if action === 'create'}
    <SubscriptionForm
      event={primaryEvent}
      {source}
      {drafts}
      onSaved={onDone}
      onBackToResults={onDone}
      onFrozen={(state) => frozen(state.status === 'saving' || state.uncertain)}
    />
  {:else if action === 'attach'}
    <details>
      <summary>元の抽出情報を確認</summary>
      <EventFields event={primaryEvent} />
    </details>
    <AnalysisAttach
      result={{ id: source?.result_id ?? candidate.result_id }}
      index={source?.event_index ?? candidate.event_index}
      additionalSources={source?.additional_sources ?? []}
      onSaved={onDone}
      onBackToResults={onDone}
      onBusyChange={frozen}
    />
  {:else if action === 'dismiss'}
    <p>
      選択した {1 + (source?.additional_sources?.length ?? 0)} 件を確認待ちから除外します。登録済みのサブスクは変更しません。
    </p>
    {#if error}<p role="alert" class="notice">{error}</p>{/if}
    <Button wrap type="button" disabled={busy} onclick={dismiss}
      >{uncertain ? '同じ要求で除外を確認' : '選択したメールを除外する'}</Button
    >
  {:else}
    <ActionGroup
      spacing="both"
      class={['actions', 'part-CandidateDetail-actions']}
    >
      <Button
        wrap
        variant="primary"
        class={['primary']}
        type="button"
        disabled={selectionBlocked ||
          selected.length === 0 ||
          (selected.length > 1 && !groupConfirmed)}
        onclick={() => choose('create')}>新規サブスクに登録</Button
      ><Button
        wrap
        type="button"
        disabled={selectionBlocked ||
          selected.length === 0 ||
          (selected.length > 1 && !groupConfirmed)}
        onclick={() => choose('attach')}>既存のサブスクに紐付ける</Button
      ><Button
        wrap
        type="button"
        disabled={selectionBlocked || selected.length === 0}
        onclick={() => choose('dismiss')}>不要として除外</Button
      >
    </ActionGroup>
  {/if}
  {#if action}<ActionGroup
      spacing="before"
      class={[
        'actions back',
        'part-CandidateDetail-actions',
        'part-CandidateDetail-back',
      ]}
    >
      <Button
        wrap
        type="button"
        disabled={busy || uncertain}
        onclick={() => {
          action = null;
          source = null;
          error = null;
        }}>選択に戻る</Button
      >
    </ActionGroup>{/if}
</div>

<style>
  .related {
    min-width: 0;
  }
  summary {
    cursor: pointer;
    padding-block: 12px;
    font-weight: 600;
  }
  .review-summary p {
    margin-block: 8px;
  }
</style>
