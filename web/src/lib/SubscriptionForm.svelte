<script lang="ts">
  import { onMount } from 'svelte';
  import { getAppSession } from './app-context.ts';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import {
    createSubscriptionsAPI,
    parseValues,
    type Subscription,
    type SubscriptionValues,
  } from './subscriptions-api.ts';
  import {
    blankSubscription,
    subscriptionFromEvent,
    createSubscriptionEditor,
    type EditorView,
    type SubscriptionDrafts,
  } from './subscriptions-controller.ts';
  import type { ProcessingEvent } from './mail-processing-api.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import { money, cycle } from './daily-text.ts';
  let {
    initial = null,
    event = null,
    source = null,
    drafts,
    onSaved,
    onBackToResults = () => {},
    onBusyChange = () => {},
    onFrozen = () => {},
  }: {
    initial?: Subscription | null;
    event?: ProcessingEvent | null;
    source?: { result_id: string; event_index: number } | null;
    drafts?: SubscriptionDrafts;
    onSaved: (s: Subscription) => void;
    onBackToResults?: () => void;
    onBusyChange?: (busy: boolean) => void;
    onFrozen?: (
      state: Pick<EditorView, 'status' | 'uncertain' | 'dirty'>,
    ) => void;
  } = $props();
  const session = getAppSession(),
    token = session.token,
    api = createSubscriptionsAPI(PUBLIC_API_BASE_URL);
  let controller: ReturnType<typeof createSubscriptionEditor> | undefined;
  let view = $state<EditorView>({
    values: blankSubscription(),
    baseline: null,
    latest: null,
    saved: null,
    confirmed: false,
    dirty: false,
    uncertain: false,
    conflict: false,
    status: 'ready',
    error: null,
  });
  const busy = $derived(view.status === 'saving'),
    frozen = $derived(busy || view.uncertain);
  const valid = $derived.by(() => {
    try {
      parseValues(view.values);
      return true;
    } catch {
      return false;
    }
  });
  function edit(patch: Partial<SubscriptionValues>) {
    controller?.edit(patch);
  }
  onMount(() => {
    controller = createSubscriptionEditor(
      api,
      token,
      (next) => {
        view = next;
        onBusyChange(next.status === 'saving');
        onFrozen(next);
      },
      session.onDenied,
      () => crypto.randomUUID(),
      initial,
      source,
      drafts
        ? {
            store: drafts,
            key: source
              ? `${source.result_id}/${source.event_index}`
              : (initial?.id ?? 'manual'),
          }
        : undefined,
    );
    view = controller.state();
    onFrozen(view);
    if (event && !view.dirty) controller.edit(subscriptionFromEvent(event));
    return () => {
      controller?.dispose();
      onBusyChange(false);
    };
  });
  async function save(replay = false) {
    if (replay) await controller?.replay();
    else await controller?.save();
    if (controller?.state().saved) onSaved(controller.state().saved!);
  }
</script>

<form
  class="daily"
  onsubmit={(e) => {
    e.preventDefault();
    void save();
  }}
