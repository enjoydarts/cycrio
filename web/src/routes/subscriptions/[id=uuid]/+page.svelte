<script lang="ts">
  import { page } from '$app/state';
  import SubscriptionDetail from '#lib/SubscriptionDetail.svelte';
</script>

<svelte:head
  ><title>サブスクの詳細 — Cycrio</title><meta
    name="robots"
    content="noindex"
  /></svelte:head
>
{#key page.params.id}<SubscriptionDetail id={page.params.id ?? ''} />{/key}
