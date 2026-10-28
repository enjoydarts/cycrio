<script lang="ts">
  import { onMount, tick } from 'svelte';
  import {
    type createAPI,
    type CredentialProvider,
    type TokenProvider,
  } from './api';
  import {
    createCredentialsController,
    type CredentialsState,
    serializeOAuthCredential,
  } from './credentials';

  let {
    api,
    token,
    onDenied,
    apiBase = '',
  }: {
    api: ReturnType<typeof createAPI>;
    token: TokenProvider;
    onDenied: () => void;
    apiBase?: string;
  } = $props();
  let view = $state<CredentialsState>({
    status: 'idle',
    credentials: [],
    events: [],
    error: null,
    success: null,
  });
  let confirmation = $state<CredentialProvider | null>(null);
  let controller: ReturnType<typeof createCredentialsController> | undefined;
  const providers = {
    liquid: { label: 'Liquid AI（D1）', purpose: '一次判定' },
    openrouter: { label: 'OpenRouter', purpose: '二次処理' },
    openai: { label: 'OpenAI', purpose: '二次処理' },
    anthropic: { label: 'Anthropic', purpose: '二次処理' },
    resend: { label: 'Resend', purpose: 'メール送信' },
    clerk_webhook: { label: 'Clerk Webhook', purpose: 'イベント署名の検証' },
    resend_webhook: {
      label: 'Resend Webhook',
      purpose: '配信イベント署名の検証',
    },
    gmail_oauth: {
      label: 'Gmail / Google Workspace',
      purpose: 'メール読み取り用 OAuth アプリ',
    },
    outlook_oauth: {
      label: 'Outlook.com / Hotmail',
      purpose: '個人アカウントのメール読み取り用 OAuth アプリ',
    },
  };
  const statuses = {
    not_configured: '未登録',
    configured: '登録済み',
    disabled: '無効',
  };
  const actions = {
    credential_created: '登録',
    credential_updated: '更新',
    credential_disabled: '無効化',
  };
  const errors = {
    authentication_required: 'ログインの有効期限を確認しています。',
    access_denied: '管理者権限を再確認しています。',
    dependency_unavailable:
      '読み込みまたは保存に失敗しました。保存状態を再読み込みして確認してください。',
    invalid_request:
      'API キーまたは OAuth アプリの入力条件とコールバック URI を確認してください。',
    conflict:
      '別の操作により更新されました。最新の保存状態を読み込みました。内容を確認して、もう一度入力・操作してください。',
    not_found: '対象が見つかりません。保存状態を再読み込みしてください。',
  };
  let busy = $derived(
    view.status === 'idle' ||
      view.status === 'loading' ||
      view.status === 'saving' ||
      view.status === 'denied',
  );
  const datetime = (value: string) =>
    new Date(value).toLocaleString('ja-JP', {
      timeZone: 'Asia/Tokyo',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    });

  onMount(() => {
    controller = createCredentialsController(
      api,
      token,
      (next) => {
        view = next;
      },
      onDenied,
    );
    void controller.load();
    return () => controller?.dispose();
  });

  function submit(
    event: SubmitEvent,
    provider: CredentialProvider,
    version: number,
  ) {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const field = (name: string) =>
      (form.elements.namedItem(name) as HTMLInputElement).value;
    let secret = '';
    try {
      secret =
        provider === 'gmail_oauth' || provider === 'outlook_oauth'
          ? serializeOAuthCredential(provider, {
              ClientID: field('client_id'),
              ClientSecret: field('client_secret'),
              RedirectURI: field('redirect_uri'),
            })
          : field('secret');
    } catch {
      /* The controller publishes the same safe validation error. */
    }
    form.reset();
    confirmation = null;
    if (!busy) void controller?.put(provider, secret, version);
  }

  function disable(provider: CredentialProvider, version: number) {
    confirmation = null;
    void controller?.disable(provider, version);
  }

  async function openConfirmation(provider: CredentialProvider) {
    confirmation = provider;
    await tick();
    if (confirmation === provider)
      document.getElementById(`confirm-disable-${provider}`)?.focus();
  }

  async function cancelConfirmation(provider: CredentialProvider) {
    confirmation = null;
    await tick();
    if (confirmation === null)
      document.getElementById(`disable-${provider}`)?.focus();
  }
</script>

<section
  class="credentials"
  aria-labelledby="credentials-heading"
  aria-busy={busy}
