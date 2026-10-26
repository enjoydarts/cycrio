<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { getAppSession } from '#lib/app-context.ts';
  const session = getAppSession();
  onMount(() => {
    if (session.user?.role === 'platform_admin')
      void goto('/settings/credentials', { replaceState: true });
  });
</script>

<svelte:head
  ><title>管理者設定 — Cycrio</title><meta
    name="robots"
    content="noindex"
  /></svelte:head
>
{#if session.user?.role === 'platform_admin'}<p role="status">
    管理者設定を開いています…
  </p>{:else}<h1>管理者の権限が必要です</h1>
  <p>この設定を利用する権限がありません。</p>
  <a href="/settings/mail">メール連携へ戻る</a>{/if}
