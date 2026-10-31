<script lang="ts">
  import Button from './Button.svelte';
  import ActionLink from './ActionLink.svelte';
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api.ts';
  import type { MailAPI, MailAccount, ProbeError } from './mail-api.ts';
  import MailSync from './MailSync.svelte';
  import type { MailSyncAPI } from './mail-sync-api.ts';
  import type { SyncRequests } from './mail-sync-controller.ts';
  import Modal from './Modal.svelte';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import { safeAnalysisReturn } from './setup-readiness.ts';
  import {
    createMailController,
    emptyMailView,
    probePending,
    mailFolderLabel,
    filterMailFolders,
  } from './mail-controller.ts';
  let {
    api,
    token,
    onDenied,
    syncAPI,
    returnTo = null,
    oauthResult = null,
    connectOnly = false,
    onConnected = () => {},
    onConnectClose = () => {},
    onFrozen = () => {},
  }: {
    api: MailAPI;
    token: TokenProvider;
    onDenied: () => void;
    syncAPI?: MailSyncAPI;
    returnTo?: string | null;
    oauthResult?: string | null;
    connectOnly?: boolean;
    onConnected?: (account: MailAccount) => void;
    onConnectClose?: () => void;
    onFrozen?: (frozen: boolean) => void;
  } = $props();
  const syncRequests: SyncRequests = new Map();
  let view = $state(emptyMailView());
  let query = $state('');
  let editing = $state(false);
  let syncSaving = $state(false);
  let confirmClose = $state(false);
  let notifiedClose = false;
  let controller: ReturnType<typeof createMailController> | undefined;
  const account = $derived(view.accounts.find((a) => a.id === view.selected));
  const providerNames: Record<MailAccount['provider'], string> = {
    fastmail: 'Fastmail',
    imap: 'IMAP',
    gmail: 'Gmail / Google Workspace',
    outlook: 'Outlook.com / Hotmail',
  };
  const oauth = $derived(
    view.form?.provider === 'gmail' || view.form?.provider === 'outlook',
  );
  const oauthReady = $derived(
    view.oauthProviders.some(
      (row) => row.provider === view.form?.provider && row.ready,
    ),
  );
  const busy = $derived(
    view.status === 'saving' || view.status === 'loading' || syncSaving,
  );
  const returnTarget = $derived(
    returnTo === null
      ? account
        ? `/analysis?tab=history&setup_account=${account.id}&account_id=${account.id}`
        : null
      : safeAnalysisReturn(returnTo),
  );
  const returnLocked = $derived(busy || view.uncertain || view.form !== null);
  const folders = $derived(filterMailFolders(account?.folders ?? [], query));
  const states: Record<MailAccount['state'], string> = {
    unverified: '接続未確認',
    ready: '接続確認済み',
    needs_reauth: '再認証が必要',
    disconnected: '解除済み',
  };
  const errors = {
    authentication_required: 'ログインし直してください。',
    access_denied: '利用許可を確認してください。',
    dependency_unavailable:
      'サービスに接続できませんでした。時間をおいて再試行してください。',
    invalid_request: '表示名、接続先、認証情報の入力条件を確認してください。',
    conflict:
      '別の操作で設定が更新されました。最新の状態に更新して内容を確認し、必要なら入力を破棄してからやり直してください。',
    not_found: '対象の接続情報が見つかりません。最新の状態に更新してください。',
  };
  const reasons: Record<ProbeError, string> = {
    '': '',
    authentication_failed:
      '認証に失敗しました。接続情報の更新または再認証を行ってください。',
    tls_failed: '安全な TLS 接続を確立できませんでした。',
    connection_failed: 'メールサービスに接続できませんでした。',
    timeout: '接続確認が時間切れになりました。',
    response_too_large: 'サーバーの応答が大きすぎました。',
    folder_limit: 'フォルダー数が取得上限を超えました。',
    invalid_response: 'サーバーの応答を確認できませんでした。',
    credentials_unavailable:
      '保存済みの接続情報を利用できません。接続情報を更新してください。',
    cancelled: '接続確認を中止しました。',
    lease_expired: '接続確認が中断されました。必要なら再度実行してください。',
  };
  const probeStates = {
    queued: '確認待ち',
    leased: '確認の準備中',
    running: '接続確認中',
    succeeded: '接続確認に成功',
    failed: '接続確認に失敗',
    cancelled: '接続確認を中止',
  };
  function date(value: string | null) {
    return value
      ? new Intl.DateTimeFormat('ja-JP', {
          dateStyle: 'medium',
          timeStyle: 'short',
          timeZone: 'Asia/Tokyo',
        }).format(new Date(value))
      : '未確認';
  }
  async function submitConnection() {
    const form = controller?.state().form;
    if (oauth) {
      await controller?.connectOAuth();
      return;
    }
    await controller?.save();
    const next = controller?.state();
    const saved = next?.accounts.find((row) => row.id === form?.id);
    if (
      form?.baseline === 0 &&
      saved &&
      next?.status === 'ready' &&
      next.success
    ) {
      if (connectOnly) {
        notifiedClose = true;
        controller?.close();
        onFrozen(false);
      }
      onConnected(saved);
    }
  }
  function closeConnection() {
    if (notifiedClose || !requestConnectionClose()) return;
    controller?.close();
    if (controller?.state().form) return;
    editing = false;
    onFrozen(false);
    if (connectOnly) {
      notifiedClose = true;
      onConnectClose();
    }
  }
  function requestConnectionClose() {
    if (busy || (connectOnly && view.uncertain)) return false;
    if (
      connectOnly &&
      (view.form?.credentialsDirty || view.form?.foldersDirty)
    ) {
      confirmClose = true;
      return false;
    }
    return true;
  }
  function discardConnection() {
    if (busy || view.uncertain) return;
    controller?.discard();
    confirmClose = false;
    closeConnection();
  }
  $effect(() => {
    onFrozen(
      busy ||
        view.uncertain ||
        !!view.form?.credentialsDirty ||
        !!view.form?.foldersDirty,
    );
  });
  onMount(() => {
    controller = createMailController(
      api,
      token,
      (next) => {
        view = next;
      },
      onDenied,
    );
    let active = true;
    let openedConnection = false;
    const initializeConnection = async () => {
      await controller?.refresh();
      if (
        active &&
        connectOnly &&
        !openedConnection &&
        controller?.state().status === 'ready'
      ) {
        openedConnection = true;
        controller.newAccount();
      }
    };
    void initializeConnection();
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      await controller?.poll();
      if (active) timer = setTimeout(poll, 2500);
    };
    timer = setTimeout(poll, 2500);
    return () => {
      active = false;
      clearTimeout(timer);
      controller?.dispose();
    };
  });
