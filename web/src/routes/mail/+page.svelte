<script lang="ts">
  import AccessPanel from '#lib/AccessPanel.svelte';
</script>

<svelte:head
  ><title>Cycrio — メール連携</title><meta
    name="description"
    content="自分の Fastmail 接続情報と対象フォルダーを管理します。"
  /></svelte:head
>
<main><AccessPanel mode="mail" /></main>

<style>
  main {
    flex: 1;
    min-width: 0;
    padding: 2rem 0 3rem;
  }
</style>
