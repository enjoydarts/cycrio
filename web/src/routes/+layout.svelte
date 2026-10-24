<script lang="ts">
  let { children } = $props();
</script>

<div class="site-shell">
  <header class="site-header">
    <a class="brand" href="/" aria-label="Cycrio ホーム"
      ><span class="brand-mark" aria-hidden="true">C</span>cycrio<span
        class="brand-dot"
        aria-hidden="true">.</span
      ></a
    >
    <span class="environment"
      ><span aria-hidden="true"></span>開発プレビュー</span
    >
  </header>
  {@render children()}
  <footer class="site-footer">
    <span>© Cycrio</span><span>暮らしの定期支出に、見通しを。</span>
  </footer>
</div>

<style>
  :global(*) {
    box-sizing: border-box;
  }
  :global(body) {
    margin: 0;
    color: #183f33;
    background: #f5f7f2;
    font-family:
      -apple-system, BlinkMacSystemFont, 'Segoe UI',
      'Hiragino Kaku Gothic ProN', 'Noto Sans JP', sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  :global(button),
  :global(a) {
    -webkit-tap-highlight-color: transparent;
  }
  :global(button:focus-visible),
  :global(a:focus-visible) {
    outline: 3px solid #a6bc6e;
    outline-offset: 4px;
  }
  :global(::selection) {
    background: #d3e5ce;
  }
  .site-shell {
    max-width: 1280px;
    min-height: 100svh;
    margin: 0 auto;
    padding: 0 clamp(1.25rem, 5vw, 4.5rem);
    display: flex;
    flex-direction: column;
  }
  .site-header {
    min-height: 6.5rem;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    border-bottom: 1px solid #e1e7dd;
  }
  .brand {
    display: flex;
    align-items: center;
    color: #183f33;
    font-size: 1.7rem;
    font-weight: 750;
    letter-spacing: -0.06em;
    text-decoration: none;
  }
  .brand-mark {
    display: grid;
    width: 2.15rem;
    height: 2.15rem;
    place-items: center;
    margin-right: 0.65rem;
    border-radius: 0.7rem;
    background: #183f33;
    color: white;
    font-size: 1.25rem;
    font-weight: 600;
    letter-spacing: -0.08em;
    padding-right: 0.1rem;
  }
  .brand-dot {
    color: #92aa61;
  }
  .environment {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    color: #6b786b;
    font-size: 0.7rem;
    white-space: nowrap;
  }
  .environment span {
    width: 0.35rem;
    height: 0.35rem;
    border-radius: 50%;
    background: #90a667;
  }
  .site-footer {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    padding: 1.6rem 0 2rem;
    border-top: 1px solid #e1e7dd;
    color: #7d877b;
    font-size: 0.67rem;
    line-height: 1.8;
  }
  @media (max-width: 600px) {
    .site-header {
      min-height: 5rem;
    }
    .brand {
      font-size: 1.5rem;
    }
    .site-footer {
      font-size: 0.62rem;
    }
  }
</style>
