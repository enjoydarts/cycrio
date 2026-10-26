<script lang="ts">
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
  let registrations = $state<AnalysisResult['registrations']>([]),
    action = $state<{ index: number; kind: 'create' | 'attach' } | null>(null),
    busy = $state(false);
  $effect(() => {
    registrations = result.registrations;
  });
  function changed(value: boolean) {
    busy = value;
    onBusyChange(value);
  }
  function saved(index: number, s: Subscription) {
    registrations = [
      ...registrations.filter((r) => r.event_index !== index),
      { event_index: index, subscription_id: s.id },
    ];
    action = null;
    onRegistered({ ...result, registrations });
  }
</script>

<div class="daily">
  <p class="muted">
    {result.account_label} · {processingDate(result.finished_at)}（日本時間）
  </p>
  {#if result.state === 'succeeded'}<p class="notice">
      AI
      抽出・未確認。抽出したイベントごとに内容を確認して登録します。契約は自動で作成・更新しません。
    </p>
    <p>{registrations.length} / {result.events.length} イベントを登録済み</p>
    {#each result.events as event, index}{@const registration =
        registrations.find((r) => r.event_index === index)}
      <section class="section">
        <h3>{index + 1}. {event.service} · {eventNames[event.event_type]}</h3>
        <EventFields {event} />
        {#if registration}<a
            class="button"
            href={`/subscriptions/${registration.subscription_id}`}
            >登録済みのサブスクを見る</a
          >{:else if action?.index === index}{#if action.kind === 'create'}<h3>
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
              onBusyChange={changed}
              onSaved={(s) => saved(index, s)}
            />{/if}{:else}<div class="actions">
            <button
              class="primary"
              disabled={busy}
              onclick={() => (action = { index, kind: 'create' })}
              >新規サブスクに登録</button
            ><button
              disabled={busy}
              onclick={() => (action = { index, kind: 'attach' })}
              >既存のサブスクに紐付ける</button
            >
          </div>{/if}
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
