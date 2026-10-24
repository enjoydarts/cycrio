<script lang="ts">
  import AccessPanel from '#lib/AccessPanel.svelte';
</script>

<svelte:head
  ><title>ログインを確認 — Cycrio</title><meta
    name="robots"
    content="noindex"
  /></svelte:head
>
<main><AccessPanel mode="callback" /></main>

<style>
  main {
    flex: 1;
    display: grid;
    align-content: center;
    width: 100%;
    max-width: 32rem;
    margin: 0 auto;
    padding: 3rem 0;
  }
</style>
