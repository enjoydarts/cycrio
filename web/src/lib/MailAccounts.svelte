<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api.ts';
  import type { MailAPI, MailAccount, ProbeError } from './mail-api.ts';
  import MailSync from './MailSync.svelte';
  import type { MailSyncAPI } from './mail-sync-api.ts';
  import type { SyncRequests } from './mail-sync-controller.ts';
  import Modal from './Modal.svelte';
  import {
    createMailController,
    emptyMailView,
    probePending,
  } from './mail-controller.ts';
  let {
    api,
    token,
    onDenied,
    syncAPI,
  }: {
    api: MailAPI;
    token: TokenProvider;
    onDenied: () => void;
    syncAPI?: MailSyncAPI;
  } = $props();
  const syncRequests: SyncRequests = new Map();
  let view = $state(emptyMailView());
  let query = $state('');
  let editing = $state(false);
  let syncSaving = $state(false);
  let controller: ReturnType<typeof createMailController> | undefined;
  const account = $derived(view.accounts.find((a) => a.id === view.selected));
  const busy = $derived(
    view.status === 'saving' || view.status === 'loading' || syncSaving,
  );
  const folders = $derived(
    (account?.folders ?? []).filter((folder) =>
      folder.name.toLocaleLowerCase().includes(query.toLocaleLowerCase()),
    ),
  );
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
    invalid_request:
      '表示名、メールアドレス、アプリパスワードの入力条件を確認してください。',
    conflict:
      '別の操作で設定が更新されました。最新の状態に更新して内容を確認し、必要なら入力を破棄してからやり直してください。',
    not_found: '対象の接続情報が見つかりません。最新の状態に更新してください。',
  };
  const reasons: Record<ProbeError, string> = {
    '': '',
    authentication_failed:
      '認証に失敗しました。専用のアプリパスワードを確認してください。',
    tls_failed: '安全な TLS 接続を確立できませんでした。',
    connection_failed: 'Fastmail に接続できませんでした。',
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
  onMount(() => {
    controller = createMailController(
      api,
      token,
      (next) => {
        view = next;
      },
      onDenied,
    );
    void controller.refresh();
    let active = true;
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

<section class="mail-workspace" aria-label="自分のメール連携">
  <div class="mail-heading">
    <div>
      <h2>メール連携</h2>
      <p>接続情報と、対象のフォルダーを管理します。</p>
    </div>
    <button
      class="mail-button mail-primary"
      disabled={busy || view.accounts.length >= 10 || view.uncertain}
      onclick={() => {
        query = '';
        editing = false;
        controller?.newAccount();
      }}>＋ メールを接続</button
    >
  </div>
  <div class="mail-list-heading">
    <span>登録済み {view.accounts.length} 件</span><button
      id="mail-refresh"
      class="mail-button"
      disabled={busy}
      onclick={() => controller?.refresh()}>更新</button
    >
  </div>
  {#if !view.form}{@render feedback()}{/if}
  <section class="mail-list" aria-label="メールアカウント一覧">
    {#if view.status === 'loading' && !view.accounts.length}<p role="status">
        読み込み中です…
      </p>{:else if !view.accounts.length}<p class="mail-empty">
        アカウントはまだ登録されていません。接続情報を追加してください。
      </p>{/if}
    <ul>
      {#each view.accounts as row (row.id)}<li>
          <div class="mail-account-row">
            <div class="mail-account-name">
              <strong>{row.label}</strong><span
                >Fastmail · 対象 {row.folders.filter(
                  (folder) => folder.selected,
                ).length} フォルダー</span
              >
            </div>
            <span class="mail-badge">{states[row.state]}</span><button
              class="mail-button"
              aria-label={`${row.label}の接続設定`}
              disabled={busy}
              onclick={() => {
                query = '';
                editing = false;
                controller?.select(row.id);
              }}>接続を編集</button
            >
          </div>
        </li>{/each}
    </ul>
  </section>
  <details class="mail-guide">
    <summary>連携について</summary>
    <p>
      Fastmail 専用のアプリパスワードで <code>imap.fastmail.com:993</code
      >（TLS）に接続します。Google
      ログインとは別の設定です。保存だけでは同期しません。本文取得・AI
      送信は「メール解析」で個別に確認して実行します。
    </p>
  </details>
  <Modal
    open={view.form !== null}
    title={account?.label ?? 'Fastmail を追加'}
    {busy}
    onclose={() => {
      controller?.close();
      editing = false;
    }}
  >
    <div class="mail-detail">
      {@render feedback()}
      {#if view.form}
        <div class="mail-section-heading">
          <h3>{account ? 'Fastmail の接続設定' : '接続情報'}</h3>
          {#if account}<span class="mail-badge">{states[account.state]}</span
            >{/if}
        </div>
        {#if !account || editing}
          <form
            onsubmit={(event) => {
              event.preventDefault();
              void controller?.save();
            }}
            autocomplete="off"
          >
            <p class="mail-help">
              {account
                ? '接続情報を更新する場合は、メールアドレスと新しいアプリパスワードを再入力してください。保存すると以前の接続確認とフォルダー選択をリセットします。'
                : '本人が所有する Fastmail のメールアドレスと、メール利用を許可した専用のアプリパスワードを入力してください。'}
            </p>
            <div class="mail-fields">
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
              <label for="mail-login"
                >Fastmail のメールアドレス<input
                  id="mail-login"
                  name="fastmail-login"
                  type="email"
                  maxlength="254"
                  required
                  autocomplete="off"
                  spellcheck="false"
                  autocapitalize="none"
                  value={view.form.login}
                  disabled={busy || view.uncertain}
                  oninput={(event) =>
                    controller?.edit({ login: event.currentTarget.value })}
                /><span>ログインに使用する完全なメールアドレス。</span></label
              >
              <label for="mail-password"
                >専用のアプリパスワード<input
                  id="mail-password"
                  name="fastmail-app-password"
                  type="password"
                  minlength="8"
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
                  >空白を含まない半角英数・記号 8〜1024
                  文字。保存後の再表示はできません。</span
                ></label
              >
            </div>
            <div class="mail-actions">
              <button
                type="submit"
                class="mail-button mail-primary"
                disabled={busy || view.uncertain}>接続情報を保存</button
              ><button
                id="mail-discard"
                type="button"
                class="mail-button"
                disabled={busy}
                onclick={() => controller?.discard()}>入力を破棄</button
              >
            </div>
          </form>
        {:else}<button
            class="mail-button"
            onclick={() => {
              editing = true;
            }}>接続情報を編集</button
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
              <button
                class="mail-button"
                disabled={busy ||
                  account.state === 'disconnected' ||
                  probePending(account.latest_probe) ||
                  view.form.credentialsDirty ||
                  view.form.foldersDirty}
                onclick={() => controller?.requestProbe()}
                >{view.uncertain
                  ? '同じ接続確認を再送する'
                  : '接続を確認する'}</button
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
                    ><strong>{folder.name}</strong><small
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
            <button
              class="mail-button mail-primary"
              disabled={busy ||
                view.uncertain ||
                account.state !== 'ready' ||
                view.form.credentialsDirty ||
                probePending(account.latest_probe)}
              onclick={() => controller?.saveFolders()}
              >フォルダー選択を保存</button
            >
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
                <button
                  class="mail-button"
                  disabled={busy}
                  onclick={() => controller?.disconnect()}
                  >確認した連携を解除する</button
                ><button
                  class="mail-button"
                  disabled={busy}
                  onclick={() => controller?.confirmDisconnect(false)}
                  >キャンセル</button
                >
              </div>{:else}<button
                class="mail-button"
                disabled={busy || account.state === 'disconnected'}
                onclick={() => controller?.confirmDisconnect(true)}
                >連携を解除する</button
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
  .mail-heading,
  .mail-section-heading,
  .mail-probe,
  .mail-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
  }
  .mail-heading {
    align-items: flex-start;
  }
  h2,
  h3,
  h4,
  p {
    margin: 0;
  }
  h2 {
    font-size: 1.1rem;
    margin-bottom: 0.5rem;
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
  .mail-heading p,
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
  .mail-list-heading .mail-button {
    border-color: transparent;
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
    padding: 18px 0;
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
  input:not([type='checkbox']) {
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
  .mail-button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-height: 2.8rem;
    max-width: 100%;
    border: 1px solid var(--color-action);
    border-radius: 0.5rem;
    padding: 0.65rem 1rem;
    background: var(--color-surface);
    color: var(--color-action);
    font: inherit;
    font-size: 0.8rem;
    line-height: 1.6;
    cursor: pointer;
  }
  .mail-primary {
    background: var(--color-action);
    color: var(--color-on-action);
  }
  button:disabled {
    opacity: 0.55;
    cursor: default;
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
    .mail-heading {
      gap: 0.75rem;
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
