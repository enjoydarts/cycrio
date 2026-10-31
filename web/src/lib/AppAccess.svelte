<script lang="ts">
  import Button from './Button.svelte';
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
  <div class="access-layout">
    <a class="access-brand" href="/"
      ><img
        src="/brand/cycrio-mark-128.png"
        width="56"
        height="56"
        alt=""
      />{#if state.status === 'signed_out'}<h1 class="brand-name">
          Cycrio
        </h1>{:else}<span class="brand-name">Cycrio</span>{/if}</a
    >
    {#if state.status === 'signed_out'}<p class="access-caption">
        サインインして続ける
      </p>{/if}
    <section
      class="access-panel"
      class:auth-card-shell={state.status === 'signed_out'}
      aria-label="アカウントのアクセス確認"
    >
      <div aria-live={state.status === 'signed_out' ? 'off' : 'polite'}>
        {#if state.status === 'signed_out'}
          <p class="sr-only">Google アカウントでログインしてください。</p>
        {:else if state.status === 'unconfigured'}
          <h1>ログインの準備中です</h1>
          <p>管理者による認証設定の完了後に、ご利用いただけます。</p>
          <Button wrap variant="primary" class={['primary']} onclick={retry}
            >設定を再確認</Button
          >
        {:else if state.status === 'pending'}
          <h1>利用許可をお待ちください</h1>
          <p>ログインを確認しました。管理者の許可後にご利用いただけます。</p>
          <p class="approval-identity">
            管理者に次のユーザーIDをお伝えください。<br />
            <span>ユーザーID：</span><code>{state.clerkUserId}</code>
          </p>
          <Button wrap variant="primary" class={['primary']} onclick={retry}
            >利用許可を再確認</Button
          ><Button wrap onclick={signOut}>ログアウト</Button>
        {:else if state.status === 'error'}
          <h1>確認できませんでした</h1>
          <p>{errors[state.code]}</p>
          <Button wrap variant="primary" class={['primary']} onclick={retry}
            >もう一度試す</Button
          >{#if state.signedIn}<Button wrap onclick={signOut}>ログアウト</Button
            >{/if}
        {:else if state.status === 'denied'}
          <h1>利用権限がありません</h1>
          <p>権限について管理者へお問い合わせください。</p>
          <Button wrap onclick={signOut}>ログアウト</Button>
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
        <div
          class="clerk-host"
          class:last-google={state.lastAuthenticationStrategy ===
            'oauth_google'}
        >
          {#if state.lastAuthenticationStrategy === 'oauth_google'}<span
              class="sr-only">前回利用したログイン方法: Google</span
            >{/if}
          {#key signUp}<div use:authForm></div>{/key}
        </div>
      {/if}
    </section>
    {#if state.status === 'signed_out'}<p class="login-note">
        ご利用には管理者の許可が必要です。
      </p>
    {/if}
  </div>
</main>

<style>
  .access-page {
    min-height: 100svh;
    display: grid;
    justify-items: center;
    align-content: center;
    padding: 48px 16px;
    background: var(--brand-background);
  }
  .access-layout {
    width: 100%;
    max-width: 400px;
  }
  .clerk-host.last-google :global(button.cl-socialButtonsBlockButton) {
    position: relative;
    overflow: visible;
  }
  .clerk-host.last-google
    :global(
      button.cl-socialButtonsBlockButton:not(
          :has(.cl-lastAuthenticationStrategyBadge)
        )::after
    ) {
    content: 'Last used';
    position: absolute;
    right: -10px;
    top: -11px;
    pointer-events: none;
    line-height: 18px;
    box-shadow: 0 0 0 1px var(--color-surface);
  }
  .access-panel {
    width: 100%;
    padding: 32px;
    border: 1px solid var(--color-border);
    border-radius: 12px;
    background: var(--color-surface);
    box-shadow: 0 10px 32px var(--color-shadow);
  }
  .access-brand {
    display: flex;
    flex-direction: column;
    min-height: 44px;
    gap: 12px;
    align-items: center;
    color: var(--color-text);
    text-decoration: none;
    font-size: 28px;
    font-weight: 750;
    letter-spacing: -1px;
    margin-bottom: 12px;
  }
  .auth-card-shell {
    padding: 0;
    border: 0;
    border-radius: 0;
    background: transparent;
    box-shadow: none;
  }
  .brand-name {
    font-size: 24px;
    font-weight: 700;
    line-height: 32px;
    margin: 0;
    letter-spacing: -0.7px;
  }
  .access-caption {
    text-align: center;
    margin: 0 0 28px;
    font-size: 14px;
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
  .login-note {
    margin: 18px 0 0;
    font-size: 12px;
    text-align: center;
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
  .clerk-host :global(.cl-signIn-start .cl-headerTitle),
  .clerk-host :global(.cl-signIn-start .cl-headerSubtitle),
  .clerk-host :global(.cl-signUp-start .cl-headerTitle),
  .clerk-host :global(.cl-signUp-start .cl-headerSubtitle),
  .clerk-host :global(.cl-signIn-start .cl-footerAction),
  .clerk-host :global(.cl-signUp-start .cl-footerAction) {
    display: none;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
  @media (max-width: 400px) {
    .access-panel:not(.auth-card-shell) {
      padding: 24px 20px;
    }
    .clerk-host :global(.cl-card) {
      padding: 24px;
    }
    h1 {
      font-size: 21px;
    }
  }
</style>
