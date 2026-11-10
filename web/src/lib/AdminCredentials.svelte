<script lang="ts">
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { onMount, tick } from 'svelte';
  import { beforeNavigate } from '$app/navigation';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import {
    type createAPI,
    type CredentialProvider,
    type TokenProvider,
  } from './api';
  import {
    createCredentialsController,
    type CredentialsState,
    serializeOAuthCredential,
    serializePostHogCredential,
    serializeOneSignalCredential,
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
  let selectedProvider = $state<CredentialProvider | null>(null);
  let history = $state(false),
    dirty = $state(false),
    discardConfirmation = $state(false);
  let nextProvider = $state<CredentialProvider | null>(null);
  let controller: ReturnType<typeof createCredentialsController> | undefined;
  const providers = {
    onesignal: { label: 'OneSignal', purpose: '端末へのPush通知' },
    posthog: {
      label: 'PostHog',
      purpose: '本番環境のエラー追跡',
    },
    healthchecks: {
      label: 'Healthchecks.io',
      purpose: 'API・DB・Workerの定期監視通知',
    },
    liquid: { label: 'Liquid AI（D1）', purpose: '一次判定' },
    openrouter: { label: 'OpenRouter', purpose: '二次処理' },
    openai: { label: 'OpenAI', purpose: '二次処理' },
    anthropic: { label: 'Anthropic', purpose: '二次処理' },
    resend: { label: 'Resend', purpose: 'メール送信' },
    clerk_backend: {
      label: 'Clerk Backend API',
      purpose: '退会時のClerkユーザー削除',
    },
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
      'API キー・Ping URL・OAuth アプリ・エラー追跡設定の入力条件を確認してください。',
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
  function selectProvider(value: CredentialProvider | null) {
    if (busy || selectedProvider === value) return;
    if (dirty) {
      nextProvider = value;
      discardConfirmation = true;
      return;
    }
    selectedProvider = value;
    confirmation = null;
  }
  function discardEditor() {
    if (busy) return;
    dirty = false;
    discardConfirmation = false;
    selectedProvider = nextProvider;
    confirmation = null;
  }
  $effect(() => {
    if (view.status === 'denied' || view.status === 'disposed') {
      selectedProvider = null;
      dirty = false;
      discardConfirmation = false;
    }
  });
  beforeNavigate((navigation) => {
    if (
      view.status === 'saving' ||
      (dirty &&
        !window.confirm(
          '保存していない入力があります。このページを離れますか？',
        ))
    )
      navigation.cancel();
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
    const leave = (event: BeforeUnloadEvent) => {
      if (dirty || view.status === 'saving') {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', leave);
    return () => {
      window.removeEventListener('beforeunload', leave);
      controller?.dispose();
    };
  });

  function submit(
    event: SubmitEvent,
    provider: CredentialProvider,
    version: number,
  ) {
    event.preventDefault();
    if (busy) return;
    const form = event.currentTarget as HTMLFormElement;
    const field = (name: string) =>
      (form.elements.namedItem(name) as HTMLInputElement).value;
    let secret = '';
    try {
      secret =
        provider === 'onesignal'
          ? serializeOneSignalCredential({
              app_id: field('app_id'),
              api_key: field('api_key'),
            })
          : provider === 'posthog'
            ? serializePostHogCredential({
                project_token: field('project_token'),
                host: field('host'),
              })
            : provider === 'gmail_oauth' || provider === 'outlook_oauth'
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
    dirty = false;
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
  class="credentials library-page"
  aria-labelledby="credentials-heading"
  aria-busy={busy}
>
  <LibraryHeader
    title="API キー・連携アプリ"
    id="credentials-heading"
    description="サービスを選んで登録・更新します。保存した秘密値は再表示しません。"
  >
    {#snippet action()}
      <Button
        wrap
        class={[
          'secondary refresh',
          'part-AdminCredentials-secondary',
          'part-AdminCredentials-refresh',
        ]}
        disabled={busy || dirty}
        onclick={() => {
          confirmation = null;
          void controller?.load();
        }}>更新</Button
      >
    {/snippet}
  </LibraryHeader>
  <LibraryPanel>
    <nav class="library-tabs" aria-label="API キーの表示">
      <Button
        wrap
        variant="tab"
        aria-current={!history ? 'page' : undefined}
        onclick={() => (history = false)}>接続サービス</Button
      ><Button
        wrap
        variant="tab"
        aria-current={history ? 'page' : undefined}
        onclick={() => (history = true)}>操作履歴</Button
      >
    </nav>
    <div class="library-content">
      <div class="feedback" aria-live="polite" aria-atomic="true">
        {#if view.status === 'idle' || view.status === 'loading'}<p>
            保存状態と操作履歴を読み込んでいます。
          </p>{/if}
        {#if view.status === 'saving'}<p>
            処理中です。完了までお待ちください。
          </p>{/if}
        {#if view.error}<p class="error" role="alert">
            {errors[view.error]}
          </p>{/if}
        {#if view.success}<p class="success">
            {view.success === 'saved'
              ? selectedProvider === 'posthog'
                ? 'エラー追跡設定を保存しました。'
                : selectedProvider === 'healthchecks'
                  ? '監視用Ping URLを保存しました。'
                  : 'API キーを保存しました。'
              : selectedProvider === 'posthog'
                ? 'エラー追跡を無効化しました。'
                : selectedProvider === 'healthchecks'
                  ? '監視通知の送信を停止しました。'
                  : 'API キーを無効化しました。'}
          </p>{/if}
      </div>
      {#if discardConfirmation}<div class="confirmation" role="alert">
          <p>入力を破棄してサービス一覧に戻りますか？</p>
          <ActionGroup
            class={[
              'confirmation-actions',
              'part-AdminCredentials-confirmation-actions',
            ]}
          >
            <Button
              wrap
              class={['danger', 'part-AdminCredentials-danger']}
              disabled={busy}
              onclick={discardEditor}>入力を破棄して戻る</Button
            ><Button
              wrap
              class={['secondary', 'part-AdminCredentials-secondary']}
              disabled={busy}
              onclick={() => (discardConfirmation = false)}>編集を続ける</Button
            >
          </ActionGroup>
        </div>{/if}
      <div hidden={history}>
        {#if selectedProvider === null}
          <div class="service-list" aria-label="登録サービス">
            {#each view.credentials as credential (credential.provider)}
              <Button
                wrap
                variant="plain"
                class={['service-row', 'part-AdminCredentials-service-row']}
                disabled={busy}
                onclick={() => selectProvider(credential.provider)}
                ><span
                  ><strong>{providers[credential.provider].label}</strong><small
                    >{providers[credential.provider].purpose}</small
                  ></span
                ><span
                  class="badge"
                  class:configured={credential.state === 'configured'}
                  >{statuses[credential.state]}</span
                ><span aria-hidden="true">→</span></Button
              >
            {/each}
          </div>
        {:else}<Button
            wrap
            class={[
              'secondary back',
              'part-AdminCredentials-secondary',
              'part-AdminCredentials-back',
            ]}
            disabled={busy}
            onclick={() => selectProvider(null)}>← サービス一覧に戻る</Button
          >{/if}
        <div class="provider-list">
          {#each view.credentials as credential (credential.provider)}
            {#if credential.provider === selectedProvider}
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
                      {#if credential.updated_at}<time
                          datetime={credential.updated_at}
                          >{datetime(credential.updated_at)}</time
                        >{:else}未更新{/if}
                    </dd>
                  </div>
                </dl>
                <form
                  onsubmit={(event) =>
                    submit(event, credential.provider, credential.version)}
                  oninput={() => (dirty = true)}
                  novalidate
                >
                  {#if credential.provider === 'onesignal'}
                    <div class="credential-field">
                      <label for="app-id-onesignal">App ID</label><input
                        id="app-id-onesignal"
                        name="app_id"
                        type="text"
                        required
                        disabled={busy}
                        autocomplete="off"
                        spellcheck="false"
                      />
                    </div>
                    <div class="credential-field">
                      <label for="api-key-onesignal">API key</label><input
                        id="api-key-onesignal"
                        name="api_key"
                        type="password"
                        required
                        disabled={busy}
                        autocomplete="new-password"
                        spellcheck="false"
                      />
                      <p class="help">
                        OneSignalのApp API
                        keyを入力します。保存後は再表示しません。更新時はApp
                        IDも再入力してください。
                      </p>
                    </div>
                  {:else if credential.provider === 'posthog'}
                    <p class="help">
                      本番環境の未処理エラーだけを送信します。自動アクセス解析・録画・Cookieは利用しません。
                    </p>
                    <div class="credential-field">
                      <label for="project-token-posthog">Project Token</label>
                      <input
                        id="project-token-posthog"
                        name="project_token"
                        type="password"
                        autocomplete="off"
                        spellcheck="false"
                        autocapitalize="off"
                        required
                        disabled={busy}
                      />
                      <p class="help">
                        phc_ で始まるブラウザー公開用のProject
                        Tokenを入力してください。Personal API
                        keyは使用できません。保存後は再表示しません。
                      </p>
                    </div>
                    <div class="credential-field">
                      <label for="host-posthog">Host</label>
                      <select id="host-posthog" name="host" disabled={busy}>
                        <option value="https://us.i.posthog.com"
                          >US — https://us.i.posthog.com</option
                        >
                        <option value="https://eu.i.posthog.com"
                          >EU — https://eu.i.posthog.com</option
                        >
                      </select>
                      <p class="help">
                        プロジェクトのリージョンを選択します。更新時はProject
                        TokenとHostを再入力してください。
                      </p>
                    </div>
                  {:else if credential.provider === 'gmail_oauth' || credential.provider === 'outlook_oauth'}
                    {@const mailProvider =
                      credential.provider === 'gmail_oauth'
                        ? 'gmail'
                        : 'outlook'}
                    <p class="help">
                      {mailProvider === 'gmail'
                        ? 'Gmail の読み取り専用権限 gmail.readonly を利用します。個人・Workspace に対応します。Workspace では組織管理者の許可が必要な場合があります。'
                        : 'Microsoft Graph の Mail.Read と offline_access を利用します。個人の Outlook.com / Hotmail 向けアプリを登録してください。'}
                    </p>
                    <div class="credential-field">
                      <label for={`client-id-${credential.provider}`}
                        >Client ID</label
                      >
                      <input
                        id={`client-id-${credential.provider}`}
                        name="client_id"
                        type="text"
                        autocomplete="off"
                        spellcheck="false"
                        required
                        disabled={busy}
                      />
                    </div>
                    <div class="credential-field">
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
                    </div>
                    <div class="credential-field">
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
                        を登録してください。HTTPS（ローカル開発は localhost /
                        127.0.0.1 の
                        HTTP）に対応します。更新時はすべての項目を再入力します。
                      </p>
                    </div>
                  {:else}
                    <div class="credential-field">
                      <label for={`secret-${credential.provider}`}
                        >{credential.provider === 'healthchecks'
                          ? 'Healthchecks.io Ping URL'
                          : `${provider.label} の API キー`}</label
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
                      <p id="secret-help" class="help">
                        {#if credential.provider === 'healthchecks'}
                          Healthchecks.ioで作成した https://hc-ping.com/
                          から始まるUUID形式のPing
                          URLを入力します。保存すると、API・DB・Workerが正常な場合だけ1分ごとに空のPOSTを送信します。保存したURLは再表示しません。送信先のチェックは、この環境専用にしてください。
                        {:else}
                          キーは空白を含まない半角英数字・記号の 8〜4096
                          文字で入力してください。送信時に入力欄を消去します。
                        {/if}
                      </p>
                    </div>
                  {/if}
                  <div class="form-actions">
                    <ActionGroup align="end">
                      <Button
                        wrap
                        variant="primary"
                        class={['primary']}
                        type="submit"
                        disabled={busy}
                        >{credential.provider === 'healthchecks'
                          ? credential.state === 'configured'
                            ? '監視用URLを更新する'
                            : '保存して監視を開始'
                          : credential.state === 'configured'
                            ? '設定を更新する'
                            : '設定を登録する'}</Button
                      >
                    </ActionGroup>
                  </div>
                </form>
                {#if credential.state === 'configured'}
                  <section
                    class="disable-section"
                    aria-label={`${provider.label} の無効化`}
                  >
                    {#if confirmation === credential.provider}
                      <div
                        class="confirmation"
                        role="group"
                        aria-label={`${provider.label} の無効化確認`}
                      >
                        {#if credential.provider === 'healthchecks'}
                          <p>
                            Cycrioからの監視通知を停止しますか？healthchecks.io側のチェックも一時停止してください。
                          </p>
                        {:else}<p>
                            {provider.label} の API キーを無効化しますか？
                          </p>{/if}
                        <ActionGroup
                          class={[
                            'confirmation-actions',
                            'part-AdminCredentials-confirmation-actions',
                          ]}
                        >
                          <Button
                            wrap
                            id={`confirm-disable-${credential.provider}`}
                            class={['danger', 'part-AdminCredentials-danger']}
                            disabled={busy}
                            onclick={() =>
                              disable(credential.provider, credential.version)}
                            >無効化を確定する</Button
                          ><Button
                            wrap
                            class={[
                              'secondary',
                              'part-AdminCredentials-secondary',
                            ]}
                            disabled={busy}
                            onclick={() =>
                              cancelConfirmation(credential.provider)}
                            >キャンセル</Button
                          >
                        </ActionGroup>
                      </div>
                    {:else}<Button
                        wrap
                        id={`disable-${credential.provider}`}
                        class={['secondary', 'part-AdminCredentials-secondary']}
                        disabled={busy}
                        onclick={() => openConfirmation(credential.provider)}
                        >{provider.label} を無効化する</Button
                      >{/if}
                  </section>
                {/if}
              </article>{/if}
          {/each}
        </div>
      </div>
      <section hidden={!history} class="audit" aria-labelledby="audit-heading">
        <h2 id="audit-heading">操作履歴</h2>
        <p class="intro">最新 50 件まで表示します。日時は日本時間です。</p>
        {#if view.status === 'ready' && view.events.length === 0}<p
            class="intro"
          >
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
              <details>
                <summary>操作の詳細</summary>
                <p>操作したユーザー ID <code>{event.actor_id}</code></p>
              </details>
              <time datetime={event.created_at}
                >{datetime(event.created_at)}</time
              >
            </li>{/each}
        </ol>
      </section>
    </div>
  </LibraryPanel>
</section>

<style>
  [hidden] {
    display: none !important;
  }
  :global(.part-AdminCredentials-refresh) {
    width: auto;
  }
  .service-list {
    overflow: hidden;
  }
  :global(.part-AdminCredentials-service-row) {
    display: flex;
    align-items: center;
    gap: 1rem;
    margin: 0;
    padding: 1rem 0;
    border-radius: 0;
    border-bottom: 1px solid var(--color-border);
  }
  :global(.part-AdminCredentials-service-row):hover {
    background: var(--color-surface-soft);
  }
  :global(.part-AdminCredentials-service-row > span:first-child) {
    flex: 1;
    min-width: 0;
  }
  :global(.part-AdminCredentials-service-row) small {
    display: block;
    color: var(--color-muted);
    font-weight: 400;
    margin-top: 0.2rem;
  }
  :global(.part-AdminCredentials-service-row):last-child {
    border-bottom: 0;
  }
  :global(.part-AdminCredentials-back) {
    width: auto;
    margin: 0;
  }
  .credentials {
    margin-bottom: 2rem;
    text-align: left;
    min-width: 0;
  }
  h2 {
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
  .feedback:has(p) {
    margin-bottom: 1.25rem;
  }
  .feedback p {
    margin: 0;
  }
  .error {
    color: var(--color-danger);
  }
  .success {
    color: var(--color-action-hover);
  }
  .provider-list {
    display: grid;
    max-width: 640px;
    gap: 1rem;
    margin-top: 1.25rem;
  }
  .provider {
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
  form {
    display: grid;
    gap: 20px;
  }
  .credential-field {
    min-width: 0;
  }
  .form-actions {
    border-top: 1px solid var(--color-border);
    padding-top: 16px;
  }
  .disable-section {
    margin-top: 24px;
    border-top: 1px solid var(--color-border);
    padding-top: 20px;
  }
  :global(.part-AdminCredentials-secondary) {
    background: var(--color-surface);
    color: var(--color-action);
  }
  :global(.part-AdminCredentials-secondary):hover {
    background: var(--color-surface-soft);
  }
  :global(.part-AdminCredentials-danger) {
    background: #9f1239;
    color: white;
    border-color: #9f1239;
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
    margin: 0 0 12px;
  }
  :global(.part-AdminCredentials-confirmation-actions) {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    justify-content: flex-end;
  }
  .help {
    margin: 8px 0 0;
  }
  form > .help {
    margin: 0;
  }
  @media (max-width: 600px) {
    .form-actions :global(.ui-action),
    :global(.part-AdminCredentials-confirmation-actions) :global(.ui-action) {
      width: 100%;
    }
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
