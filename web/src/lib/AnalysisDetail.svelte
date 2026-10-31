<script lang="ts">
  import Button from './Button.svelte';
  import ActionLink from './ActionLink.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import type { AnalysisResult } from './analysis-api.ts';
  import type { Subscription } from './subscriptions-api.ts';
  import type { SubscriptionDrafts } from './subscriptions-controller.ts';
  import type { MailAccount } from './mail-api.ts';
  import type { ProcessingRequests } from './mail-processing-controller.ts';
  import { processingDate, processingReasons } from './mail-processing-text.ts';
  import { eventNames } from './daily-text.ts';
  import EventFields from './EventFields.svelte';
  import SubscriptionForm from './SubscriptionForm.svelte';
  import AnalysisAttach from './AnalysisAttach.svelte';
  import AnalysisStart from './AnalysisStart.svelte';
  import AnalysisResultStatus from './AnalysisResultStatus.svelte';
  import { acknowledgeAnalysisRegistration } from './analysis-controller.ts';
  let {
    result,
    active,
    accounts,
    requests,
    drafts,
    onRegistered,
    onAccepted,
    onBackToResults,
    onBusyChange = () => {},
  }: {
    result: AnalysisResult;
    active: boolean;
    accounts: MailAccount[];
    requests: ProcessingRequests;
    drafts: SubscriptionDrafts;
    onRegistered: (result: AnalysisResult) => void;
    onAccepted: () => void;
    onBackToResults: () => void;
    onBusyChange?: (busy: boolean) => void;
  } = $props();
  let registrations = $derived(result.registrations),
    action = $state<{ index: number; kind: 'create' | 'attach' } | null>(null),
    busy = $state(false),
    registrationError = $state(false);
  function changed(value: boolean) {
    busy = value;
    onBusyChange(value);
  }
  function saved(index: number, s: Subscription) {
    const acknowledged = acknowledgeAnalysisRegistration(result, s, index);
    if (!acknowledged) {
      registrationError = true;
      action = null;
      return;
    }
    registrations = acknowledged.registrations;
    registrationError = false;
    action = null;
    onRegistered(acknowledged);
  }
  const automationReasons: Record<string, string> = {
    ambiguous_identity:
      '登録先のサブスクを一意に特定できませんでした。新規登録か既存のサブスクへの紐付けを選んでください。',
    invalid_facts:
      '契約に必要な情報を確定できませんでした。内容を確認してください。',
    recording_failed:
      '自動登録を完了できませんでした。現在の登録状況を確認してください。',
  };
  const billingReasons: Record<string, string> = {
    missing_facts: '明細に必要な金額・通貨・日付などが不足しています。',
    invalid_facts: '明細の情報を確定できませんでした。',
    duplicate_candidate: '重複している可能性がある明細があります。',
    refund_target_ambiguous:
      '返金に対応する支払いを一意に特定できませんでした。',
    source_conflict: 'この根拠は別の履歴と競合しています。',
    recording_failed: '明細の自動記録を完了できませんでした。',
  };
</script>

<div class="daily">
  {#if registrationError}<p class="notice" role="alert">
      保存は受け付けられましたが、この解析結果との紐付けを確認できません。<Button
        wrap
        onclick={onBackToResults}>結果一覧で最新の登録状況を確認</Button
      >
    </p>{/if}
  <p class="muted">
    {result.account_label} · {processingDate(result.finished_at)}（日本時間）
  </p>
  {#if result.state === 'succeeded'}<AnalysisResultStatus {result} />
    {#each result.events as event, index}{@const registration =
        registrations.find((r) => r.event_index === index)}
      {@const contractOutcome = result.automation?.find(
        (a) => a.event_index === index,
      )}
      {@const financialOutcome = result.billing_automation?.find(
        (a) => a.event_index === index,
      )}
      {@const manualRegistration = !registration}
      {@const financialSourceAction =
        !!financialOutcome &&
        (['needs_review', 'failed'].includes(financialOutcome.state) ||
          (financialOutcome.state === 'recorded' &&
            !!financialOutcome.entry_id))}
      <section class="section">
        <h3>{index + 1}. {event.service} · {eventNames[event.event_type]}</h3>
        {#if contractOutcome && ['needs_review', 'failed'].includes(contractOutcome.state) && !registration}<p
            class="notice"
          >
            {automationReasons[contractOutcome.reason] ??
              '登録内容を確認してください。'}
          </p>{/if}
        {#if financialOutcome && ['needs_review', 'failed'].includes(financialOutcome.state)}<p
            class="notice"
          >
            {billingReasons[financialOutcome.reason] ??
              '明細の内容を確認してください。'}{registration
              ? '支払・請求履歴から内容を確認して記録できます。'
              : 'サブスクを登録した後、支払・請求履歴で内容を確認して記録できます。'}
          </p>{/if}
        <EventFields {event} />
        {#if registration}<ActionLink
            wrap
            class={['button']}
            href={`/subscriptions/${registration.subscription_id}`}
            >登録済みのサブスクを見る</ActionLink
          >
          <ActionLink
            wrap
            class={['button']}
            href={`/subscriptions/${registration.subscription_id}?tab=history${financialSourceAction ? `&source_result_id=${result.id}&source_event_index=${index}` : ''}`}
            >支払・請求履歴を見る</ActionLink
          >{:else if manualRegistration && action?.index === index}{#if action.kind === 'create'}<h3
            >
              新規サブスクに登録
            </h3>
            <SubscriptionForm
              {event}
              {drafts}
              {onBackToResults}
              source={{ result_id: result.id, event_index: index }}
              onBusyChange={changed}
              onSaved={(s) => saved(index, s)}
            />{:else}<AnalysisAttach
              {result}
              {index}
              {onBackToResults}
              onBusyChange={changed}
              onSaved={(s) => saved(index, s)}
            />{/if}{:else if manualRegistration}<ActionGroup
            class={['actions', 'part-AnalysisDetail-actions']}
          >
            <Button
              wrap
              variant="primary"
              class={['primary']}
              disabled={busy}
              onclick={() => (action = { index, kind: 'create' })}
              >新規サブスクに登録</Button
            ><Button
              wrap
              disabled={busy}
              onclick={() => (action = { index, kind: 'attach' })}
              >既存のサブスクに紐付ける</Button
            >
          </ActionGroup>{:else}<p class="muted">
            {contractOutcome?.state === 'registered'
              ? 'この情報には現在の登録先がありません。'
              : result.automation_state === 'not_applicable'
                ? 'この情報は自動登録の対象外です。'
                : '自動登録待ちです。処理が進むと登録先が表示されます。'}
          </p>{/if}
      </section>{/each}
  {:else if result.state === 'not_related'}<p class="empty">
      サブスクに関連しないと判定されました。抽出したイベントはありません。
    </p>{:else}<h3>
      {result.state === 'unknown' ? '解析結果が不明です' : '解析に失敗しました'}
    </h3>
    <p class="notice">
      {processingReasons[result.error_code] || '結果を確認できませんでした。'}
    </p>
    <AnalysisStart
      {active}
      {accounts}
      {requests}
      retry={result}
      {onAccepted}
      {onBusyChange}
    />{/if}
</div>