</script>

{#snippet feedback()}
  {#if view.error}<p class="mail-feedback" role="alert">
      {errors[view.error]}
    </p>{/if}
  {#if view.success}<p class="mail-feedback" role="status">
      {view.success}
    </p>{/if}
  {#if view.uncertain}<p class="mail-feedback" role="alert">
      接続確認が受け付けられたか確認できません。「同じ接続確認を再送する」で受付状況を確認できます。新しい確認は重複作成しません。
    </p>{/if}
{/snippet}

<section class="mail-workspace library-page" aria-label="自分のメール連携">
  {#if !connectOnly}
    <LibraryHeader
      title="メール連携"
      description="接続情報と、対象のフォルダーを管理します。"
      >{#snippet action()}
        <Button
          wrap
          variant="primary"
          class={['primary']}
          disabled={busy || view.accounts.length >= 10 || view.uncertain}
          onclick={() => {
            query = '';
            editing = false;
            controller?.newAccount();
          }}>＋ メールを接続</Button
        >
      {/snippet}</LibraryHeader
    >
    <LibraryPanel>
      <div class="mail-list-heading library-toolbar">
        <span>登録済み {view.accounts.length} 件</span><Button
          wrap
          id="mail-refresh"
          class={['mail-button', 'part-MailAccounts-mail-button']}
          disabled={busy}
          onclick={() => controller?.refresh()}>更新</Button
        >
      </div>
      {#if oauthResult === 'connected'}<p class="mail-feedback" role="status">
          メールの認証が完了しました。接続を確認して対象フォルダーを選んでください。
          <ActionLink
            wrap
            class={['mail-button', 'part-MailAccounts-mail-button']}
            href="/import">取り込みを続ける</ActionLink
          >
        </p>{:else if oauthResult === 'failed'}<p
          class="mail-feedback"
          role="alert"
        >
          メールの認証が完了しませんでした。接続設定からもう一度お試しください。
        </p>{/if}
      {#if !view.form}{@render feedback()}{/if}
      <section class="mail-list" aria-label="メールアカウント一覧">
        {#if view.status === 'loading' && !view.accounts.length}<p
            role="status"
          >
            読み込み中です…
          </p>{:else if !view.accounts.length}<p class="mail-empty">
            アカウントはまだ登録されていません。接続情報を追加してください。
          </p>{/if}
        <ul>
          {#each view.accounts as row (row.id)}<li>
              <div class="mail-account-row">
                <div class="mail-account-name">
                  <strong>{row.label}</strong><span
                    >{providerNames[row.provider]} · 対象 {row.folders.filter(
                      (folder) => folder.selected,
                    ).length} フォルダー</span
                  >
                </div>
                <span class="mail-badge">{states[row.state]}</span><Button
                  wrap
                  class={['mail-button', 'part-MailAccounts-mail-button']}
                  aria-label={`${row.label}の接続設定`}
                  disabled={busy}
                  onclick={() => {
                    query = '';
                    editing = false;
                    controller?.select(row.id);
                  }}>接続を編集</Button
                >
              </div>
            </li>{/each}
        </ul>
      </section>
      <div class="library-content">
        <details class="mail-guide">
          <summary>連携について</summary>
          <p>
            Gmail / Google Workspace と Outlook.com / Hotmail
            は各サービスの認証画面で、メールの読み取りを許可します。Google
            Workspace は組織の管理者による許可が必要な場合があります。アプリの
            Google ログインとは別の認可です。 Fastmail
            は専用のアプリパスワードで <code>imap.fastmail.com:993</code
            >（TLS）に接続します。 汎用 IMAP は 993（TLS）または
            143（STARTTLS）を使います。保存だけでは同期しません。本文取得・AI
            送信は「メール解析」で期間と条件を一度確認してから開始します。開始後は、新着の確認と解析を自動で続けます。
          </p>
        </details>
        {#if returnTarget}<div class="mail-return">
            {#if returnLocked}<span class="mail-button" aria-disabled="true"
                >解析へ戻って条件を確認</span
              >{:else}<ActionLink
                wrap
                class={['mail-button', 'part-MailAccounts-mail-button']}
                href={returnTarget}>解析へ戻って条件を確認</ActionLink
              >{/if}
          </div>{/if}
      </div>
    </LibraryPanel>
  {/if}
  {#if connectOnly && !view.form}
    {@render feedback()}
    {#if view.status === 'loading'}<p role="status">
        接続を読み込み中です…
      </p>{:else if view.accounts.length >= 10}<p role="alert">
        接続できるメールは10件までです。
      </p>{/if}
    {#if view.status === 'error'}<Button
        wrap
        class={['mail-button', 'part-MailAccounts-mail-button']}
        onclick={async () => {
          await controller?.refresh();
          if (controller?.state().status === 'ready') controller.newAccount();
        }}>再試行</Button
      >{/if}
    <Button
      wrap
      class={['mail-button', 'part-MailAccounts-mail-button']}
      disabled={busy}
      onclick={closeConnection}>戻る</Button
    >
  {/if}
  <Modal
    open={view.form !== null}
    title={account?.label ?? 'メールを追加'}
    {busy}
    canClose={requestConnectionClose}
    onclose={closeConnection}
  >
    <div class="mail-detail">
      {@render feedback()}
      {#if connectOnly && confirmClose}
        <div class="mail-feedback" role="alert">
          <p>保存していない入力があります。入力を破棄して閉じますか？</p>
          <div class="mail-actions">
            <Button
              wrap
              class={['mail-button', 'part-MailAccounts-mail-button']}
              disabled={busy || view.uncertain}
              onclick={discardConnection}>入力を破棄して閉じる</Button
            >
            <Button
              wrap
              class={['mail-button', 'part-MailAccounts-mail-button']}
              disabled={busy || view.uncertain}
              onclick={() => {
                confirmClose = false;
              }}>編集を続ける</Button
            >
          </div>
        </div>
      {/if}
      {#if view.form}
        <div class="mail-section-heading">
          <h3>
            {account
              ? `${providerNames[account.provider]} の接続設定`
              : '接続情報'}
          </h3>
          {#if account}<span class="mail-badge">{states[account.state]}</span
            >{/if}
        </div>
        {#if !account || editing}
          <form
            onsubmit={(event) => {
              event.preventDefault();
              void submitConnection();
            }}
            autocomplete="off"
          >
            <p class="mail-help">
              {oauth
                ? 'メールの読み取りを許可します。メールの送信・削除は行いません。認証情報はサービスの認証画面で入力します。'
                : account
                  ? '接続情報を更新する場合は、メールアドレスと新しいアプリパスワードを再入力してください。保存すると以前の接続確認とフォルダー選択をリセットします。'
                  : view.form.provider === 'imap'
                    ? '本人が所有するメールアカウントのログイン名と、メールサーバーのパスワードを入力してください。'
                    : '本人が所有するメールアドレスと、メール利用を許可したアプリパスワードを入力してください。'}
            </p>
            <div class="mail-fields">
              <label for="mail-provider"
                >メールサービス<select
                  id="mail-provider"
                  value={view.form.provider}
                  disabled={busy || !!account}
                  onchange={(event) =>
                    controller?.edit({
                      provider: event.currentTarget
                        .value as MailAccount['provider'],
                    })}
                >
                  {#each Object.entries(providerNames) as [value, label]}<option
                      {value}>{label}</option
                    >{/each}
                </select><span>登録後は同じメールサービスで再接続します。</span
                ></label
              >
              <label for="mail-label"
                >表示名<input
                  id="mail-label"
                  type="text"
                  required
                  value={view.form.label}
                  disabled={busy || view.uncertain}
                  oninput={(event) =>
                    controller?.edit({ label: event.currentTarget.value })}
                /><span>1〜80 文字。前後の空白は除きます。</span></label
              >
              {#if !oauth}
                {#if view.form.provider === 'imap'}
                  <label for="mail-host"
                    >IMAP サーバー<input
                      id="mail-host"
                      type="text"
                      required
                      autocomplete="off"
                      spellcheck="false"
                      value={view.form.host}
                      disabled={busy || view.uncertain}
                      oninput={(event) =>
                        controller?.edit({ host: event.currentTarget.value })}
                    /><span
                      >例:
                      imap.example.com。公開されたサーバー名を入力してください。</span
                    ></label
                  >
                  <label for="mail-port"
                    >暗号化接続<select
                      id="mail-port"
                      value={view.form.port}
                      disabled={busy || view.uncertain}
                      onchange={(event) =>
                        controller?.edit({
                          port: Number(event.currentTarget.value),
                        })}
                      ><option value={993}>993 — TLS</option><option value={143}
                        >143 — STARTTLS</option
                      ></select
                    ><span>STARTTLS が利用できない場合は接続しません。</span
                    ></label
                  >
                {/if}
                <label for="mail-login"
                  >{view.form.provider === 'imap'
                    ? 'ログイン名'
                    : 'メールアドレス'}<input
                    id="mail-login"
                    name="fastmail-login"
                    type={view.form.provider === 'imap' ? 'text' : 'email'}
                    maxlength="254"
                    required
                    autocomplete="off"
                    spellcheck="false"
                    autocapitalize="none"
                    value={view.form.login}
                    disabled={busy || view.uncertain}
                    oninput={(event) =>
                      controller?.edit({ login: event.currentTarget.value })}
                  /><span
                    >{view.form.provider === 'imap'
                      ? 'メールサーバーに登録したログイン名。'
                      : 'ログインに使用する完全なメールアドレス。'}</span
                  ></label
                >
                <label for="mail-password"
                  >{view.form.provider === 'imap'
                    ? 'パスワード'
                    : '専用のアプリパスワード'}<input
                    id="mail-password"
                    name="fastmail-app-password"
                    type="password"
                    minlength={view.form.provider === 'imap' ? 1 : 8}
                    maxlength="1024"
                    required
                    autocomplete="new-password"
                    spellcheck="false"
                    value={view.form.app_password}
                    disabled={busy || view.uncertain}
                    oninput={(event) =>
                      controller?.edit({
                        app_password: event.currentTarget.value,
                      })}
                  /><span
                    >{view.form.provider === 'imap'
                      ? 'サーバーが指定するパスワード。保存後の再表示はできません。'
                      : '空白を含まない半角英数・記号 8〜1024 文字。保存後の再表示はできません。'}</span
                  ></label
                >
              {/if}
            </div>
            {#if oauth}
              {#if !oauthReady}<p class="mail-help" role="status">
                  管理者によるメール連携アプリの準備待ちです。準備が完了すると接続できます。
                </p>{:else}<p class="mail-help">
                  {view.form.provider === 'gmail'
                    ? '個人 Gmail と Google Workspace に対応しています。Workspace の組織設定により、管理者の許可が必要な場合があります。'
                    : '個人の Outlook.com / Hotmail アカウントに対応しています。'}
                </p>{/if}
            {/if}
            <div class="mail-actions">
              <Button
                wrap
                type="submit"
                class={[
                  'mail-button mail-primary',
                  'part-MailAccounts-mail-button',
                  'part-MailAccounts-mail-primary',
                ]}
                disabled={busy || view.uncertain || (oauth && !oauthReady)}
                >{oauth
                  ? `${providerNames[view.form.provider]} で認証する`
                  : '接続情報を保存'}</Button
              ><Button
                wrap
                id="mail-discard"
                type="button"
                class={['mail-button', 'part-MailAccounts-mail-button']}
                disabled={busy}
                onclick={() => controller?.discard()}>入力を破棄</Button
              >
            </div>
          </form>
        {:else}<Button
            wrap
            class={['mail-button', 'part-MailAccounts-mail-button']}
            onclick={() => {
              editing = true;
            }}>接続情報を編集</Button
          >{/if}
        {#if account}
          <section class="mail-step" aria-label="接続確認">
            <h4>接続を確認</h4>
            <p class="mail-help">
              保存済みの接続情報でフォルダー一覧のみを取得します。メール本文の取得や
              AI の呼び出しはありません。
            </p>
            <div class="mail-probe">
              <p>
                <strong
                  >{account.latest_probe
                    ? probeStates[account.latest_probe.state]
                    : '接続確認はまだ実行されていません'}</strong
                ><span>最終確認: {date(account.checked_at)}（日本時間）</span>
              </p>
              <Button
                wrap
                class={['mail-button', 'part-MailAccounts-mail-button']}
                disabled={busy ||
                  account.state === 'disconnected' ||
                  probePending(account.latest_probe) ||
                  view.form.credentialsDirty ||
                  view.form.foldersDirty}
                onclick={() => controller?.requestProbe()}
                >{view.uncertain
                  ? '同じ接続確認を再送する'
                  : '接続を確認する'}</Button
              >
            </div>
            {#if account.latest_probe?.error_code || account.last_error}<p
                class="mail-help"
              >
                {reasons[
                  account.latest_probe?.error_code || account.last_error
                ]}
              </p>{/if}
            {#if view.form.credentialsDirty || view.form.foldersDirty}<p
                class="mail-help"
              >
                編集中の内容を保存または破棄してから接続確認を実行してください。
              </p>{/if}
          </section>
          <section class="mail-step" aria-label="フォルダー選択">
            <div class="mail-section-heading">
              <h4>対象フォルダー</h4>
              <span>{view.form.folder_ids.length} 件選択</span>
            </div>
            <p class="mail-help">
              フォルダー選択を保存しても、メール同期はまだ始まりません。選択なしでも保存できます。
            </p>
            <label class="mail-filter" for="mail-folder-filter"
              >フォルダー名で絞り込む<input
                id="mail-folder-filter"
                type="search"
                bind:value={query}
                placeholder="例: INBOX"
              /><span
                >{folders.length} / {account.folders.length} 件表示 · 表示外の選択も保持します</span
              ></label
            >
            <div class="mail-folder-list">
              {#each folders as folder (folder.id)}<label
                  class="mail-folder-row"
                  ><input
                    type="checkbox"
                    checked={view.form.folder_ids.includes(folder.id)}
                    disabled={busy ||
                      view.uncertain ||
                      account.state !== 'ready' ||
                      !folder.available ||
                      !folder.selectable ||
                      probePending(account.latest_probe)}
                    onchange={(event) =>
                      controller?.toggleFolder(
                        folder.id,
                        event.currentTarget.checked,
                      )}
                  /><span
                    ><strong>{mailFolderLabel(folder)}</strong><small
                      >{!folder.available
                        ? '取得不能 · 現在の一覧にありません'
                        : !folder.selectable
                          ? '選択不可（NoSelect）'
                          : '選択可能'}</small
                    ></span
                  ></label
                >{:else}<p class="mail-empty">
                  {account.folders.length
                    ? '条件に一致するフォルダーがありません。'
                    : '接続確認に成功すると、フォルダー一覧を表示します。'}
                </p>{/each}
            </div>
            <Button
              wrap
              class={[
                'mail-button mail-primary',
                'part-MailAccounts-mail-button',
                'part-MailAccounts-mail-primary',
              ]}
              disabled={busy ||
                view.uncertain ||
                account.state !== 'ready' ||
                view.form.credentialsDirty ||
                probePending(account.latest_probe)}
              onclick={() => controller?.saveFolders()}
              >フォルダー選択を保存</Button
            >
            {#if account.state === 'ready' && account.folders.some((f) => f.selected) && !view.form.credentialsDirty && !view.form.foldersDirty}<ActionLink
                wrap
                class={['mail-button', 'part-MailAccounts-mail-button']}
                href={returnTarget ??
                  `/analysis?tab=history&setup_account=${account.id}&account_id=${account.id}`}
                >このアカウントの自動処理を設定</ActionLink
              >{/if}
          </section>
          {#if syncAPI}<details class="mail-step">
              <summary>メールの同期</summary>
              {#key account.id}<MailSync
                  {account}
                  api={syncAPI}
                  {token}
                  {onDenied}
                  requests={syncRequests}
                  onBusyChange={(saving) => {
                    syncSaving = saving;
                  }}
                  refreshAccount={() => {
                    void controller?.refresh();
                  }}
                />{/key}
            </details>{/if}
          <details class="mail-step mail-disconnect">
            <summary>連携を解除</summary>
            <p class="mail-help">
              保存済みの資格情報と進行中の確認を無効化します。再接続には新しい接続情報が必要です。
            </p>
            {#if view.disconnectConfirmation}<p>
                「{account.label}」の連携を解除します。
              </p>
              <label class="mail-confirm"
                ><input
                  type="checkbox"
                  checked
                  onchange={(event) =>
                    controller?.confirmDisconnect(event.currentTarget.checked)}
                />このアカウントの連携解除を確認しました</label
              >
              <div class="mail-actions">
                <Button
                  wrap
                  class={['mail-button', 'part-MailAccounts-mail-button']}
                  disabled={busy}
                  onclick={() => controller?.disconnect()}
                  >確認した連携を解除する</Button
                ><Button
                  wrap
                  class={['mail-button', 'part-MailAccounts-mail-button']}
                  disabled={busy}
                  onclick={() => controller?.confirmDisconnect(false)}
                  >キャンセル</Button
                >
              </div>{:else}<Button
                wrap
                class={['mail-button', 'part-MailAccounts-mail-button']}
                disabled={busy || account.state === 'disconnected'}
                onclick={() => controller?.confirmDisconnect(true)}
                >連携を解除する</Button
              >{/if}
          </details>
        {/if}
      {/if}
    </div>
  </Modal>
</section>

<style>
  .mail-workspace {
    margin-top: 0;
    min-width: 0;
  }
  .mail-section-heading,
  .mail-probe,
  .mail-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
  }
  h3,
  h4,
  p {
    margin: 0;
  }
  h3 {
    font-size: 1.05rem;
    overflow-wrap: anywhere;
  }
  h4 {
    font-size: 0.95rem;
  }
  p {
    line-height: 1.9;
  }
  .mail-help,
  .mail-empty {
    color: var(--color-muted);
    font-size: 0.85rem;
  }
  .mail-guide {
    margin: 1.5rem 0 0;
    color: var(--color-muted);
    font-size: 0.82rem;
  }
  .mail-guide p {
    margin-top: 0.5rem;
    color: var(--color-muted);
  }
  .mail-return {
    margin-top: 1.5rem;
  }
  .mail-return [aria-disabled='true'] {
    opacity: 0.55;
  }
  code {
    overflow-wrap: anywhere;
  }
  .mail-list-heading {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
    margin-top: 24px;
    font-size: 12px;
    color: var(--color-muted);
    border-bottom: 1px solid var(--color-border);
    padding-bottom: 8px;
  }
  .mail-section-heading {
    margin-bottom: 1rem;
  }
  .mail-section-heading > span {
    font-size: 0.75rem;
    color: var(--color-muted);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .mail-account-row {
    display: flex;
    align-items: center;
    gap: 16px;
    border-bottom: 1px solid var(--color-border);
    padding: 20px 22px;
    overflow-wrap: anywhere;
  }
  .mail-account-name {
    flex: 1;
    min-width: 0;
  }
  .mail-account-name span {
    display: block;
    font-size: 0.8rem;
    color: var(--color-muted);
    margin-top: 0.4rem;
  }
  .mail-detail {
    min-width: 0;
    padding: 0;
  }
  .mail-badge {
    background: var(--color-surface-soft);
    color: var(--color-action);
    border-radius: 4px;
    padding: 4px 8px;
    font-size: 12px;
  }
  .mail-help {
    margin: 0.6rem 0 1rem;
  }
  .mail-fields {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1rem;
  }
  .mail-fields label:first-child {
    grid-column: 1 / -1;
  }
  .mail-fields label,
  .mail-filter {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    font-size: 0.8rem;
    font-weight: 600;
    min-width: 0;
  }
  .mail-fields label > span,
  .mail-filter > span {
    color: var(--color-muted);
    font-size: 0.75rem;
    line-height: 1.7;
    font-weight: 400;
  }
  input:not([type='checkbox']),
  select {
    width: 100%;
    min-width: 0;
    border: 1px solid var(--color-border);
    border-radius: 0.45rem;
    padding: 0.75rem;
    color: var(--color-text);
    background: var(--color-surface);
    font: inherit;
    font-size: 1rem;
  }
  input:focus-visible {
    outline: 2px solid var(--color-action);
    outline-offset: 2px;
  }
  input[type='checkbox'] {
    width: 1.1rem;
    height: 1.1rem;
    flex: 0 0 auto;
    accent-color: var(--color-action);
  }
  .mail-actions {
    justify-content: flex-start;
    margin-top: 1.2rem;
  }
  :global(.part-MailAccounts-mail-primary) {
    background: var(--color-action);
    color: var(--color-on-action);
  }
  .mail-step {
    margin-top: 1.75rem;
    padding-top: 1.5rem;
    border-top: 1px solid var(--color-border);
  }
  .mail-probe {
    background: var(--color-surface-soft);
    padding: 1rem;
    border-radius: 0.5rem;
    font-size: 0.82rem;
  }
  .mail-probe span {
    display: block;
    color: var(--color-muted);
    font-size: 0.75rem;
    margin-top: 0.3rem;
  }
  .mail-filter {
    margin: 1rem 0;
  }
  .mail-folder-list {
    max-height: 24rem;
    overflow-y: auto;
    border: 1px solid var(--color-border);
    border-radius: 0.5rem;
    margin-bottom: 1rem;
  }
  .mail-folder-row {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    padding: 0.85rem 1rem;
    cursor: pointer;
    min-width: 0;
  }
  .mail-folder-row + .mail-folder-row {
    border-top: 1px solid var(--color-border);
  }
  .mail-folder-row span {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .mail-folder-row strong {
    font-size: 0.82rem;
    font-weight: 500;
  }
  .mail-folder-row small {
    display: block;
    margin-top: 0.25rem;
    color: var(--color-muted);
    font-size: 0.75rem;
  }
  .mail-empty {
    padding: 1rem 0.5rem;
  }
  .mail-feedback {
    border-left: 3px solid var(--color-action);
    background: var(--color-surface-soft);
    padding: 0.8rem 1rem;
    margin-bottom: 1rem;
    font-size: 0.85rem;
    overflow-wrap: anywhere;
  }
  .mail-confirm {
    display: flex;
    gap: 0.65rem;
    align-items: center;
    font-size: 0.8rem;
    margin-top: 0.75rem;
  }
  @media (max-width: 850px) {
    .mail-fields {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  @media (max-width: 400px) {
    .mail-actions {
      align-items: stretch;
      flex-direction: column;
    }
    .mail-account-row {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      gap: 10px;
    }
    .mail-account-name {
      grid-column: 1 / -1;
    }
  }
</style>
