<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { createAPI } from './api';
  import { createAuthController, type AuthState } from './auth';
  import AdminCredentials from './AdminCredentials.svelte';
  import ModelCatalog from './ModelCatalog.svelte';
  import AIModelSettings from './AIModelSettings.svelte';
  import { createCatalogAPI } from './catalog-api';

  let { mode = 'home' }: { mode?: 'home' | 'admin' | 'callback' } = $props();
  let authState = $state<AuthState>({ status: 'loading' });
  let signUp = $derived(page.url.searchParams.get('sign_up') === 'true');
  let controller: ReturnType<typeof createAuthController> | undefined;
  const api = createAPI(PUBLIC_API_BASE_URL);
  const catalogAPI = createCatalogAPI(PUBLIC_API_BASE_URL);
  let adminPanel = $state<'credentials' | 'catalog' | 'settings'>(
    'credentials',
  );
  const errors = {
    authentication_required:
      'ログインの有効期限が切れた可能性があります。もう一度ログインしてください。',
    access_denied: 'このページを利用する権限がありません。',
    dependency_unavailable:
      'サービスに接続できませんでした。しばらく待ってから、もう一度お試しください。',
  };

  onMount(() => {
    controller = createAuthController(api, mode, (next) => {
      authState = next;
    });
    void controller.start();
    return () => controller?.dispose();
  });

  function mountAuth(node: HTMLDivElement) {
    const unmount = controller?.mountAuth(node, signUp);
    return { destroy: () => unmount?.() };
  }

  function retry() {
    void controller?.start();
  }
</script>

<section
  class="access-card"
  class:admin-workspace={mode === 'admin' && authState.status === 'allowed'}
  aria-label={mode === 'admin'
    ? '管理者のアクセス確認'
    : 'アカウントのアクセス確認'}
