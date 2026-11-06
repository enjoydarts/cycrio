<script lang="ts">
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import {
    createScheduledChangesAPI,
    type ScheduledChange,
    type ScheduledChangePage,
  } from './subscription-scheduled-changes-api.ts';
  import {
    createScheduledCancellation,
    type CancellationView,
  } from './scheduled-change-controller.ts';
  import { createFeed, type FeedView } from './daily-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import ScheduledChangeSummary from './ScheduledChangeSummary.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import Pagination from './Pagination.svelte';
  import Modal from './Modal.svelte';
  let {
    id,
    onFrozen = () => {},
    onChanged = () => {},
  }: {
    id: string;
    onFrozen?: (frozen: boolean) => void;
    onChanged?: () => void;
  } = $props();
  const session = getAppSession(),
    api = createScheduledChangesAPI(PUBLIC_API_BASE_URL);
  let view = $state<FeedView<ScheduledChangePage>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let offset = $state(0),
    selected = $state<ScheduledChange | null>(null);
  let cancelView = $state<CancellationView>({
    busy: false,
    uncertain: false,
    error: null,
  });
  let cancellation: ReturnType<typeof createScheduledCancellation> | undefined;
  const locked = $derived(cancelView.busy || cancelView.uncertain);
  const feed = createFeed(
    (next: number, token, signal) => api.list(id, next, token, signal),
    session.token,
    (next) => (view = next),
    session.onDenied,
  );
  function load(next = 0) {
    if (locked) return;
    offset = next;
    void feed.load(next);
  }
  function close() {
    if (locked) return;
    selected = null;
    cancellation?.dispose();
    cancellation = undefined;
    cancelView = { busy: false, uncertain: false, error: null };
  }
  function open(schedule: ScheduledChange) {
    if (locked) return;
    cancellation?.dispose();
    selected = schedule;
    cancelView = { busy: false, uncertain: false, error: null };
    cancellation = createScheduledCancellation(
      api,
      session.token,
      schedule,
      (next) => {
        cancelView = next;
        onFrozen(next.busy || next.uncertain);
      },
      () => {
        selected = null;
        onFrozen(false);
        onChanged();
        load(offset);
      },
      () => {
        selected = null;
        onFrozen(false);
        session.onDenied();
      },
    );
  }
  onMount(() => {
    load();
    return () => {
      feed.dispose();
      cancellation?.dispose();
      onFrozen(false);
    };
  });
</script>

<section aria-label="契約変更の予約">
  <h2>契約変更の予約</h2>
  <p class="muted">
    予約中は現在の契約内容を保持します。効力日（日本時間）以降の反映前の確認時に、契約の更新や元情報の変更が見つかれば要再確認で停止します。
  </p>
  {#if view.status === 'loading'}<p role="status">予約を読み込んでいます…</p>
  {:else if view.error}<p role="alert">{processingErrors[view.error]}</p>
    <Button type="button" wrap onclick={() => load(offset)}
      >予約を再読み込み</Button
    >
  {:else if view.page}
    {#each view.page.schedules as schedule (schedule.id)}<ScheduledChangeSummary
        {schedule}
        {locked}
        onCancel={() => open(schedule)}
      />{:else}<p class="empty">契約変更の予約はありません。</p>{/each}
    <ActionGroup
      ><Pagination
        total={view.page.total}
        offset={view.page.offset}
        limit={view.page.limit}
        label="契約変更の予約のページ"
        onchange={load}
      /></ActionGroup
    >
  {/if}
</section>
{#if selected}<Modal
    open={true}
    title="予約の取消を確認"
    busy={locked}
    onclose={close}
  >
    <p>
      {selected.effective_on}（日本時間）の変更予約を取り消します。現在の契約内容は変更しません。元の抽出情報は候補に戻ります。
    </p>
    {#if cancelView.error}<p role="alert">
        {processingErrors[cancelView.error]}
      </p>{/if}
    {#if cancelView.uncertain}<p role="status">
        取消の受付状況が不明です。この画面で同じ要求を再確認してください。
      </p>{/if}
    {#if cancelView.error === 'conflict'}<p>
        予約の状態が更新されました。閉じて最新の予約を確認してください。
      </p>{/if}
    <ActionGroup
      ><Button
        type="button"
        wrap
        disabled={locked}
        onclick={() => {
          close();
          load(offset);
        }}>閉じる</Button
      >
      <Button
        type="button"
        wrap
        disabled={cancelView.busy ||
          (!!cancelView.error && !cancelView.uncertain)}
        onclick={() => cancellation?.submit()}
        >{cancelView.uncertain
          ? '同じ要求で取消を確認'
          : 'この予約を取り消す'}</Button
      >
    </ActionGroup>
  </Modal>{/if}

<style>
  section {
    min-width: 0;
    margin-top: 28px;
  }
  p {
    overflow-wrap: anywhere;
  }
</style>
