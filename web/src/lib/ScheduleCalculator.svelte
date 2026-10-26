<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { beforeNavigate } from '$app/navigation';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { createScheduleAPI } from './schedule-api.ts';
  import {
    createScheduleController,
    draftScheduleRule,
    initialScheduleView,
    scheduleDisplaySnapshot,
    type ScheduleDraft,
    type ScheduleDrafts,
  } from './schedule-controller.ts';
  import type { Subscription } from './subscriptions-api.ts';
  import { cycle } from './daily-text.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import Modal from './Modal.svelte';
  import ScheduleConditions from './ScheduleConditions.svelte';
  import ScheduleReview from './ScheduleReview.svelte';
  import './schedule.css';
  let {
    subscription: subscriptionProp,
    drafts: draftsProp,
    onSaved: onSavedProp,
  }: {
    subscription: Subscription;
    drafts: ScheduleDrafts;
    onSaved: (subscription: Subscription) => void;
  } = $props();
  const { subscription, drafts, onSaved } = untrack(() => ({
    subscription: subscriptionProp,
    drafts: draftsProp,
    onSaved: onSavedProp,
  }));
  const session = getAppSession(),
    token = session.token,
    api = createScheduleAPI(PUBLIC_API_BASE_URL);
  let controller = $state<ReturnType<typeof createScheduleController>>();
  let view = $state(initialScheduleView(subscription)),
    open = $state(false),
    navigationMessage = $state('');
  let alive = false;
  const busy = $derived(view.status === 'saving');
  const frozen = $derived(
    busy ||
      view.uncertain ||
      !!view.saved ||
      view.status === 'denied' ||
      view.status === 'disposed',
  );
  const valid = $derived.by(() => {
    try {
      draftScheduleRule(view.values);
      return true;
    } catch {
      return false;
    }
  });
  const shown = $derived(scheduleDisplaySnapshot(view));
  function edit(patch: Partial<ScheduleDraft>) {
    controller?.edit(patch);
  }
  function acknowledge() {
    const current = controller?.acknowledge();
    if (current && alive) {
      open = false;
      onSaved(current.subscription);
    }
  }
  async function save(replay = false) {
    if (replay) await controller?.replay();
    else await controller?.save();
    if (!alive) return;
    const saved = controller?.state().saved;
    if (
      saved &&
      !replay &&
      saved.current.subscription.version === saved.receipt.applied_version
    )
      acknowledge();
  }
  function close() {
    if (!busy) {
      open = false;
      navigationMessage = '';
    }
  }
  beforeNavigate(({ cancel }) => {
    if (busy) {
      cancel();
      navigationMessage =
        '保存中の画面移動を停止しました。受付状況を確認してから移動してください。';
    }
  });
  onMount(() => {
    alive = true;
    controller = createScheduleController(
      api,
      token,
      (next) => {
        view = next;
      },
      session.onDenied,
      { subscription, drafts },
    );
    view = controller.state();
    const current = controller;
    void current.load();
    return () => {
      alive = false;
      current.dispose();
    };
  });
</script>

<svelte:window
  onbeforeunload={(event) => {
    if (busy) {
      event.preventDefault();
      event.returnValue = '';
    }
  }}
/>

<div class="schedule-entry">
  <button
    type="button"
    onclick={() => {
      open = true;
      navigationMessage = '';
      void controller?.load();
    }}>次回予定を計算</button
  >
  <ScheduleConditions view={shown} />
