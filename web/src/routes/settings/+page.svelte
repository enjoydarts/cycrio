<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  onMount(() => {
    void goto('/settings/mail', { replaceState: true });
  });
</script>

<svelte:head><title>設定 — Cycrio</title></svelte:head>
<p role="status">メール連携を開いています…</p>
