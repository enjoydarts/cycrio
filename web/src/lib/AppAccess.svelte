<script lang="ts">
  import type { AuthState } from './auth.ts';
  let {
    state,
    retry,
    signOut,
    mountAuth,
    signUp = false,
  }: {
    state: AuthState;
    retry: () => void;
    signOut: () => void;
    mountAuth: (node: HTMLDivElement, signUp: boolean) => () => void;
    signUp?: boolean;
  } = $props();
  function authForm(node: HTMLDivElement) {
    const unmount = mountAuth(node, signUp);
    return { destroy: unmount };
  }
  const errors = {
    authentication_required:
      'ログインの有効期限が切れた可能性があります。もう一度ログインしてください。',
    access_denied: 'このページを利用する権限がありません。',
    dependency_unavailable:
      'サービスに接続できませんでした。時間をおいて、もう一度お試しください。',
  };
</script>

<main class="access-page">
  <section class="access-panel" aria-label="アカウントのアクセス確認">
    <a class="access-brand" href="/"
      ><img
        src="/brand/cycrio-mark-128.png"
        width="40"
        height="40"
        alt=""
      />Cycrio</a
    >
    <div aria-live={state.status === 'signed_out' ? 'off' : 'polite'}>
      {#if state.status === 'signed_out'}
        <h1>毎月の支出に、見通しを。</h1>
        <p>
          メールとつながるサブスクリプション管理。<br />Google
          アカウントでログインしてください。
        </p>
      {:else if state.status === 'unconfigured'}
        <h1>ログインの準備中です</h1>
        <p>管理者による認証設定の完了後に、ご利用いただけます。</p>
        <button class="primary" onclick={retry}>設定を再確認</button>
      {:else if state.status === 'pending'}
        <h1>利用許可をお待ちください</h1>
        <p>ログインを確認しました。管理者の許可後にご利用いただけます。</p>
        <p class="approval-identity">
          管理者に次のユーザーIDをお伝えください。<br />
          <span>ユーザーID：</span><code>{state.clerkUserId}</code>
        </p>
        <button class="primary" onclick={retry}>利用許可を再確認</button><button
          onclick={signOut}>ログアウト</button
        >
      {:else if state.status === 'error'}
        <h1>確認できませんでした</h1>
        <p>{errors[state.code]}</p>
        <button class="primary" onclick={retry}>もう一度試す</button
        >{#if state.signedIn}<button onclick={signOut}>ログアウト</button>{/if}
      {:else if state.status === 'denied'}
        <h1>利用権限がありません</h1>
        <p>権限について管理者へお問い合わせください。</p>
        <button onclick={signOut}>ログアウト</button>
      {:else}
        <h1>
          {state.status === 'redirecting'
            ? 'ログインを完了しています'
            : 'アクセスを確認しています'}
        </h1>
        <p>しばらくお待ちください。</p>
      {/if}
    </div>
    {#if state.status === 'signed_out'}
      {#key signUp}<div class="clerk-host" use:authForm></div>{/key}
      <p class="login-note">
        ご利用には管理者の許可が必要です。<br
        />メール取得の許可は、ログイン後に別途設定します。
      </p>
    {/if}
  </section>
</main>

<style>
  .access-page {
    min-height: 100svh;
    display: grid;
    place-items: center;
    padding: 28px 16px;
  }
  .access-panel {
    width: 100%;
    max-width: 460px;
    padding: 32px;
    border: 1px solid var(--color-border);
    border-radius: 12px;
    background: var(--color-surface);
  }
  .access-brand {
    display: flex;
    min-height: 44px;
    gap: 8px;
    align-items: center;
    color: var(--color-text);
    text-decoration: none;
    font-size: 28px;
    font-weight: 750;
    letter-spacing: -1px;
    margin-bottom: 26px;
  }
  h1 {
    font-size: 23px;
    line-height: 1.55;
    margin: 0 0 12px;
  }
  p {
    color: var(--color-muted);
    line-height: 1.9;
    margin: 0 0 20px;
  }
  button {
    display: block;
    width: 100%;
    border: 1px solid var(--color-border);
    border-radius: 6px;
    padding: 10px;
    margin-top: 10px;
    background: var(--color-surface);
    color: var(--color-action);
  }
  .primary {
    background: var(--color-action);
    color: var(--color-on-action);
    border-color: var(--color-action);
  }
  .login-note {
    border-top: 1px solid var(--color-border);
    padding-top: 18px;
    margin: 18px 0 0;
    font-size: 12px;
  }
  .approval-identity {
    overflow-wrap: anywhere;
  }
  .clerk-host {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .clerk-host :global(.cl-rootBox),
  .clerk-host :global(.cl-cardBox),
  .clerk-host :global(.cl-card) {
    width: 100%;
    min-width: 0;
    max-width: 100%;
  }
  .clerk-host :global(.cl-card) {
    padding: 16px 0;
  }
  @media (max-width: 400px) {
    .access-panel {
      padding: 24px 20px;
    }
    h1 {
      font-size: 21px;
    }
  }
</style>
