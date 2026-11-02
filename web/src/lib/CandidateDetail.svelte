<script lang="ts">
  import CandidateReview from './CandidateReview.svelte';
  import type { Candidate } from './approval-api.ts';
  import type { SubscriptionDrafts } from './subscriptions-controller.ts';
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
</script>

{#key `${candidate.result_id}/${candidate.event_index}`}
  <CandidateReview
    {candidate}
    {drafts}
    {onDone}
    {onBusyChange}
    {initialAction}
  />
{/key}
