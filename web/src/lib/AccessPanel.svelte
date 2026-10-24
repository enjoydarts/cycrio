<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { createAPI } from './api';
  import { createAuthController, type AuthState } from './auth';

  let { mode = 'home' }: { mode?: 'home' | 'admin' | 'callback' } = $props();
  let authState = $state<AuthState>({ status: 'loading' });
  let signUp = $derived(page.url.searchParams.get('sign_up') === 'true');
  let controller: ReturnType<typeof createAuthController> | undefined;
  const errors = {
    authentication_required:
      'ログインの有効期限が切れた可能性があります。もう一度ログインしてください。',
    access_denied: 'このページを利用する権限がありません。',
    dependency_unavailable:
      'サービスに接続できませんでした。しばらく待ってから、もう一度お試しください。',
  };

  onMount(() => {
    controller = createAuthController(
      createAPI(PUBLIC_API_BASE_URL),
      mode,
      (next) => {
        authState = next;
      },
    );
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
  aria-label={mode === 'admin'
    ? '管理者のアクセス確認'
    : 'アカウントのアクセス確認'}
>
  <div class="card-heading">
    <span class="section-label"
      >{mode === 'admin' ? 'ADMINISTRATION' : 'YOUR ACCOUNT'}</span
    ><span class="small-mark" aria-hidden="true">↗</span>
  </div>
  <div aria-live="polite" aria-atomic="true" class="status-content">
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
      <div class="status-symbol success" aria-hidden="true">✓</div>
      <span class="status-badge"
        >{mode === 'admin' ? '管理者アクセス確認済み' : '利用許可済み'}</span
      >
      <h2>{mode === 'admin' ? '管理者ページ' : 'ログインできました'}</h2>
      <p class="body-copy">
        {mode === 'admin'
          ? 'サーバーで管理者権限を確認しました。'
          : 'サーバーでアカウントの利用許可を確認しました。'}
      </p>
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
      <div class="notice">
        <strong>現在はログイン基盤の開発段階です</strong>
        <p>
          {mode === 'admin'
            ? 'AI 設定・API キー管理・原価集計は、今後実装予定です。'
            : 'メール連携・サブスクリプションの管理機能は、今後実装予定です。'}
        </p>
      </div>
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
  .access-card {
    padding: clamp(1.5rem, 4vw, 2.75rem);
    background: #fff;
    border: 1px solid #dfe7df;
    border-radius: 1.5rem;
    box-shadow: 0 12px 50px #183f3307;
    min-width: 0;
  }
  .card-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 2rem;
  }
  .section-label {
    color: #728078;
    font-size: 0.68rem;
    font-weight: 650;
    letter-spacing: 0.17em;
  }
  .small-mark {
    color: #8b9b90;
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
    color: #617067;
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
    background: #f0f4ed;
    color: #59734a;
    font-size: 1.65rem;
  }
  .status-symbol.success {
    color: #1b5744;
    background: #e8f2e9;
  }
  .status-badge {
    display: inline-block;
    color: #526f45;
    font-size: 0.73rem;
    font-weight: 600;
  }
  .notice {
    border-left: 2px solid #bacbb2;
    padding: 0.15rem 0 0.15rem 1rem;
    margin: 1.8rem 0;
    color: #5a6c5d;
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
    color: white;
    background: #1b5744;
  }
  .primary:hover {
    background: #124332;
  }
  .secondary {
    color: #52685b;
    background: #fff;
    border-color: #dce5dc;
  }
  .secondary:hover {
    background: #f5f8f3;
  }
  .identity,
  .account-details {
    padding: 1rem;
    margin: 1.5rem 0;
    background: #f5f7f2;
    border-radius: 0.7rem;
  }
  .identity span {
    display: block;
    margin-bottom: 0.6rem;
    font-size: 0.75rem;
    color: #647364;
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
    color: #647364;
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
    color: #78857b;
    font-size: 0.72rem;
    line-height: 1.9;
    margin: 1.5rem 0 0;
    padding-top: 1.25rem;
    border-top: 1px solid #edf0e9;
  }
  .text-link {
    display: block;
    margin-top: 1.25rem;
    color: #1b5744;
    font-size: 0.85rem;
    text-align: center;
  }
  .loader {
    width: 1.1rem;
    height: 1.1rem;
    border: 2px solid #c4d1bd;
    border-top-color: #1b5744;
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
