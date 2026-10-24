<svelte:head>
  <title>Cycrio</title>
  <meta
    name="description"
    content="メール連携型サブスクリプション管理サービス Cycrio"
  />
</svelte:head>

<main>
  <div class="mark" aria-hidden="true">C</div>
  <p class="eyebrow">CYCRIO</p>
  <h1>サブスクリプションを、<br />もっと見渡しやすく。</h1>
  <p class="description">メールとつながるサブスクリプション管理サービス。</p>
  <p class="status">現在、開発環境を準備しています。</p>
</main>

<style>
  :global(body) {
    margin: 0;
    background: #f5f7f4;
    color: #183f33;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  }

  main {
    box-sizing: border-box;
    display: flex;
    min-height: 100svh;
    align-items: center;
    justify-content: center;
    flex-direction: column;
    padding: 3rem 1.5rem;
    text-align: center;
  }

  .mark {
    display: grid;
    width: 4rem;
    height: 4rem;
    place-items: center;
    border-radius: 1.25rem;
    background: #183f33;
    color: white;
    font-size: 2rem;
    font-weight: 700;
  }

  .eyebrow {
    margin: 1.5rem 0 1rem;
    font-size: 0.85rem;
    font-weight: 700;
    letter-spacing: 0.2em;
  }

  h1 {
    margin: 0;
    font-size: clamp(1.7rem, 5vw, 3rem);
    line-height: 1.6;
    letter-spacing: 0.025em;
  }

  .description {
    max-width: 32rem;
    margin: 1.5rem 0;
    color: #4f645b;
    line-height: 1.9;
  }

  .status {
    padding: 0.75rem 1.1rem;
    border: 1px solid #c6d6cb;
    border-radius: 0.7rem;
    font-size: 0.85rem;
    line-height: 1.6;
  }
</style>
