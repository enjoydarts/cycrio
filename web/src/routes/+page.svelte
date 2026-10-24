<script lang="ts">
  import AccessPanel from '#lib/AccessPanel.svelte';
</script>

<svelte:head>
  <title>Cycrio — ログイン</title>
  <meta
    name="description"
    content="メールとつながるサブスクリプション管理サービス Cycrio。Google アカウントでログイン。"
  />
</svelte:head>

<main>
  <div class="introduction">
    <p class="eyebrow"><span></span> A CLEARER VIEW OF YOUR SUBSCRIPTIONS</p>
    <h1>毎月の支出に、<br />見通しを。</h1>
    <p class="lead">メールとつながる、<br />サブスクリプション管理サービス。</p>
    <div class="service-note">
      <span aria-hidden="true">↻</span>
      <p>
        Cycrio は現在開発中です。<br
        />まずは、安心して使えるログインの入口から。
      </p>
    </div>
    <div class="access-note">
      <span class="line"></span>
      <p>
        Google で本人確認<br /><span>管理者の許可後にご利用いただけます。</span>
      </p>
    </div>
  </div>
  <AccessPanel />
</main>

<style>
  main {
    flex: 1;
    display: grid;
    grid-template-columns: 1fr 1fr;
    align-items: center;
    gap: clamp(2.5rem, 6vw, 6rem);
    padding: 4.5rem 0 5rem;
  }
  .introduction {
    padding: 0.5rem 0;
  }
  .eyebrow {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin: 0 0 2.5rem;
    font-size: 0.57rem;
    font-weight: 600;
    letter-spacing: 0.09em;
    color: #687c61;
  }
  .eyebrow span {
    display: block;
    width: 1.6rem;
    height: 1px;
    background: #8ca26c;
    flex-shrink: 0;
  }
  h1 {
    margin: 0;
    font-size: clamp(2.5rem, 4.6vw, 4rem);
    font-weight: 600;
    letter-spacing: -0.045em;
    line-height: 1.5;
  }
  .lead {
    margin: 1.75rem 0 2rem;
    color: #637462;
    font-size: 0.98rem;
    line-height: 2;
  }
  .service-note {
    display: flex;
    align-items: center;
    gap: 0.9rem;
    max-width: 24rem;
    padding-top: 1.5rem;
    border-top: 1px solid #dce5d7;
  }
  .service-note > span {
    color: #7e9664;
    font-size: 2rem;
    font-weight: 300;
  }
  .service-note p {
    margin: 0;
    color: #77816f;
    font-size: 0.74rem;
    line-height: 1.9;
  }
  .access-note {
    display: flex;
    align-items: center;
    gap: 0.8rem;
    margin-top: 3.5rem;
  }
  .access-note .line {
    width: 0.25rem;
    height: 2rem;
    background: #a5b786;
    border-radius: 1px;
  }
  .access-note p {
    margin: 0;
    font-size: 0.75rem;
    font-weight: 600;
    line-height: 1.9;
  }
  .access-note p span {
    color: #85907f;
    font-size: 0.68rem;
    font-weight: 400;
  }
  @media (max-width: 760px) {
    main {
      grid-template-columns: 1fr;
      padding: 2.7rem 0 3rem;
      gap: 2.5rem;
    }
    h1 {
      font-size: clamp(2.4rem, 8vw, 3.5rem);
    }
    .eyebrow {
      margin-bottom: 1.5rem;
    }
    .lead {
      margin: 1rem 0 1.5rem;
    }
    .access-note {
      margin-top: 1.5rem;
    }
    .service-note {
      max-width: none;
    }
  }
</style>