>
  <h3 id="credentials-heading">API キー・メール連携アプリ管理</h3>
  <p class="intro">
    保存した API キー・Client Secret は再表示できません。AI の接続テストは <a
      href="/settings/ai-runtime">AI 実行・接続</a
    > で行えます。
  </p>
  <div class="feedback" aria-live="polite" aria-atomic="true">
    {#if view.status === 'idle' || view.status === 'loading'}<p>
        保存状態と操作履歴を読み込んでいます。
      </p>{/if}
    {#if view.status === 'saving'}<p>
        処理中です。完了までお待ちください。
      </p>{/if}
    {#if view.error}<p class="error" role="alert">{errors[view.error]}</p>{/if}
    {#if view.success}<p class="success">
        {view.success === 'saved'
          ? 'API キーを保存しました。'
          : 'API キーを無効化しました。'}
      </p>{/if}
  </div>
  <button
    class="secondary refresh"
    disabled={busy}
    onclick={() => {
      confirmation = null;
      void controller?.load();
    }}>保存状態と履歴を再読み込み</button
  >
  <div class="provider-list">
    {#each view.credentials as credential (credential.provider)}
      {@const provider = providers[credential.provider]}
      <article class="provider">
        <div class="provider-heading">
          <h4>{provider.label}</h4>
          <span
            class:configured={credential.state === 'configured'}
            class="badge">{statuses[credential.state]}</span
          >
        </div>
        <p class="purpose">{provider.purpose}</p>
        <dl>
          <div>
            <dt>リビジョン</dt>
            <dd>{credential.version}</dd>
          </div>
          <div>
            <dt>最終更新（日本時間）</dt>
            <dd>
              {#if credential.updated_at}<time datetime={credential.updated_at}
                  >{datetime(credential.updated_at)}</time
                >{:else}未更新{/if}
            </dd>
          </div>
        </dl>
        <form
          onsubmit={(event) =>
            submit(event, credential.provider, credential.version)}
          novalidate
        >
          {#if credential.provider === 'gmail_oauth' || credential.provider === 'outlook_oauth'}
            {@const mailProvider =
              credential.provider === 'gmail_oauth' ? 'gmail' : 'outlook'}
            <p class="help">
              {mailProvider === 'gmail'
                ? 'Gmail の読み取り専用権限 gmail.readonly を利用します。個人・Workspace に対応します。Workspace では組織管理者の許可が必要な場合があります。'
                : 'Microsoft Graph の Mail.Read と offline_access を利用します。個人の Outlook.com / Hotmail 向けアプリを登録してください。'}
            </p>
            <label for={`client-id-${credential.provider}`}>Client ID</label>
            <input
              id={`client-id-${credential.provider}`}
              name="client_id"
              type="text"
              autocomplete="off"
              spellcheck="false"
              required
              disabled={busy}
            />
            <label for={`client-secret-${credential.provider}`}
              >Client Secret</label
            >
            <input
              id={`client-secret-${credential.provider}`}
              name="client_secret"
              type="password"
              autocomplete="new-password"
              spellcheck="false"
              required
              disabled={busy}
            />
            <label for={`redirect-uri-${credential.provider}`}
              >Redirect URI</label
            >
            <input
              id={`redirect-uri-${credential.provider}`}
              name="redirect_uri"
              type="url"
              value={`${apiBase.replace(/\/$/, '')}/v1/mail/oauth/callback/${mailProvider}`}
              autocomplete="off"
              spellcheck="false"
              required
              disabled={busy}
            />
            <p class="help">
              API の公開 URL を使い、認可サービスにも同じ URI
              を登録してください。HTTPS（ローカル開発は localhost / 127.0.0.1 の
              HTTP）に対応します。更新時はすべての項目を再入力します。
            </p>
          {:else}
            <label for={`secret-${credential.provider}`}
              >{provider.label} の API キー</label
            >
            <input
              id={`secret-${credential.provider}`}
              name="secret"
              type="password"
              autocomplete="off"
              spellcheck="false"
              autocapitalize="off"
              minlength="8"
              maxlength="4096"
              required
              disabled={busy}
              aria-describedby="secret-help"
            />
          {/if}
          <button class="primary" type="submit" disabled={busy}
            >{credential.state === 'configured'
              ? '設定を更新する'
              : '設定を登録する'}</button
          >
        </form>
        {#if credential.state === 'configured'}
          {#if confirmation === credential.provider}
            <div
              class="confirmation"
              role="group"
              aria-label={`${provider.label} の無効化確認`}
            >
              <p>{provider.label} の API キーを無効化しますか？</p>
              <div class="confirmation-actions">
                <button
                  id={`confirm-disable-${credential.provider}`}
                  class="danger"
                  disabled={busy}
                  onclick={() =>
                    disable(credential.provider, credential.version)}
                  >無効化を確定する</button
                ><button
                  class="secondary"
                  disabled={busy}
                  onclick={() => cancelConfirmation(credential.provider)}
                  >キャンセル</button
                >
              </div>
            </div>
          {:else}<button
              id={`disable-${credential.provider}`}
              class="secondary"
              disabled={busy}
              onclick={() => openConfirmation(credential.provider)}
              >{provider.label} を無効化する</button
            >{/if}
        {/if}
      </article>
    {/each}
  </div>
  <p id="secret-help" class="help">
    キーは空白を含まない半角英数字・記号の 8〜4096
    文字で入力してください。送信時に入力欄を消去します。
  </p>
  <section class="audit" aria-labelledby="audit-heading">
    <h3 id="audit-heading">最近の操作履歴</h3>
    <p class="intro">最新 50 件まで表示します。日時は日本時間です。</p>
    {#if view.status === 'ready' && view.events.length === 0}<p class="intro">
        操作履歴はまだありません。
      </p>{/if}
    <ol>
      {#each view.events as event (event.id)}<li>
          <div class="audit-heading">
            <strong
              >{providers[event.provider].label}：{actions[
                event.action
              ]}</strong
            ><span>リビジョン {event.version}</span>
          </div>
          <p>操作したユーザー ID <code>{event.actor_id}</code></p>
          <time datetime={event.created_at}>{datetime(event.created_at)}</time>
        </li>{/each}
    </ol>
  </section>
</section>

<style>
  .credentials {
    margin: 0 0 2rem;
    text-align: left;
    min-width: 0;
  }
  h3 {
    margin: 0 0 0.75rem;
    font-size: 1.1rem;
  }
  .intro,
  .help,
  .purpose {
    color: var(--color-muted);
    font-size: 0.8rem;
    line-height: 1.8;
  }
  .feedback {
    font-size: 0.85rem;
    line-height: 1.8;
    overflow-wrap: anywhere;
  }
  .error {
    color: var(--color-danger);
  }
  .success {
    color: var(--color-action-hover);
  }
  .provider-list {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 20rem), 1fr));
    gap: 1rem;
    margin-top: 1.25rem;
  }
  .provider {
    border: 1px solid var(--color-border);
    border-radius: 0.85rem;
    padding: clamp(0.8rem, 3vw, 1.2rem);
    min-width: 0;
  }
  .provider-heading,
  .audit-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  h4 {
    font-size: 0.95rem;
    margin: 0;
  }
  .badge {
    font-size: 0.75rem;
    padding: 0.2rem 0.55rem;
    border-radius: 1rem;
    background: var(--color-surface-soft);
    color: var(--color-muted);
  }
  .badge.configured {
    color: var(--color-action-hover);
    background: var(--brand-accent);
  }
  .purpose {
    margin: 0.35rem 0 0.85rem;
  }
  dl {
    font-size: 0.75rem;
    line-height: 1.8;
    margin: 0 0 1rem;
  }
  dl > div {
    display: flex;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  dt {
    color: var(--color-muted);
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  label {
    display: block;
    font-size: 0.8rem;
    font-weight: 600;
    margin-bottom: 0.45rem;
  }
  input {
    box-sizing: border-box;
    width: 100%;
    min-width: 0;
    border: 1px solid var(--color-border);
    border-radius: 0.5rem;
    padding: 0.7rem;
    color: var(--color-text);
    background: var(--color-surface);
    font: inherit;
  }
  button {
    width: 100%;
    min-height: 2.75rem;
    border: 1px solid var(--color-border);
    border-radius: 0.55rem;
    padding: 0.6rem 0.7rem;
    margin-top: 0.65rem;
    font: inherit;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
    line-height: 1.6;
    overflow-wrap: anywhere;
  }
  .primary {
    background: var(--color-action);
    color: var(--color-on-action);
    border-color: var(--color-action);
  }
  .primary:hover {
    background: var(--color-action-hover);
  }
  .secondary {
    background: var(--color-surface);
    color: var(--color-action);
  }
  .secondary:hover {
    background: var(--color-surface-soft);
  }
  .danger {
    background: #9f1239;
    color: white;
    border-color: #9f1239;
  }
  button:disabled {
    opacity: 0.6;
    cursor: wait;
  }
  button:focus-visible,
  input:focus-visible {
    outline: 2px solid var(--color-action-hover);
    outline-offset: 3px;
  }
  .confirmation {
    background: var(--color-surface-soft);
    padding: 0.75rem;
    margin-top: 0.8rem;
    border-radius: 0.5rem;
    font-size: 0.8rem;
    line-height: 1.8;
  }
  .confirmation p {
    margin: 0;
  }
  .confirmation-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .confirmation-actions button {
    flex: 1 1 8rem;
  }
  .help {
    margin: 1rem 0 2rem;
  }
  .audit {
    border-top: 1px solid var(--color-border);
    padding-top: 1.5rem;
  }
  ol {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  li {
    padding: 1rem 0;
    border-bottom: 1px solid var(--color-border);
    font-size: 0.75rem;
    line-height: 1.8;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  li p {
    margin: 0.4rem 0;
    color: var(--color-muted);
  }
  code {
    display: block;
    color: var(--color-text);
    overflow-wrap: anywhere;
  }
  li time {
    color: var(--color-muted);
  }
</style>
