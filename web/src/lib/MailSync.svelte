<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api.ts';
  import type { MailAccount, ProbeError } from './mail-api.ts';
  import {
    validSyncRange,
    type MailSyncAPI,
    type SyncRun,
  } from './mail-sync-api.ts';
  import {
    createMailSyncController,
    emptySyncView,
    selectedSyncFolders,
    syncActive,
    type SyncRequests,
  } from './mail-sync-controller.ts';
  let {
    account,
    api,
    token,
    onDenied,
    refreshAccount,
    onBusyChange = () => {},
    requests = new Map(),
  }: {
    account: MailAccount;
    api: MailSyncAPI;
    token: TokenProvider;
    onDenied: () => void;
    refreshAccount: () => void;
    onBusyChange?: (saving: boolean) => void;
    requests?: SyncRequests;
  } = $props();
  let view = $state(emptySyncView());
  let controller = $state<ReturnType<typeof createMailSyncController>>();
  const folderCount = $derived(selectedSyncFolders(account));
  const busy = $derived(view.status === 'saving' || view.status === 'loading');
  const valid = $derived(
    validSyncRange(view.form.since, view.form.before, view.form.max_messages),
  );
  const canStart = $derived(
    !busy &&
      !view.uncertain &&
      !view.conflict &&
      account.state === 'ready' &&
      folderCount > 0 &&
      !view.runs.some(syncActive) &&
      valid,
  );
  const states: Record<SyncRun['state'], string> = {
    queued: '同期待ち',
    leased: '同期の準備中',
    running: 'IDを列挙中',
    completed: '列挙完了',
    limited: '上限に到達',
    failed: '同期に失敗',
    cancelled: '同期を取消済み',
  };
  const reasons: Record<ProbeError, string> = {
    '': '',
    authentication_failed:
      '認証に失敗しました。接続情報を更新し、接続確認を実行してください。',
    tls_failed: '安全な TLS 接続を確立できませんでした。',
    connection_failed: 'メールサーバーに接続できませんでした。',
    timeout: '同期が時間切れになりました。',
    response_too_large: 'メールサーバーの応答が大きすぎました。',
    folder_limit: '対象フォルダー数が上限を超えました。',
    invalid_response: 'メールサーバーの応答を確認できませんでした。',
    credentials_unavailable:
      '保存済みの接続情報を利用できません。接続情報を更新してください。',
    cancelled: '同期を取り消しました。',
    lease_expired:
      '同期が中断されました。必要なら条件を確認して再度実行してください。',
  };
  const errors = {
    authentication_required: 'ログインし直してください。',
    access_denied: '利用許可を確認してください。',
    dependency_unavailable:
      'サービスに接続できませんでした。最新の履歴を確認してください。',
    invalid_request: '期間と上限の入力条件を確認してください。',
    conflict:
      '設定または実行状態が更新されました。最新の状態を確認してください。',
    not_found: '対象の同期が見つかりません。最新の履歴を確認してください。',
  };
  const date = (value: string) =>
    new Intl.DateTimeFormat('ja-JP', {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: 'Asia/Tokyo',
    }).format(new Date(value));
  onMount(() => {
    controller = createMailSyncController(
      api,
      token,
      (next) => {
        view = next;
        onBusyChange(next.status === 'saving');
      },
      onDenied,
      refreshAccount,
      () => crypto.randomUUID(),
      requests,
    );
    controller.setAccount(account);
    void controller.refresh();
    return () => controller?.dispose();
  });
  $effect(() => {
    controller?.setAccount(account);
  });
  $effect(() => {
    if (
      !controller ||
      busy ||
      view.status === 'denied' ||
      !(view.uncertain || view.runs.some(syncActive))
    )
      return;
    const timer = setTimeout(() => {
      void controller?.poll();
    }, 2500);
    return () => clearTimeout(timer);
  });
</script>

