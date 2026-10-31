<script lang="ts">
  import Button from './Button.svelte';
  import { untrack } from 'svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { ApiError } from './api.ts';
  import { createApprovalAPI, type Candidate } from './approval-api.ts';
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
  let action = $state<'create' | 'attach' | 'dismiss' | null>(
      untrack(() => initialAction),
    ),
    busy = $state(false),
    uncertain = $state(false),
    error = $state<string | null>(null);
  function frozen(value: boolean) {
    busy = value;
    onBusyChange(value);
  }
  async function dismiss() {
    if (busy) return;
    frozen(true);
    error = null;
    try {
      await api.dismiss(candidate, session.token);
      uncertain = false;
      onDone();
    } catch (e) {
      const code = e instanceof ApiError ? e.code : 'dependency_unavailable';
      error = processingErrors[code];
      uncertain = code === 'dependency_unavailable';
      if (['authentication_required', 'access_denied'].includes(code))
        session.onDenied();
    } finally {
      busy = false;
      onBusyChange(uncertain);
    }
  }
</script>

<div class="daily">
  <p class="muted">{candidate.account_label}</p>
  {#if action === 'create'}
    <SubscriptionForm
      event={candidate.event}
      source={{
        result_id: candidate.result_id,
        event_index: candidate.event_index,
      }}
      {drafts}
      onSaved={onDone}
      onBackToResults={onDone}
      onFrozen={(state) => frozen(state.status === 'saving' || state.uncertain)}
    />
  {:else if action === 'attach'}
    <EventFields event={candidate.event} />
    <AnalysisAttach
      result={{ id: candidate.result_id }}
      index={candidate.event_index}
      onSaved={onDone}
      onBackToResults={onDone}
      onBusyChange={frozen}
    />
  {:else if action === 'dismiss'}
    <p>
      「{candidate.event
        .service}」を確認待ちから除外します。登録済みのサブスクは変更しません。
    </p>
    {#if error}<p role="alert" class="notice">{error}</p>{/if}
    <Button wrap type="button" disabled={busy} onclick={dismiss}
      >{uncertain ? '同じ要求で除外を確認' : 'この候補を除外する'}</Button
    >
  {:else}
    <EventFields event={candidate.event} />
    <p class="notice">
      抽出された金額・周期を確認してから登録してください。既存の契約から届いた情報なら、そのサブスクに紐付けられます。
    </p>
    <ActionGroup class={['actions', 'part-CandidateDetail-actions']}>
      <Button
        wrap
        variant="primary"
        class={['primary']}
        onclick={() => (action = 'create')}>新規サブスクに登録</Button
      ><Button wrap onclick={() => (action = 'attach')}
        >既存のサブスクに紐付ける</Button
      ><Button wrap type="button" onclick={() => (action = 'dismiss')}
        >不要として除外</Button
      >
    </ActionGroup>
  {/if}
  {#if action}<ActionGroup
      class={[
        'actions back',
        'part-CandidateDetail-actions',
        'part-CandidateDetail-back',
      ]}
    >
      <Button
        wrap
        disabled={busy || uncertain}
        onclick={() => {
          action = null;
          error = null;
        }}>選択に戻る</Button
      >
    </ActionGroup>{/if}
</div>

<style>
  :global(.part-CandidateDetail-back) {
    margin-top: 24px;
  }
</style>
