<script lang="ts">
  import ActionGroup from './ActionGroup.svelte';
  import Button from './Button.svelte';
  import CurrencySelect from './CurrencySelect.svelte';
  import BillingDateFields from './BillingDateFields.svelte';
  import ExtractedChargeHint from './ExtractedChargeHint.svelte';
  import { onMount } from 'svelte';
  import { getAppSession } from './app-context.ts';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import {
    createSubscriptionsAPI,
    type Subscription,
    type SubscriptionValues,
  } from './subscriptions-api.ts';
  import {
    blankSubscription,
    parseSubscriptionDraft,
    subscriptionFromEvent,
    createSubscriptionEditor,
    subscriptionBillingAnchor,
    type EditorView,
    type SubscriptionDrafts,
  } from './subscriptions-controller.ts';
  import type { ProcessingEvent } from './mail-processing-api.ts';
  import {
    candidateSourceKey,
    type CandidateSources,
  } from './candidate-selection.ts';
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
    source?: CandidateSources | null;
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
      parseSubscriptionDraft(view.values);
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
              ? candidateSourceKey(source)
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
      抽出値を確認して登録してください。不明な日付・周期は補完していません。通常継続料金が明示された場合だけ契約料金の初期値に使います。取引金額は契約料金へ自動転用しません。解約の検出で既存のサブスクを自動変更しません。
    </p>{/if}
  {#if event}<ExtractedChargeHint
      {event}
      values={view.values}
      disabled={frozen}
      onchange={edit}
    />{/if}
  <div class="form-grid">
    <label class="wide"
      >契約種別<select
        value={view.values.contract_kind ?? 'unknown'}
        disabled={frozen}
        onchange={(e) =>
          edit({
            contract_kind: e.currentTarget
              .value as SubscriptionValues['contract_kind'],
          })}
        ><option value="unknown">不明</option><option value="recurring"
          >継続契約</option
        ><option value="fixed_term">期間限定（自動更新なし）</option></select
      ></label
    >
    <label
      >効力日（任意）<input
        type="date"
        value={view.values.effective_date ?? ''}
        disabled={frozen}
        oninput={(e) => edit({ effective_date: e.currentTarget.value || null })}
      /></label
    >
    <label
      >利用終了日（任意）<input
        type="date"
        value={view.values.access_end_date ?? ''}
        disabled={frozen}
        oninput={(e) =>
          edit({ access_end_date: e.currentTarget.value || null })}
      /></label
    >
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
    {#if view.values.contract_kind !== 'fixed_term'}
      <label
        >通常継続料金（任意）<input
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
        >通貨（料金と一緒に入力）<CurrencySelect
          value={view.values.currency ?? ''}
          disabled={frozen}
          onchange={(currency) => edit({ currency })}
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
        /><small class="muted"
          >単位を選ぶと、空欄は1回として扱います（1〜10000）。</small
        ></label
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
      <BillingDateFields
        values={view.values}
        disabled={frozen}
        onchange={edit}
        receivedAt={initial ? subscriptionBillingAnchor(initial) : null}
      />
    {:else}<p class="muted wide">
        期間限定契約には通常継続料金・請求周期・次回請求日を設定しません。
      </p>{/if}
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
      サービス名、料金と通貨、日付・数値の形式を確認してください。周期の回数は空欄なら1回として扱います。
    </p>{/if}
  {#if view.conflict}<div class="notice" role="alert">
      {#if view.baseline}<p>
          別の操作で契約が更新されました。入力を残しています。最新の内容を確認してから保存してください。
        </p>
        {#if view.latest}<p>最新のサービス名: {view.latest.service}</p>
          <dl class="field-grid">
            <div>
              <dt>最新の契約種別</dt>
              <dd>
                {view.latest.contract_kind === 'fixed_term'
                  ? '期間限定（自動更新なし）'
                  : view.latest.contract_kind === 'recurring'
                    ? '継続契約'
                    : '不明'}
              </dd>
            </div>
            <div>
              <dt>最新の効力日</dt>
              <dd>{view.latest.effective_date ?? '不明'}</dd>
            </div>
            <div>
              <dt>最新の利用終了日</dt>
              <dd>{view.latest.access_end_date ?? '不明'}</dd>
            </div>
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
              <dd>
                {view.latest.next_billing_date ?? '不明'}{view.latest
                  .next_billing_date &&
                view.latest.next_billing_mode === 'automatic'
                  ? '（推定）'
                  : ''}
              </dd>
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
          <ActionGroup class={['actions', 'part-SubscriptionForm-actions']}>
            <Button wrap type="button" onclick={() => controller?.rebase()}
              >入力を保持して最新内容を確認し直す</Button
            ><Button wrap type="button" onclick={() => controller?.discard()}
              >入力を破棄して最新内容に戻す</Button
            >
          </ActionGroup>{:else}<Button
            wrap
            type="button"
            onclick={() => controller?.refreshLatest()}
            >最新の契約内容を再取得</Button
          >{/if}
      {:else if source}<p>
          他の操作ですでに登録された可能性があります。入力を保持しています。結果一覧に戻り、最新の登録状況と登録先を確認してください。
        </p>
        <Button
          wrap
          type="button"
          onclick={() => {
            drafts?.delete(candidateSourceKey(source));
            onBackToResults();
          }}>解析結果の一覧に戻って登録状況を更新</Button
        >
      {:else}<p>
          要求が競合しました。入力を保持しています。内容を確認して新規登録をやり直せます。
        </p>
        <Button wrap type="button" onclick={() => controller?.discard()}
          >入力を破棄して新規登録をやり直す</Button
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
    <Button wrap type="button" disabled={busy} onclick={() => save(true)}
      >同じ要求で再確認</Button
    >{:else}<Button
      wrap
      variant="primary"
      class={['primary']}
      type="submit"
      disabled={busy || !view.confirmed || !valid || view.conflict}
      >{busy ? '保存中…' : initial ? '変更を保存' : 'サブスクを登録'}</Button
    >{/if}
</form>
