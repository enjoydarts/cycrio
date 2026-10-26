<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  onMount(() => {
    void goto('/subscriptions', { replaceState: true });
  });
</script>

<svelte:head
  ><title>Cycrio</title><meta
    name="description"
    content="メールとつながるサブスクリプション管理サービス"
  /></svelte:head
>
<p role="status">サブスクを開いています…</p>
