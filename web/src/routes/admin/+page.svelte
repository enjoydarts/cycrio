<script lang="ts">
  import AccessPanel from '#lib/AccessPanel.svelte';
</script>

<svelte:head
  ><title>管理者ページ — Cycrio</title><meta
    name="robots"
    content="noindex"
  /></svelte:head
>
<main>
  <a class="back" href="/">← ホーム</a>
  <p class="eyebrow">CYCRIO / ADMINISTRATION</p>
  <h1>管理者ページ</h1>
  <p class="description">
    管理者専用の領域です。アクセス時に権限を確認します。
  </p>
  <AccessPanel mode="admin" />
</main>

<style>
  main {
    flex: 1;
    width: 100%;
    max-width: 35rem;
    margin: 0 auto;
    padding: 2.5rem 0 4rem;
  }
  .back {
    color: #65785f;
    text-decoration: none;
    font-size: 0.8rem;
  }
  .eyebrow {
    color: #7a8872;
    font-size: 0.65rem;
    letter-spacing: 0.12em;
    margin-top: 2.5rem;
  }
  h1 {
    font-size: 1.8rem;
    letter-spacing: -0.03em;
    margin: 1rem 0;
    font-weight: 600;
  }
  .description {
    font-size: 0.85rem;
    color: #718069;
    margin-bottom: 2rem;
    line-height: 1.9;
  }
</style>