>
  {#if event}<p class="notice">
      AI
      抽出値を確認して登録してください。不明な日付・周期は補完していません。支払い・返金の抽出金額は取引額です。契約料金として適切か確認してください。解約の検出で既存のサブスクを自動変更しません。
    </p>{/if}
  <div class="form-grid">
    <label class="wide"
      >サービス名<input
        required
        maxlength="400"
        value={view.values.service}
        disabled={frozen}
        oninput={(e) => edit({ service: e.currentTarget.value })}
      /></label
    >
    <label class="wide"
      >プラン（任意）<input
        maxlength="400"
        value={view.values.plan ?? ''}
        disabled={frozen}
        oninput={(e) => edit({ plan: e.currentTarget.value || null })}
      /></label
    >
    <label
      >料金（任意）<input
        type="text"
        inputmode="decimal"
        placeholder="不明の場合は空欄"
        value={view.values.amount ?? ''}
        disabled={frozen}
        oninput={(e) => edit({ amount: e.currentTarget.value || null })}
      /><small class="muted">整数24桁・小数12桁まで。0 も登録できます。</small
      ></label
    >
    <label
      >通貨（料金と一緒に入力）<input
        aria-label="通貨"
        maxlength="3"
        placeholder="例: JPY"
        value={view.values.currency ?? ''}
        disabled={frozen}
        oninput={(e) =>
          edit({ currency: e.currentTarget.value.toUpperCase() || null })}
      /></label
    >
    <label
      >請求周期の回数（任意）<input
        type="text"
        inputmode="numeric"
        value={view.values.interval_count ?? ''}
        disabled={frozen}
        oninput={(e) =>
          edit({
            interval_count:
              e.currentTarget.value === ''
                ? null
                : /^\d+$/.test(e.currentTarget.value)
                  ? Number(e.currentTarget.value)
                  : NaN,
          })}
      /><small class="muted">周期の単位と一緒に 1〜10000 を入力</small></label
    >
    <label
      >周期の単位<select
        value={view.values.interval_unit ?? ''}
        disabled={frozen}
        onchange={(e) =>
          edit({
            interval_unit: (e.currentTarget.value ||
              null) as SubscriptionValues['interval_unit'],
          })}
        ><option value="">不明</option><option value="day">日</option><option
          value="week">週間</option
        ><option value="month">か月</option><option value="year">年</option
        ></select
      ></label
    >
    <label
      >次回請求日（任意）<input
        type="date"
        value={view.values.next_billing_date ?? ''}
        disabled={frozen}
        oninput={(e) =>
          edit({ next_billing_date: e.currentTarget.value || null })}
      /></label
    >
    <label
      >状態<select
        aria-label="状態"
        value={view.values.status}
        disabled={frozen}
        onchange={(e) =>
          edit({
            status: e.currentTarget.value as SubscriptionValues['status'],
          })}
        ><option value="unknown">状態不明</option><option value="active"
          >利用中</option
        ><option value="ended">終了済み</option></select
      ></label
    >
  </div>
  {#if !valid}<p class="muted">
      サービス名を入力し、料金と通貨・周期の回数と単位をそれぞれ揃えてください。日付と数値の形式も確認してください。
    </p>{/if}
  {#if view.conflict}<div class="notice" role="alert">
      {#if view.baseline}<p>
          別の操作で契約が更新されました。入力を残しています。最新の内容を確認してから保存してください。
        </p>
        {#if view.latest}<p>最新のサービス名: {view.latest.service}</p>
          <dl class="field-grid">
            <div>
              <dt>最新のプラン</dt>
              <dd>{view.latest.plan ?? '不明'}</dd>
            </div>
            <div>
              <dt>最新の料金</dt>
              <dd class="money">
                {money(view.latest.amount, view.latest.currency)}
              </dd>
            </div>
            <div>
              <dt>最新の周期</dt>
              <dd>
                {cycle(view.latest.interval_unit, view.latest.interval_count)}
              </dd>
            </div>
            <div>
              <dt>最新の次回請求日</dt>
              <dd>{view.latest.next_billing_date ?? '不明'}</dd>
            </div>
            <div>
              <dt>最新の状態</dt>
              <dd>
                {view.latest.status === 'active'
                  ? '利用中'
                  : view.latest.status === 'ended'
                    ? '終了済み'
                    : '状態不明'}
              </dd>
            </div>
          </dl>
          <div class="actions">
            <button type="button" onclick={() => controller?.rebase()}
              >入力を保持して最新内容を確認し直す</button
            ><button type="button" onclick={() => controller?.discard()}
              >入力を破棄して最新内容に戻す</button
            >
          </div>{:else}<button
            type="button"
            onclick={() => controller?.refreshLatest()}
            >最新の契約内容を再取得</button
          >{/if}
      {:else if source}<p>
          他の操作ですでに登録された可能性があります。入力を保持しています。結果一覧に戻り、最新の登録状況と登録先を確認してください。
        </p>
        <button
          type="button"
          onclick={() => {
            drafts?.delete(`${source.result_id}/${source.event_index}`);
            onBackToResults();
          }}>解析結果の一覧に戻って登録状況を更新</button
        >
      {:else}<p>
          要求が競合しました。入力を保持しています。内容を確認して新規登録をやり直せます。
        </p>
        <button type="button" onclick={() => controller?.discard()}
          >入力を破棄して新規登録をやり直す</button
        >{/if}
    </div>{/if}
  <label class="check"
    ><input
      type="checkbox"
      checked={view.confirmed}
      disabled={frozen || view.conflict}
      onchange={(e) => controller?.confirm(e.currentTarget.checked)}
    />内容を確認しました</label
  >
  {#if view.error}<p role="alert" class="notice error">
      {processingErrors[view.error]}
    </p>{/if}
  {#if view.uncertain}<p class="notice">
      受付状況を確認できません。入力と要求を保持しています。同じ要求で再確認してください。
    </p>
    <button type="button" disabled={busy} onclick={() => save(true)}
      >同じ要求で再確認</button
    >{:else}<button
      class="primary"
      type="submit"
      disabled={busy || !view.confirmed || !valid || view.conflict}
      >{busy ? '保存中…' : initial ? '変更を保存' : 'サブスクを登録'}</button
    >{/if}
</form>