<section class="sync-workspace" aria-label="メールIDの手動同期">
  <div class="sync-heading">
    <div>
      <p class="sync-eyebrow">MANUAL ID SYNC</p>
      <h4>メールIDの手動同期</h4>
    </div>
    <button
      id="sync-refresh"
      type="button"
      disabled={busy}
      onclick={() => controller?.refresh()}>同期履歴を更新</button
    >
  </div>
  <p class="sync-help">
    本文取得・AI送信は行わずIDだけを列挙します。件名・送信者も取得しません。フォルダー選択の保存と同期の開始は別の操作です。
  </p>
  <p class="sync-target">
    保存済みの対象フォルダー <strong>{folderCount} 件</strong><span
      >取得可能かつ選択可能なフォルダーだけが対象です。</span
    >
  </p>
  <p class="sync-help">
    期間は IMAP
    メールサーバーの受信日の暦日条件です。終了日は含みません。1〜366日、上限は1〜1000件で指定してください。
  </p>
  {#if account.state !== 'ready' || folderCount === 0}<p class="sync-feedback">
      開始には接続確認済みのアカウントと、保存済みの対象フォルダーが1件以上必要です。
    </p>{/if}
  {#if view.conflict}<p class="sync-feedback" role="alert">
      フォルダー選択などの設定が更新された可能性があります。入力と元の設定バージョンは保持しています。最新の保存済み設定を確認し、「同期条件を破棄」で基準を更新してから入力し直してください。
    </p>{/if}
  <form onsubmit={(event) => event.preventDefault()}>
    <div class="sync-fields">
      <label for="sync-since"
        >開始日（含む）<input
          id="sync-since"
          type="date"
          min="1900-01-01"
          max="9999-12-31"
          required
          value={view.form.since}
          disabled={view.status === 'saving' || view.uncertain}
          oninput={(event) =>
            controller?.edit({ since: event.currentTarget.value })}
        /></label
      >
      <label for="sync-before"
        >終了日（含まない）<input
          id="sync-before"
          type="date"
          min="1900-01-01"
          max="9999-12-31"
          required
          value={view.form.before}
          disabled={view.status === 'saving' || view.uncertain}
          oninput={(event) =>
            controller?.edit({ before: event.currentTarget.value })}
        /></label
      >
      <label for="sync-max"
        >今回確認するIDの上限<input
          id="sync-max"
          type="number"
          min="1"
          max="1000"
          step="1"
          placeholder="例: 100"
          required
          value={view.form.max_messages}
          disabled={view.status === 'saving' || view.uncertain}
          oninput={(event) =>
            controller?.edit({ max_messages: event.currentTarget.value })}
        /></label
      >
    </div>
    <p class="sync-confirm">
      開始前に条件を確認：保存済み {folderCount} フォルダー / {view.form
        .since || '開始日未入力'} 〜 {view.form.before ||
        '終了日未入力'}（終了日を除く）/ 上限 {view.form.max_messages ||
        '未入力'} 件
    </p>
    <div class="sync-actions">
      <button
        id="sync-start"
        class="sync-primary"
        type="submit"
        disabled={!canStart}
        onclick={() => controller?.start()}>入力した条件で同期を開始</button
      ><button
        id="sync-discard"
        type="button"
        disabled={view.status === 'saving' || view.uncertain}
        onclick={() => controller?.discard()}>同期条件を破棄</button
      >
    </div>
  </form>
  <div aria-live="polite" aria-atomic="true">
    {#if view.status === 'loading'}<p class="sync-feedback">
        同期履歴を確認しています。
      </p>{:else if view.status === 'saving'}<p class="sync-feedback">
        同期の操作を送信しています。
      </p>{/if}
    {#if view.error}<p class="sync-feedback" role="alert">
        {errors[view.error]}
      </p>{/if}
    {#if view.uncertain}<p class="sync-feedback">
        開始要求が受け付けられたか確認できません。履歴を確認し、必要なら同じ要求を再送してください。条件と要求IDは保持しています。
      </p>
      <button
        id="sync-retry"
        type="button"
        disabled={busy}
        onclick={() => controller?.retry()}>同じ要求を再送</button
      >{/if}
  </div>
  <div class="sync-history-heading">
    <h5>同期履歴</h5>
    <span>最新20件 · 日時は日本時間</span>
  </div>
  <ol class="sync-history">
    {#each view.runs as run (run.id)}
      <li>
        <div class="sync-heading">
          <strong class:sync-limited={run.state === 'limited'}
            >{states[run.state]}</strong
          ><time datetime={run.created_at}>{date(run.created_at)}</time>
        </div>
        <p class="sync-range">
          {run.since} 〜 {run.before}（終了日を除く）· 上限 {run.max_messages} 件
        </p>
        <dl>
          <div>
            <dt>今回確認したID</dt>
            <dd>{run.examined_count} 件</dd>
          </div>
          <div>
            <dt>新規登録</dt>
            <dd>{run.added_count} 件</dd>
          </div>
          <div>
            <dt>完了フォルダー</dt>
            <dd>{run.completed_folders} / {run.folder_count}</dd>
          </div>
        </dl>
        {#if run.state === 'limited'}<p class="sync-help">
            指定した上限で停止しました。対象期間のすべてを確認できたとは限りません。
          </p>{/if}
        {#if run.error_code}<p class="sync-help">
            {reasons[run.error_code]}
          </p>{/if}
        {#if syncActive(run)}<button
            type="button"
            disabled={view.status === 'saving'}
            onclick={() => controller?.cancel(run.id)}>同期を停止</button
          >{/if}
      </li>
    {:else}<li class="sync-empty">
        同期履歴はありません。条件を入力して明示的に開始してください。
      </li>{/each}
  </ol>
</section>

<style>
  .sync-workspace {
    min-width: 0;
    margin-top: 1.75rem;
    padding-top: 1.5rem;
    border-top: 1px solid var(--color-border);
  }
  h4,
  h5,
  p,
  dl,
  dd {
    margin: 0;
  }
  h4 {
    font-size: 1rem;
  }
  h5 {
    font-size: 0.9rem;
  }
  .sync-heading,
  .sync-actions,
  .sync-history-heading {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.8rem;
  }
  .sync-eyebrow {
    color: var(--color-action);
    font-size: 0.75rem;
    letter-spacing: 0.12em;
    margin-bottom: 0.4rem;
  }
  .sync-help,
  .sync-range,
  .sync-confirm {
    color: var(--color-muted);
    font-size: 0.8rem;
    line-height: 1.9;
    margin: 0.7rem 0;
    overflow-wrap: anywhere;
  }
  .sync-target {
    font-size: 0.85rem;
    padding: 1rem;
    background: var(--color-surface-soft);
    border-radius: 0.5rem;
    line-height: 1.8;
  }
  .sync-target strong {
    margin-left: 0.5rem;
  }
  .sync-target span {
    display: block;
    font-size: 0.75rem;
    color: var(--color-muted);
  }
  .sync-fields {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 1rem;
    margin: 1rem 0;
  }
  .sync-fields label {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    min-width: 0;
    font-size: 0.75rem;
    font-weight: 600;
  }
  input {
    box-sizing: border-box;
    min-width: 0;
    width: 100%;
    border: 1px solid var(--color-border);
    border-radius: 0.45rem;
    padding: 0.75rem 0.5rem;
    font: inherit;
    font-size: 1rem;
    color: var(--color-text);
    background: var(--color-surface);
  }
  button {
    min-height: 2.8rem;
    max-width: 100%;
    padding: 0.65rem 1rem;
    border: 1px solid var(--color-action);
    border-radius: 0.5rem;
    background: var(--color-surface);
    color: var(--color-action);
    font: inherit;
    font-size: 0.8rem;
    line-height: 1.6;
    cursor: pointer;
  }
  .sync-primary {
    background: var(--color-action);
    color: var(--color-on-action);
  }
  button:disabled {
    opacity: 0.55;
    cursor: default;
  }
  button:focus-visible,
  input:focus-visible {
    outline: 2px solid var(--color-action);
    outline-offset: 2px;
  }
  .sync-actions {
    justify-content: flex-start;
  }
  .sync-feedback {
    margin: 0.8rem 0;
    padding: 0.8rem 1rem;
    border-left: 3px solid var(--color-action);
    background: var(--color-surface-soft);
    font-size: 0.8rem;
    line-height: 1.9;
    overflow-wrap: anywhere;
  }
  .sync-history-heading {
    margin: 1.6rem 0 0.8rem;
  }
  .sync-history-heading span,
  time {
    font-size: 0.75rem;
    color: var(--color-muted);
  }
  .sync-history {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  .sync-history li {
    min-width: 0;
    border: 1px solid var(--color-border);
    padding: 1rem;
    border-radius: 0.5rem;
    font-size: 0.85rem;
  }
  .sync-history li + li {
    margin-top: 0.75rem;
  }
  .sync-history dl {
    display: flex;
    flex-wrap: wrap;
    gap: 0.8rem 1.4rem;
    margin: 0.8rem 0;
  }
  .sync-history dt {
    font-size: 0.75rem;
    color: var(--color-muted);
  }
  .sync-history dd {
    margin-top: 0.25rem;
    font-size: 0.9rem;
    font-variant-numeric: tabular-nums;
  }
  .sync-limited {
    color: var(--color-action);
  }
  .sync-empty {
    color: var(--color-muted);
    line-height: 1.9;
  }
  @media (max-width: 1100px) {
    .sync-fields {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  @media (max-width: 400px) {
    .sync-actions {
      flex-direction: column;
      align-items: stretch;
    }
    .sync-history li {
      padding: 0.85rem;
    }
  }
</style>
