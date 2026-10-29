<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import {
    createBillingAPI,
    parseBillingValues,
    type BillingEntry,
    type BillingSource,
  } from './billing-api.ts';
  import {
    createBillingEditor,
    initialBillingView,
    type BillingDraft,
    type BillingDrafts,
  } from './billing-controller.ts';
  import type { Subscription } from './subscriptions-api.ts';
  import { processingErrors, processingDate } from './mail-processing-text.ts';
  import EventFields from './EventFields.svelte';
  import BillingTable from './BillingTable.svelte';
  import BillingSources from './BillingSources.svelte';
  import './billing.css';
  let {
    subscription: subscriptionProp,
    initial: initialProp = null,
    source: sourceProp = null,
    drafts: draftsProp,
    onSaved: onSavedProp,
    onBusyChange: onBusyChangeProp = () => {},
    onDirty: onDirtyProp = () => {},
  }: {
    subscription: Subscription;
    initial?: BillingEntry | null;
    source?: BillingSource | null;
    drafts: BillingDrafts;
    onSaved: (entry: BillingEntry) => void;
    onBusyChange?: (busy: boolean) => void;
    onDirty?: (dirty: boolean) => void;
  } = $props();
  // The controller, visible original values and callbacks share one immutable
  // target lifetime. A different target is mounted by BillingHistory's key.
  const {
    subscription,
    initial,
    source,
    drafts,
    onSaved,
    onBusyChange,
    onDirty,
  } = untrack(() => ({
    subscription: subscriptionProp,
    initial: initialProp,
    source: sourceProp,
    drafts: draftsProp,
    onSaved: onSavedProp,
    onBusyChange: onBusyChangeProp,
    onDirty: onDirtyProp,
  }));
  const session = getAppSession(),
    token = session.token,
    api = createBillingAPI(PUBLIC_API_BASE_URL);
  let controller = $state<ReturnType<typeof createBillingEditor>>();
  let view = $state(initialBillingView()),
    choosingPayment = $state(false),
    paymentID = $state(''),
    reuse = $state<BillingEntry | null>(null),
    reuseConfirmed = $state(false);
  const busy = $derived(view.status === 'saving'),
    frozen = $derived(
      busy ||
        view.uncertain ||
        view.status === 'denied' ||
        view.status === 'disposed',
    );
  const valid = $derived.by(() => {
    try {
      parseBillingValues(view.values);
      return true;
    } catch {
      return false;
    }
  });
  const dateLabel = $derived(
    view.values.kind === 'payment'
      ? '支払日'
      : view.values.kind === 'refund'
        ? '返金日'
        : view.values.kind === 'invoice'
          ? '請求日'
          : '明細の日付',
  );
  function edit(patch: Partial<BillingDraft>) {
    controller?.edit(patch);
    reuse = null;
    reuseConfirmed = false;
  }
  function chooseReuse(entry: BillingEntry) {
    if (frozen || entry.subscription_id !== subscription.id) return;
    reuse = entry;
    reuseConfirmed = false;
  }
  async function save(replay = false) {
    if (replay) await controller?.replay();
    else await controller?.save();
    const saved = controller?.state().saved;
    if (saved) onSaved(saved);
  }
  async function attachExisting() {
    if (frozen || !reuse || !reuseConfirmed) return;
    await controller?.attachTo(reuse, true);
    const saved = controller?.state().saved;
    if (saved) onSaved(saved);
  }
  onMount(() => {
    controller = createBillingEditor(
      api,
      token,
      (next) => {
        view = next;
        onBusyChange(next.status === 'saving' || next.uncertain);
        onDirty(next.dirty);
        if (next.error === 'conflict') reuseConfirmed = false;
        if (next.status === 'denied' || next.status === 'disposed') {
          reuse = null;
          reuseConfirmed = false;
          paymentID = '';
          choosingPayment = false;
        }
      },
      session.onDenied,
      {
        subscription_id: subscription.id,
        initial,
        source,
        drafts,
        draftKey: source
          ? `${subscription.id}/${source.result_id}/${source.event_index}`
          : `${subscription.id}/${initial?.id ?? 'manual'}`,
      },
    );
    view = controller.state();
    onDirty(view.dirty);
    const current = controller;
    async function initialize() {
      if (source && !initial)
        await current.initializeSource(source.original_event);
      if (current.state().values.refund_of_id)
        await current.loadSelectedPayment();
    }
    void initialize();
    return () => {
      current.dispose();
      onBusyChange(false);
      onDirty(false);
    };
  });