>
  <div class="card-heading">
    <span class="section-label"
      >{mode === 'admin' ? 'ADMINISTRATION' : 'YOUR ACCOUNT'}</span
    ><span class="small-mark" aria-hidden="true">↗</span>
  </div>
  <div
    aria-live={authState.status === 'allowed' ? 'off' : 'polite'}
    aria-atomic="true"
    class="status-content"
  >
    {#if authState.status === 'loading' || authState.status === 'checking' || authState.status === 'redirecting'}
      <div class="status-symbol" aria-hidden="true">
        <span class="loader"></span>
      </div>
      <h2>
        {authState.status === 'redirecting'
          ? 'ログインを完了しています'
          : 'アクセスを確認しています'}
      </h2>
      <p class="body-copy">
        {authState.status === 'loading'
          ? 'ログインの設定を読み込んでいます。'
          : 'しばらくお待ちください。'}
      </p>
    {:else if authState.status === 'unconfigured'}
      <div class="status-symbol" aria-hidden="true">◌</div>
      <span class="status-badge">セットアップ待ち</span>
      <h2>ログインの準備中です</h2>
      <p class="body-copy">
        認証サービスの設定がまだ完了していません。管理者の設定完了後に、Google
        アカウントでログインできます。
      </p>
      <div class="notice">
        <strong>初期設定が必要です</strong>
        <p>
          管理者が認証設定と利用者の許可を行ってから、ご利用を開始できます。
        </p>
      </div>
      <button class="button primary" onclick={retry}
        >設定を再確認する <span aria-hidden="true">↻</span></button
      >
    {:else if authState.status === 'signed_out'}
      <h2>{mode === 'admin' ? '管理者としてログイン' : 'Cycrio へようこそ'}</h2>
      <p class="body-copy">
        Google アカウントでログインしてください。<br
        />ご利用には管理者の許可が必要です。
      </p>
    {:else if authState.status === 'pending'}
      <div class="status-symbol" aria-hidden="true">◷</div>
      <span class="status-badge">ログイン済み</span>
      <h2>利用許可をお待ちください</h2>
      <p class="body-copy">
        Google
        アカウントの確認が完了しました。次に、管理者による利用許可が必要です。
      </p>
      <div class="identity">
        <span>管理者にお伝えいただくユーザー ID</span><code
          >{authState.clerkUserId}</code
        >
      </div>
      <button class="button primary" onclick={() => controller?.refresh()}
        >利用許可を再確認する <span aria-hidden="true">↻</span></button
      >
      <button class="button secondary" onclick={() => controller?.signOut()}
        >ログアウト</button
      >
    {:else if authState.status === 'denied'}
      <div class="status-symbol" aria-hidden="true">⊘</div>
      <h2>管理者の権限が必要です</h2>
      <p class="body-copy">
        このアカウントは管理者ページを利用できません。権限については管理者へお問い合わせください。
      </p>
      <a class="button primary" href="/"
        >ホームに戻る <span aria-hidden="true">→</span></a
      >
      <button class="button secondary" onclick={() => controller?.signOut()}
        >ログアウト</button
      >
    {:else if authState.status === 'allowed'}
      {#if mode !== 'admin'}
        <div class="status-symbol success" aria-hidden="true">✓</div>
        <span class="status-badge">利用許可済み</span>
        <h2>ログインできました</h2>
        <p class="body-copy">サーバーでアカウントの利用許可を確認しました。</p>
      {:else}<span class="status-badge">管理者アクセス確認済み</span>{/if}
      <details class="login-details" open={mode !== 'admin'}>
        <summary>ログイン情報</summary>
        <dl class="account-details">
          <div>
            <dt>ユーザー ID</dt>
            <dd><code>{authState.user.id}</code></dd>
          </div>
          <div>
            <dt>権限</dt>
            <dd>
              {authState.user.role === 'platform_admin'
                ? '管理者'
                : '一般ユーザー'}
            </dd>
          </div>
        </dl>
      </details>
      {#if mode === 'admin'}
        <nav class="admin-navigation" aria-label="管理機能">
          <button
            aria-pressed={adminPanel === 'credentials'}
            onclick={() => {
              adminPanel = 'credentials';
            }}>API キー</button
          >
          <button
            aria-pressed={adminPanel === 'catalog'}
            onclick={() => {
              adminPanel = 'catalog';
            }}>モデルカタログ</button
          >
          <button
            aria-pressed={adminPanel === 'settings'}
            onclick={() => {
              adminPanel = 'settings';
            }}>AI 設定</button
          >
        </nav>
        <div
          role="region"
          aria-label={adminPanel === 'credentials'
            ? 'API キー管理'
            : adminPanel === 'catalog'
              ? 'モデルカタログ管理'
              : 'AI 設定管理'}
        >
          {#if adminPanel === 'credentials'}<AdminCredentials
              {api}
              token={async () => (await controller?.getToken()) ?? null}
              onDenied={() => {
                void controller?.refresh();
              }}
            />{:else if adminPanel === 'catalog'}<ModelCatalog
              api={catalogAPI}
              token={async () => (await controller?.getToken()) ?? null}
              onDenied={() => {
                void controller?.refresh();
              }}
            />{:else}<AIModelSettings
              api={catalogAPI}
              token={async () => (await controller?.getToken()) ?? null}
              onDenied={() => {
                void controller?.refresh();
              }}
            />{/if}
        </div>
      {:else}<div class="notice">
          <strong>現在はログイン基盤の開発段階です</strong>
          <p>メール連携・サブスクリプションの管理機能は、今後実装予定です。</p>
        </div>{/if}
      {#if mode === 'home' && authState.user.role === 'platform_admin'}<a
          class="button primary"
          href="/admin">管理者ページへ <span aria-hidden="true">→</span></a
        >{:else if mode === 'admin'}<a class="button primary" href="/"
          >ホームに戻る <span aria-hidden="true">→</span></a
        >{/if}
      <button class="button secondary" onclick={() => controller?.signOut()}
        >ログアウト</button
      >
    {:else if authState.status === 'error'}
      <div class="status-symbol" aria-hidden="true">!</div>
      <h2>確認できませんでした</h2>
      <p class="body-copy">{errors[authState.code]}</p>
      <button class="button primary" onclick={retry}
        >もう一度試す <span aria-hidden="true">↻</span></button
      >
      {#if authState.signedIn}<button
          class="button secondary"
          onclick={() => controller?.signOut()}>ログアウトしてやり直す</button
        >{/if}
      {#if mode === 'callback'}<a class="text-link" href="/"
          >ログイン画面に戻る</a
        >{/if}
    {/if}
  </div>
  {#if authState.status === 'signed_out'}
    {#key signUp}<div class="clerk-host" use:mountAuth></div>{/key}
    <p class="login-note">
      ログインは本人確認のために使用します。<br />Gmail
      などのメール取得の許可は別途行います。
    </p>
  {/if}
</section>

<style>
  .admin-navigation {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-top: 1.5rem;
  }
  .admin-navigation button {
    flex: 1 1 6rem;
    min-width: 0;
    padding: 0.65rem 0.4rem;
    border: 1px solid var(--color-border);
    border-radius: 0.5rem;
    background: var(--color-surface);
    color: var(--color-action);
    font: inherit;
    font-size: 0.8rem;
    cursor: pointer;
  }
  .admin-navigation button[aria-pressed='true'] {
    background: var(--color-action);
    color: var(--color-surface);
  }
  .admin-navigation button:focus-visible {
    outline: 2px solid var(--color-action-hover);
    outline-offset: 3px;
  }
  .access-card {
    padding: clamp(1.5rem, 4vw, 2.75rem);
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: 1.5rem;
    box-shadow: 0 12px 50px var(--color-shadow);
    min-width: 0;
  }
  .admin-workspace {
    padding: clamp(1rem, 2vw, 2rem);
    border-radius: 1rem;
  }
  .admin-workspace .card-heading {
    margin-bottom: 0.75rem;
  }
  .admin-workspace .admin-navigation {
    margin-top: 1rem;
  }
  .admin-workspace .admin-navigation button {
    flex: 0 1 auto;
    padding-inline: 1.25rem;
    font-size: 0.88rem;
  }
  .admin-workspace .button {
    display: inline-flex;
    width: auto;
    min-width: 8rem;
    padding-inline: 1rem;
    margin-right: 0.5rem;
  }
  .login-details {
    color: var(--color-muted);
    margin-top: 0.5rem;
    font-size: 0.8rem;
  }
  .login-details summary {
    cursor: pointer;
  }
  .admin-workspace .account-details {
    margin: 0.75rem 0;
    display: flex;
    flex-wrap: wrap;
    gap: 1rem 2rem;
  }
  .admin-workspace .account-details div + div {
    margin-top: 0;
  }
  .access-card:not(.admin-workspace) {
    max-width: 35rem;
    margin-inline: auto;
  }
  .card-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 2rem;
  }
  .section-label {
    color: var(--color-muted);
    font-size: 0.68rem;
    font-weight: 650;
    letter-spacing: 0.17em;
  }
  .small-mark {
    color: var(--color-action);
    font-size: 1.5rem;
  }
  h2 {
    margin: 0.8rem 0;
    font-size: clamp(1.3rem, 3.5vw, 1.75rem);
    font-weight: 650;
    letter-spacing: -0.025em;
    line-height: 1.6;
  }
  .body-copy {
    margin: 0 0 1.6rem;
    color: var(--color-muted);
    line-height: 1.9;
    font-size: 0.9rem;
  }
  .status-symbol {
    display: grid;
    width: 3rem;
    height: 3rem;
    place-items: center;
    margin-bottom: 1.6rem;
    border-radius: 50%;
    background: var(--brand-background);
    color: var(--color-action);
    font-size: 1.65rem;
  }
  .status-symbol.success {
    color: var(--color-action-hover);
    background: var(--brand-accent);
  }
  .status-badge {
    display: inline-block;
    color: var(--color-action);
    font-size: 0.73rem;
    font-weight: 600;
  }
  .notice {
    border-left: 2px solid var(--brand-accent);
    padding: 0.15rem 0 0.15rem 1rem;
    margin: 1.8rem 0;
    color: var(--color-muted);
    font-size: 0.8rem;
    line-height: 1.85;
  }
  .notice strong {
    font-weight: 600;
  }
  .notice p {
    margin: 0.3rem 0 0;
  }
  .button {
    display: flex;
    width: 100%;
    min-height: 3rem;
    align-items: center;
    justify-content: center;
    gap: 0.8rem;
    margin-top: 0.75rem;
    border: 1px solid transparent;
    border-radius: 0.7rem;
    font-family: inherit;
    font-size: 0.86rem;
    font-weight: 600;
    line-height: 1.6;
    text-decoration: none;
    cursor: pointer;
    transition: background 0.15s;
  }
  .primary {
    color: var(--color-surface);
    background: var(--color-action);
  }
  .primary:hover {
    background: var(--color-action-hover);
  }
  .secondary {
    color: var(--color-action);
    background: var(--color-surface);
    border-color: var(--color-border);
  }
  .secondary:hover {
    background: var(--color-surface-soft);
  }
  .identity,
  .account-details {
    padding: 1rem;
    margin: 1.5rem 0;
    background: var(--color-surface-soft);
    border-radius: 0.7rem;
  }
  .identity span {
    display: block;
    margin-bottom: 0.6rem;
    font-size: 0.75rem;
    color: var(--color-muted);
  }
  code {
    overflow-wrap: anywhere;
    font-size: 0.82rem;
  }
  .account-details {
    font-size: 0.8rem;
  }
  .account-details div + div {
    margin-top: 1rem;
  }
  dt {
    color: var(--color-muted);
    margin-bottom: 0.3rem;
  }
  dd {
    margin: 0;
  }
  .clerk-host {
    width: 100%;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .clerk-host :global(.cl-rootBox),
  .clerk-host :global(.cl-cardBox) {
    width: 100%;
    min-width: 0;
    max-width: 100%;
  }
  .clerk-host :global(.cl-card) {
    width: 100%;
    min-width: 0;
    padding: 1rem 0;
  }
  .login-note {
    color: var(--color-subtle);
    font-size: 0.72rem;
    line-height: 1.9;
    margin: 1.5rem 0 0;
    padding-top: 1.25rem;
    border-top: 1px solid var(--color-border);
  }
  .text-link {
    display: block;
    margin-top: 1.25rem;
    color: var(--color-action);
    font-size: 0.85rem;
    text-align: center;
  }
  .loader {
    width: 1.1rem;
    height: 1.1rem;
    border: 2px solid var(--brand-accent);
    border-top-color: var(--color-action);
    border-radius: 50%;
    animation: spin 1s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .loader {
      animation: none;
    }
  }
</style>
