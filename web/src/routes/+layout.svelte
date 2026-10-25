<script lang="ts">
  import '#lib/brand.css';
  import { page } from '$app/state';

  let { children } = $props();
</script>

<div
  class="site-shell"
  class:admin-shell={page.url.pathname === '/admin' ||
    page.url.pathname === '/mail'}
>
  <header class="site-header">
    <a class="brand" href="/" aria-label="Cycrio ホーム"
      ><img
        class="brand-mark"
        src="/brand/cycrio-mark-128.png"
        width="44"
        height="44"
        alt=""
      />Cycrio</a
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
    color: var(--color-text);
    background: var(--brand-background);
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
    outline: 3px solid var(--color-action);
    outline-offset: 4px;
  }
  :global(::selection) {
    color: var(--color-text);
    background: var(--brand-accent);
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
    border-bottom: 1px solid var(--color-border);
  }
  .site-shell.admin-shell {
    width: 100%;
    max-width: none;
    padding-inline: clamp(1rem, 2.5vw, 3rem);
  }
  .admin-shell .site-header {
    min-height: 5rem;
  }
  .brand {
    display: flex;
    align-items: center;
    color: var(--color-text);
    font-size: 1.75rem;
    font-weight: 750;
    letter-spacing: -0.06em;
    text-decoration: none;
  }
  .brand-mark {
    width: 2.75rem;
    height: 2.75rem;
    margin-right: 0.5rem;
    object-fit: contain;
    flex-shrink: 0;
  }
  .environment {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    color: var(--color-muted);
    font-size: 0.7rem;
    white-space: nowrap;
  }
  .environment span {
    width: 0.35rem;
    height: 0.35rem;
    border-radius: 50%;
    background: var(--brand-primary);
  }
  .site-footer {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    padding: 1.6rem 0 2rem;
    border-top: 1px solid var(--color-border);
    color: var(--color-subtle);
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
    .brand-mark {
      width: 2.25rem;
      height: 2.25rem;
    }
    .site-footer {
      font-size: 0.62rem;
    }
  }
</style>