</script>

<div class="daily billing">
  {#if view.loading.includes('source')}<p class="notice" role="status">
      この根拠の登録先を確認しています…
    </p>{/if}
  {#if source}<details open>
      <summary>今回の根拠 · 元の AI 抽出値</summary><EventFields
        event={source.original_event}
      />
      <p class="muted">
        抽出された請求日は候補です。支払日・返金日を意味するか確認してください。不明な値は補完していません。
      </p>
    </details>{/if}
  {#if view.linked}<aside class="notice">
      <p>
        この根拠はすでに履歴に紐付いています（{view.linked.state === 'active'
          ? '有効'
          : '無効'}）。
      </p>
      <p class="billing-id">{view.linked.id}</p>
      {#if view.baseline?.id !== view.linked.id}<p>
          現在の入力を保持しています。登録先を開く場合は、入力を破棄することを確認してください。
        </p>
        <button
          type="button"
          disabled={frozen}
          onclick={() => controller?.adoptLinked()}
          >入力を破棄して登録済みの履歴を開く</button
        >{/if}
    </aside>{/if}
  <form
    onsubmit={(event) => {
      event.preventDefault();
      void save();
    }}
  >
    {#if view.baseline}<p class="muted billing-id">
        明細 {view.baseline.id} · 版 {view.baseline.version} · 更新 {processingDate(
          view.baseline.updated_at,
        )}（日本時間）
      </p>{/if}
    <fieldset disabled={frozen}>
      <div class="form-grid">
        <label
          >種別<select
            required
            value={view.values.kind}
            onchange={(event) =>
              edit({ kind: event.currentTarget.value as BillingDraft['kind'] })}
            ><option value="">種別を選択</option><option value="invoice"
              >請求</option
            ><option value="payment">支払い</option><option value="refund"
              >返金</option
            ></select
          ></label
        >
        <label
          >{dateLabel}<input
            required
            type="date"
            value={view.values.occurred_on}
            oninput={(event) =>
              edit({ occurred_on: event.currentTarget.value })}
          /><small class="muted"
            >{dateLabel}として確認できた日付を入力してください。</small
          ></label
        >
        <label
          >金額<input
            required
            type="text"
            inputmode="decimal"
            value={view.values.amount}
            placeholder="確認した金額"
            oninput={(event) => edit({ amount: event.currentTarget.value })}
          /><small class="muted"
            >0 以上、整数24桁・小数12桁まで。返金額も正の金額で入力します。</small
          ></label
        >
        <label
          >通貨<input
            required
            type="text"
            maxlength="3"
            value={view.values.currency}
            placeholder="例: JPY"
            oninput={(event) =>
              edit({ currency: event.currentTarget.value.toUpperCase() })}
          /></label
        >
        <label
          >記録の状態<select
            value={view.values.state}
            onchange={(event) =>
              edit({
                state: event.currentTarget.value as BillingDraft['state'],
              })}
            ><option value="active">有効</option><option value="voided"
              >無効</option
            ></select
          ></label
        >
      </div>
      <p class="muted">
        無効化・復元はこのアプリ内の記録だけを変更します。決済の取消・返金の実行は行いません。
      </p>
      {#if view.values.kind === 'refund'}<section class="billing-picker">
          <h3>返金元の支払い</h3>
          <p class="muted">
            同じ契約・通貨の有効な支払いを選択してください。返金の累計は元の支払額を超えられません。
          </p>
          {#if view.selectedPayment}<BillingTable
              entries={[view.selectedPayment]}
            />{:else if view.values.refund_of_id}<p class="billing-id">
              選択中: {view.values.refund_of_id}
            </p>{:else}<p>返金元は未選択です。</p>{/if}
          <div class="actions">
            <button
              type="button"
              disabled={!view.values.currency}
              onclick={() => {
                choosingPayment = true;
                void controller?.loadPayments();
              }}>支払いを選ぶ</button
            >{#if view.values.refund_of_id}<button
                type="button"
                onclick={() => edit({ refund_of_id: null })}>選択を解除</button
              >{/if}
          </div>
          {#if choosingPayment}<label
              >支払いの明細 ID で取得<input
                type="text"
                bind:value={paymentID}
              /></label
            ><button
              type="button"
              onclick={() => controller?.selectPayment(paymentID)}
              >ID の支払いを選択</button
            >
            {#if view.loading.includes('payments') || view.loading.includes('payment')}<p
                role="status"
              >
                支払いを読み込んでいます…
              </p>{/if}
            {#if view.payments}{@const payments = view.payments.events.filter(
                (entry) => entry.currency === view.values.currency,
              )}{#if payments.length}<BillingTable
                  entries={payments}
                  onselect={(entry) => controller?.selectPayment(entry.id)}
                  label="この支払いを選ぶ"
                />{:else}<p class="empty">
                  このページに {view.values.currency} の支払いはありません。
                </p>{/if}
              <nav class="pager" aria-label="支払いのページ">
                <button
                  type="button"
                  disabled={view.payments.offset === 0}
                  onclick={() =>
                    controller?.loadPayments(
                      Math.max(0, view.payments!.offset - 20),
                    )}>前の支払い</button
                ><span>全 {view.payments.total} 件</span><button
                  type="button"
                  disabled={view.payments.offset + 20 >= view.payments.total}
                  onclick={() =>
                    controller?.loadPayments(view.payments!.offset + 20)}
                  >次の支払い</button
                >
              </nav>{/if}
          {/if}
        </section>{/if}
    </fieldset>
    {#if view.conflict}<aside class="billing-conflict" role="alert">
        <h3>最新の履歴との競合</h3>
        <p>
          入力と編集開始時の版 {view.baseline?.version} を保持しています。最新の明細と根拠を確認し、変更をやり直してください。
        </p>
        {#if view.latest}<p>
            最新の版 {view.latest.version} · 更新 {processingDate(
              view.latest.updated_at,
            )}
          </p>
          <BillingTable entries={[view.latest]} />
          <details>
            <summary>最新の根拠 {view.latest.sources.length} 件</summary
            >{#each view.latest.sources as s}<p class="billing-id">
                {s.result_id} · イベント {s.event_index + 1}
              </p>
              <EventFields event={s.original_event} />{/each}
          </details>
          <div class="actions">
            <button type="button" onclick={() => controller?.rebase()}
              >入力を保持して最新版で確認し直す</button
            ><button type="button" onclick={() => controller?.discard()}
              >入力を破棄して最新版を開く</button
            >
          </div>{:else}<button
            type="button"
            onclick={() => controller?.refreshLatest()}
            >最新の履歴を再取得</button
          >{/if}
      </aside>{/if}
    {#if !view.conflict}<section class="section">
        <h3>同じ内容の履歴を確認</h3>
        <p class="muted">
          同じ日付・種別・金額・通貨の有効な明細を確認します。確認後に明細や候補が変わった場合は、もう一度確認が必要です。
        </p>
        <button
          type="button"
          disabled={frozen || !valid || view.loading.includes('duplicates')}
          onclick={() => controller?.preview()}>重複候補を確認</button
        >
        {#if view.loading.includes('duplicates')}<p role="status">
            重複候補を確認しています…
          </p>{/if}
        {#if view.duplicates}{#if view.duplicates.total > 0}<p class="notice">
              同じ内容の履歴が {view.duplicates.total} 件あります。自動では統合しません。
            </p>
            <BillingTable
              entries={view.duplicates.events}
              onselect={source ? chooseReuse : undefined}
              label="この履歴に根拠を追加"
              disabled={frozen}
            />
            <nav class="pager" aria-label="重複候補のページ">
              <button
                type="button"
                disabled={frozen || view.duplicates.offset === 0}
                onclick={() =>
                  controller?.preview(
                    Math.max(0, view.duplicates!.offset - 20),
                  )}>前の候補</button
              ><span>全 {view.duplicates.total} 件</span><button
                type="button"
                disabled={frozen ||
                  view.duplicates.offset + 20 >= view.duplicates.total}
                onclick={() =>
                  controller?.preview(view.duplicates!.offset + 20)}
                >次の候補</button
              >
            </nav>
            <label class="check"
              ><input
                type="checkbox"
                checked={view.duplicateConfirmed}
                disabled={frozen}
                onchange={(event) =>
                  controller?.acknowledgeDuplicate(event.currentTarget.checked)}
              />候補とは別の取引であることを確認しました</label
            >{:else}<p class="muted">
              同じ内容の有効な履歴はありません。
            </p>{/if}{/if}
      </section>{/if}
    {#if view.reason === 'refund_conflict'}<p class="notice" role="alert">
        返金の累計または返金元の状態と整合しません。関連する支払い・返金の金額、通貨、状態を履歴一覧で確認してください。返金が紐付く支払いの変更・無効化にも制限があります。
      </p>
      <a
        class="button"
        href={`/subscriptions/${subscription.id}?tab=history&kind=refund`}
        >関連する返金を確認</a
      >{:else if view.reason === 'possible_duplicate'}<p
        class="notice"
        role="alert"
      >
        重複候補が更新されました。候補を再取得し、別の取引か確認し直してください。
      </p>{:else if view.reason === 'source_conflict'}<p
        class="notice"
        role="alert"
      >
        この根拠はすでに別の明細に紐付いています。登録先を確認してください。移す場合は、元の紐付けを明示的に外す必要があります。
      </p>{/if}
    {#if view.error}<p role="alert" class="notice error">
        {processingErrors[view.error]}
      </p>{/if}
    {#if view.uncertain}<p class="notice">
        受付状況を確認できません。金額・日付と要求を保持しています。同じ要求で再確認してください。
      </p>
      <button type="button" disabled={busy} onclick={() => save(true)}
        >同じ要求で再確認</button
      >{:else}<label class="check"
        ><input
          type="checkbox"
          checked={view.confirmed}
          disabled={frozen || view.conflict}
          onchange={(event) => controller?.confirm(event.currentTarget.checked)}
        />{dateLabel}・種別・金額・通貨{view.values.kind === 'refund'
          ? '・返金元'
          : ''}・記録の状態を含め、内容を確認しました</label
      ><button
        class="primary"
        type="submit"
        disabled={frozen ||
          !valid ||
          !view.confirmed ||
          view.conflict ||
          !view.duplicates ||
          (view.duplicates.total > 0 && !view.duplicateConfirmed)}
        >{busy
          ? '保存中…'
          : view.baseline
            ? '履歴の変更を保存'
            : '履歴を記録'}</button
      >{/if}
  </form>
  {#if view.baseline}<BillingSources
      {view}
      {controller}
      sources={subscription.sources}
      {frozen}
      {onSaved}
    />{/if}
  {#if source && !view.baseline}<section class="section">
      <h3>既存の履歴に根拠を追加</h3>
      <p class="muted">
        日付や金額が一致しない履歴も含め、無効な履歴まで確認できます。
      </p>
      <button
        type="button"
        disabled={frozen}
        onclick={() => controller?.loadRecords()}>既存の履歴から選ぶ</button
      >{#if view.loading.includes('records')}<p role="status">
          履歴を読み込んでいます…
        </p>{/if}{#if view.records}{#if view.records.events.length}<BillingTable
            entries={view.records.events}
            onselect={chooseReuse}
            label="この履歴を選ぶ"
            disabled={frozen}
          />{:else}<p class="empty">履歴はありません。</p>{/if}
        <nav class="pager" aria-label="既存履歴のページ">
          <button
            type="button"
            disabled={frozen || view.records.offset === 0}
            onclick={() =>
              controller?.loadRecords(Math.max(0, view.records!.offset - 20))}
            >前の履歴</button
          ><span>全 {view.records.total} 件</span><button
            type="button"
            disabled={frozen || view.records.offset + 20 >= view.records.total}
            onclick={() => controller?.loadRecords(view.records!.offset + 20)}
            >次の履歴</button
          >
        </nav>{/if}
    </section>{/if}
  {#if reuse && source}<aside class="billing-confirm">
      <h3>選択した履歴へ根拠を紐付ける</h3>
      <BillingTable
        entries={[reuse]}
      />{#if view.reason === 'version_conflict'}<p>
          選択した履歴が更新されました。最新版を取得し、紐付け先を確認し直してください。
        </p>
        {#if view.latest?.id === reuse.id}<p>最新の版 {view.latest.version}</p>
          <BillingTable entries={[view.latest]} /><button
            type="button"
            disabled={frozen}
            onclick={() => {
              if (view.latest) chooseReuse(view.latest);
            }}>最新版を紐付け先として選び直す</button
          >{:else}<button
            type="button"
            disabled={frozen}
            onclick={() => controller?.refreshAttachment(reuse!.id)}
            >紐付け先の最新版を再取得</button
          >{/if}{/if}<label class="check"
        ><input
          type="checkbox"
          bind:checked={reuseConfirmed}
          disabled={frozen}
        />入力中の明細を新規作成せず、この履歴へ根拠だけを追加します</label
      ><button
        type="button"
        disabled={frozen || !reuseConfirmed}
        onclick={attachExisting}>既存履歴に根拠を紐付ける</button
      >
    </aside>{/if}
</div>