</div>
<Modal {open} title="次回予定を計算" {busy} onclose={close}>
  <div class="daily schedule">
    <p class="muted">
      確認した請求周期と基準請求日から、今日以降の最初の予定日を計算します。保存すると、このアプリの請求周期と次回請求日を更新します。決済や履歴の推定・変更は行いません。
    </p>
    {#if navigationMessage}<p class="notice" role="status">
        {navigationMessage}
      </p>{/if}
    {#if view.loading === 'view'}<p role="status">
        保存済みの条件を読み込んでいます…
      </p>{/if}
    {#if view.baseline}<p class="schedule-asof">
        本日: {shown?.as_of}（日本時間 · Asia/Tokyo）
      </p>
      {#if view.baseline.settings?.stale}<p class="notice">
          保存済みの条件は再確認が必要です。現在の契約周期を入力欄へ反映し、前回の基準請求日を残しています。実際の周期・基準日を確認してください。
        </p>{/if}
      {#if !view.baseline.settings && view.baseline.subscription.next_billing_date}<p
          class="muted"
        >
          基準請求日の初期入力は、現在の次回請求日を入力候補として示しています。契約上の請求日として確認できる場合に使用してください。将来の予定日も指定できます。
        </p>
      {:else if !view.values.anchor_date}<p class="muted">
          基準請求日は不明です。確認できた日付を入力してください。
        </p>{/if}
      {#if view.baseline.overdue && !view.preview}<p class="schedule-warning">
          登録されている次回請求日が本日より前です。支払いや解約の状態は判断しません。
        </p>{/if}
    {/if}
    <form
      onsubmit={(event) => {
        event.preventDefault();
        void controller?.preview();
      }}
    >
      <fieldset disabled={frozen}>
        <legend class="schedule-legend">確認した計算条件</legend>
        <div class="form-grid">
          <label
            >請求周期の単位<select
              required
              value={view.values.interval_unit}
              onchange={(event) =>
                edit({
                  interval_unit: event.currentTarget
                    .value as ScheduleDraft['interval_unit'],
                })}
              ><option value="">単位を選択</option><option value="day"
                >日</option
              ><option value="week">週間</option><option value="month"
                >か月</option
              ><option value="year">年</option></select
            ></label
          >
          <label
            >請求周期の間隔<input
              type="number"
              min="1"
              max="10000"
              step="1"
              required
              value={view.values.interval_count}
              oninput={(event) =>
                edit({ interval_count: event.currentTarget.value })}
            /><small class="muted"
              >1〜10000。28日ごとと1か月ごとは異なる条件です。</small
            ></label
          >
          <label
            >基準請求日<input
              type="date"
              required
              min="0001-01-01"
              max="9999-12-31"
              value={view.values.anchor_date}
              oninput={(event) =>
                edit({ anchor_date: event.currentTarget.value })}
            /><small class="muted"
              >契約上の請求日として確認できる日付を指定してください。将来の予定日も指定できます。</small
            ></label
          >
          <label
            >日付の指定方法<select
              value={view.values.month_end ? 'month_end' : 'date'}
              onchange={(event) =>
                edit({ month_end: event.currentTarget.value === 'month_end' })}
              ><option value="date">日付指定</option><option
                value="month_end"
                disabled={view.values.interval_unit !== 'month' &&
                  view.values.interval_unit !== 'year'}>月末固定</option
              ></select
            ><small class="muted"
              >月末固定は月・年単位のみ。基準請求日もその月の最終日を指定します。日付指定では、短い月だけ末日に合わせ、その後は元の日付へ戻ります。</small
            ></label
          >
        </div>
      </fieldset>
      {#if !valid && view.values.anchor_date && view.values.interval_unit && view.values.interval_count}<p
          class="schedule-warning"
          role="status"
        >
          周期は1〜10000の整数、基準請求日は実在する日付を指定してください。月末固定の場合、基準請求日も月末である必要があります。
        </p>{/if}
      <button
        type="submit"
        disabled={frozen ||
          !valid ||
          !view.baseline ||
          view.conflict ||
          view.loading === 'preview'}
        >{view.loading === 'preview' ? '計算しています…' : '計算する'}</button
      >
    </form>
    {#if view.preview && view.baseline}<ScheduleReview
        baseline={view.baseline}
        preview={view.preview}
      />{/if}
    {#if view.conflict}<aside class="notice" role="alert">
        <h3>契約が別の画面で更新されています</h3>
        <p>
          入力と編集開始時の契約を保持しています。最新版を確認し、計算し直してください。
        </p>
        {#if view.latest}<p>
            最新の契約: {cycle(
              view.latest.subscription.interval_unit,
              view.latest.subscription.interval_count,
            )} · 次回請求日 {view.latest.subscription.next_billing_date ??
              '不明'}
          </p>
          <div class="actions">
            <button
              type="button"
              disabled={frozen}
              onclick={() => controller?.rebase()}
              >入力を保持して最新版で確認し直す</button
            ><button
              type="button"
              disabled={frozen}
              onclick={() => controller?.discard()}
              >入力を破棄して最新版を開く</button
            >
          </div>
        {:else}<button
            type="button"
            disabled={frozen || view.loading === 'latest'}
            onclick={() => controller?.refreshLatest()}
            >最新の契約を再取得</button
          >{/if}
      </aside>{/if}
    {#if view.reason === 'preview_expired'}<p class="notice" role="alert">
        日本時間の日付が変わりました。条件を保持しています。「計算する」で新しい候補を確認し直してください。
      </p>
    {:else if view.reason === 'preview_changed'}<p class="notice" role="alert">
        計算候補が変わりました。もう一度計算し、候補日を確認してください。
      </p>
    {:else if view.reason === 'ended'}<p class="notice" role="alert">
        契約は終了済みです。次回予定は保存できません。
      </p>
    {:else if view.reason === 'request_conflict'}<p class="notice" role="alert">
        保存要求と受付済みの内容が一致しません。最新の契約を確認してください。
      </p>{/if}
    {#if view.error && !view.reason}<p class="notice error" role="alert">
        {processingErrors[view.error]}
      </p>{/if}
    {#if !view.baseline && !view.uncertain && view.loading !== 'view' && view.status !== 'denied'}<button
        type="button"
        onclick={() => controller?.load()}>条件を再読み込み</button
      >{/if}
    {#if view.uncertain}<p class="notice" role="status">
        保存の受付状況を確認できません。条件・候補日と保存要求を保持しています。日付が変わっても、同じ要求で先に確認してください。
      </p>
      <button type="button" disabled={busy} onclick={() => save(true)}
        >同じ要求で再確認</button
      >
    {:else if view.saved}<aside class="notice" role="status">
        <p>確認した保存要求は受け付けられています。</p>
        <p>受付時の候補日: {view.saved.receipt.candidate_date}</p>
        {#if view.saved.current.subscription.version > view.saved.receipt.applied_version}<p
          >
            その後、契約が変更されています。現在の次回請求日: {view.saved
              .current.subscription.next_billing_date ??
              '不明'}。受付時の候補を再適用しません。
          </p>{:else}<p>
            現在の次回請求日: {view.saved.current.subscription
              .next_billing_date ?? '不明'}
          </p>{/if}<button type="button" onclick={acknowledge}
          >現在の契約を確認して閉じる</button
        >
      </aside>
    {:else}<label class="check"
        ><input
          type="checkbox"
          checked={view.confirmed}
          disabled={frozen || view.conflict || !view.preview?.candidate_date}
          onchange={(event) => controller?.confirm(event.currentTarget.checked)}
        />周期・基準請求日・日付の指定方法・候補日を確認しました</label
      ><button
        class="primary"
        type="button"
        disabled={frozen ||
          !valid ||
          view.conflict ||
          !view.preview?.candidate_date ||
          !view.confirmed}
        onclick={() => save()}
        >{busy ? '保存中…' : '周期・基準日・候補日を確認して保存'}</button
      >{/if}
  </div>
</Modal>
