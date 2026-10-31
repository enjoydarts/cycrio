<script lang="ts">
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { onMount } from 'svelte';
  import { beforeNavigate } from '$app/navigation';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import type { TokenProvider } from './api.ts';
  import type { IntegrationAPI, EmailAttempt } from './integrations-api.ts';
  import {
    createIntegrationController,
    emptyIntegrationState,
    type IntegrationState,
  } from './integrations-controller.ts';
  let {
    api,
    token,
    onDenied,
  }: { api: IntegrationAPI; token: TokenProvider; onDenied: () => void } =
    $props();
  let view = $state<IntegrationState>(emptyIntegrationState());
  let confirmed = $state(false),
    editing = $state(false);
  let pane = $state<'settings' | 'webhooks' | 'test' | 'history'>('settings');
  let controller: ReturnType<typeof createIntegrationController> | undefined;
  const uncertain = $derived(
    !!view.pending && (!view.ack || view.ack.state === 'unknown'),
  );
  const tabs = [
    { id: 'settings', label: '送信設定' },
    { id: 'webhooks', label: 'Webhook' },
    { id: 'test', label: '本人へのテスト' },
    { id: 'history', label: '送信履歴' },
  ] as const;
  beforeNavigate((navigation) => {
    if (
      view.busy ||
      uncertain ||
      (view.dirty &&
        !window.confirm(
          '保存していない設定があります。このページを離れますか？',
        ))
    )
      navigation.cancel();
  });
  onMount(() => {
    controller = createIntegrationController(
      api,
      token,
      (next) => {
        view = next;
      },
      onDenied,
    );
    void controller.load();
    const leave = (event: BeforeUnloadEvent) => {
      if (view.busy || uncertain || view.dirty) {
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
  async function save() {
    await controller?.save();
    if (!controller?.state.error && !controller?.state.dirty) editing = false;
  }
  function show(value: typeof pane) {
    if (!view.busy) pane = value;
  }
  const status = (a: EmailAttempt) =>
    [
      a.state === 'accepted'
        ? '送信受付済み'
        : a.state === 'failed'
          ? '送信失敗'
          : '送信結果不明',
      ...(a.sent ? ['送信済み'] : []),
      ...(a.delivered ? ['受信サーバーに配信済み'] : []),
      ...(a.bounced ? ['配信拒否'] : []),
      ...(a.complained ? ['迷惑メール報告あり'] : []),
      ...(a.failed ? ['配信失敗'] : []),
    ].join('・');
  const errors = {
    invalid_request: '送信元メールと Clerk インスタンスIDを確認してください。',
    conflict:
      '設定が更新されたか、準備が整っていません。状態を再確認してください。編集中の設定は保持しています。',
    dependency_unavailable:
      '状態を確認できませんでした。送信要求は保持しています。新しい送信を作らず、同じ要求を再確認してください。',
    authentication_required: 'ログイン状態を確認しています。',
    access_denied: '管理者権限を確認しています。',
    not_found: '設定が見つかりません。',
  };
</script>

<section
  class="integration-settings library-page"
  aria-label="メール送信・外部イベント"
>
  <LibraryHeader
    title="メール送信・外部イベント"
    description="送信元と外部サービスの接続を管理します。"
  >
    {#snippet action()}
      <Button
        wrap
        disabled={view.busy || view.disposed}
        onclick={() => controller?.load()}>更新</Button
      >
    {/snippet}
  </LibraryHeader>
  <LibraryPanel>
    {#if !view.disposed}
      <nav class="library-tabs" aria-label="連携の設定項目">
        {#each tabs as tab}<Button
            wrap
            variant="tab"
            disabled={view.busy}
            aria-current={pane === tab.id ? 'page' : undefined}
            onclick={() => show(tab.id)}>{tab.label}</Button
          >{/each}
      </nav>
    {/if}
    <div class="library-content">
      <div aria-live="polite">
        {#if view.busy}<p role="status">状態を確認・保存しています…</p>{/if}
        {#if view.error}<p class="notice" role="alert">
            {errors[view.error]}
          </p>{/if}
        {#if uncertain && pane !== 'test'}<div class="notice" role="status">
            <p>
              テストメールの送信結果を確認してください。元の要求を保持しています。
            </p>
            <Button wrap disabled={view.busy} onclick={() => show('test')}
              >送信要求の確認へ</Button
            >
          </div>{/if}
      </div>
      {#if !view.disposed}
        <section>
          {#if pane === 'settings'}
            <div class="section-heading">
              <h2>送信設定</h2>
              {#if !editing}<Button
                  wrap
                  variant="primary"
                  class={['primary']}
                  disabled={view.busy || !view.view || !!view.pending}
                  onclick={() => (editing = true)}
                  >{view.version ? '送信設定を編集' : '送信設定を登録'}</Button
                >{/if}
            </div>
            {#if editing}
              <form
                onsubmit={(event) => {
                  event.preventDefault();
                  void save();
                }}
              >
                <label
                  >送信元メールアドレス<input
                    type="email"
                    autocomplete="off"
                    value={view.sender}
                    disabled={view.busy || !!view.pending}
                    oninput={(event) =>
                      controller?.edit({ sender: event.currentTarget.value })}
                    required
                  /></label
                >
                <p class="muted">Resendで確認済みのドメインを使います。</p>
                <label
                  >Clerk インスタンスID<input
                    autocomplete="off"
                    placeholder="ins_..."
                    value={view.instance}
                    disabled={view.busy || !!view.pending}
                    oninput={(event) =>
                      controller?.edit({ instance: event.currentTarget.value })}
                    required
                  /></label
                >
                <ActionGroup
                  class={['actions', 'part-IntegrationSettings-actions']}
                >
                  <Button
                    wrap
                    variant="primary"
                    class={['primary']}
                    disabled={view.busy || !view.view || !!view.pending}
                    >設定を保存</Button
                  ><Button
                    wrap
                    type="button"
                    disabled={view.busy}
                    onclick={() => (editing = false)}
                    >{view.dirty ? '入力を保持して閉じる' : '閉じる'}</Button
                  >
                </ActionGroup>
              </form>
            {:else}
              <dl>
                <dt>送信元</dt>
                <dd>{view.sender || '未設定'}</dd>
                <dt>Clerk インスタンス</dt>
                <dd>{view.instance || '未設定'}</dd>
                <dt>Resend API キー</dt>
                <dd>
                  {view.view
                    ? view.view.resend_ready
                      ? '登録済み'
                      : '未登録'
                    : '確認中'}
                </dd>
              </dl>
              {#if view.dirty}<p class="notice" role="status">
                  未保存の設定を保持しています。「送信設定を編集」から続けてください。
                </p>{/if}
              {#if view.view && !view.view.resend_ready}<a
                  href="/settings/credentials">Resend のAPIキーを登録</a
                >{/if}
            {/if}
            {#if view.view && view.dirty && view.view.config.version !== view.version}<div
                class="notice"
                role="status"
              >
                <p>
                  保存状態が更新されています。編集中の入力と元の版を保持しています。
                </p>
                <p>
                  現在の送信元：{view.view.config.sender}<br />Clerk
                  インスタンスID：{view.view.config.clerk_instance_id}
                </p>
                <Button
                  wrap
                  disabled={view.busy || !!view.pending}
                  onclick={() => controller?.adoptLatestBasis()}
                  >最新設定を基準にして編集を続ける</Button
                >
              </div>{/if}
          {:else if pane === 'webhooks'}
            <h2>Webhook の接続</h2>
            <p class="muted">
              ログインアカウントの停止・削除と、メールの配信結果を受け取ります。
            </p>
            <p>
              <a href="/settings/credentials">API キー</a>でClerk / Resend
              のWebhook署名鍵（<code>whsec_...</code>）を登録してください。
            </p>
            <p>
              外部サービスから到達できるAPIのURLに、次のパスを設定します。localhost
              は外部から到達できません。
            </p>
            <dl>
              <dt>
                Clerk · {view.view
                  ? view.view.clerk_webhook_ready
                    ? '署名鍵登録済み'
                    : '署名鍵未登録'
                  : '確認中'}
              </dt>
              <dd>
                <code>/v1/webhooks/clerk</code>
                <p class="muted">user.created / user.updated / user.deleted</p>
              </dd>
              <dt>
                Resend · {view.view
                  ? view.view.resend_webhook_ready
                    ? '署名鍵登録済み'
                    : '署名鍵未登録'
                  : '確認中'}
              </dt>
              <dd>
                <code>/v1/webhooks/resend</code>
                <p class="muted">
                  email.sent / email.delivered / email.bounced /
                  email.complained / email.failed / email.suppressed /
                  email.delivery_delayed
                </p>
              </dd>
            </dl>
          {:else if pane === 'test'}
            <h2>本人へテストメールを送信</h2>
            <p>
              ログイン中の管理者本人に固定メールを1通送ります。Resendの利用枠を消費します。宛先と本文は変更できません。
            </p>
            {#if view.view && !view.view.profile_ready}<div class="notice">
                <p>本人の確認済みメールが同期待ちです。</p>
                <p>
                  Clerkで本人のプロフィールを更新し、新しい user.updated
                  イベントを発生させてください。user.created または user.updated
                  の再配信も利用できます。インスタンスID変更後には新しいイベントが必要です。確認済みのメインメールアドレスだけを送信先に使います。
                </p>
              </div>{/if}
            {#if view.view?.profile_synced_at}<p class="muted">
                本人メールの最終同期：{new Date(
                  view.view.profile_synced_at,
                ).toLocaleString('ja-JP', {
                  timeZone: 'Asia/Tokyo',
                })}（日本時間）。Webhookの到着には遅延があります。
              </p>{/if}
            {#if view.dirty}<p class="notice">
                送信前に、編集中の設定を保存してください。
              </p>
              <Button
                wrap
                disabled={view.busy}
                onclick={() => {
                  editing = true;
                  show('settings');
                }}>送信設定に戻る</Button
              >{/if}
            <label class="confirmation"
              ><input
                type="checkbox"
                bind:checked={confirmed}
                disabled={view.busy}
              />本人へのメール送信と利用枠の消費を確認しました</label
            >
            <Button
              wrap
              variant="primary"
              class={['primary']}
              disabled={view.busy ||
                !confirmed ||
                !view.view ||
                (!view.pending &&
                  (view.dirty ||
                    !view.view.profile_ready ||
                    !view.view.resend_ready ||
                    view.version === 0))}
              onclick={() => controller?.send(confirmed)}
              >{view.pending
                ? '同じ送信要求の結果を再確認'
                : 'テストメールを1通送信'}</Button
            >
            {#if view.pending}<div class="notice">
                <p>結果の再確認ではメールを再送しません。</p>
                <details>
                  <summary>送信要求の詳細</summary><code
                    >{view.pending.request_id}</code
                  >
                </details>
                <Button
                  wrap
                  disabled={view.busy}
                  onclick={() => {
                    controller?.newTest();
                    confirmed = false;
                  }}>別のテストメールを送る準備をする</Button
                >
                <p>
                  別のテストは新たなメールを送ります。結果不明の場合も、先のメールが届く可能性があります。
                </p>
              </div>{/if}
            {#if view.ack}<p role="status">
                今回の結果：{status(view.ack)}
              </p>{/if}
          {:else}
            <h2>最近の送信</h2>
            {#if view.view?.attempts.length}<ul class="history">
                {#each view.view.attempts as attempt (attempt.id)}<li>
                    <time
                      >{new Date(attempt.created_at).toLocaleString('ja-JP', {
                        timeZone: 'Asia/Tokyo',
                      })}（日本時間）</time
                    ><strong>{status(attempt)}</strong>
                  </li>{/each}
              </ul>{:else}<p class="muted">送信記録はありません。</p>{/if}
          {/if}
        </section>
      {/if}
    </div>
  </LibraryPanel>
</section>

<style>
  .integration-settings {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .section-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
  }
  h2 {
    font-size: 1.1rem;
    margin: 0 0 1rem;
  }
  p {
    margin: 0.7rem 0;
  }
  .muted {
    color: var(--color-muted);
    font-size: 0.85rem;
  }
  .notice {
    padding: 1rem;
    background: var(--color-surface-soft);
    border-radius: 8px;
    margin: 1rem 0;
  }
  form {
    display: grid;
    gap: 12px;
    max-width: 560px;
  }
  label {
    display: grid;
    gap: 6px;
  }
  input {
    box-sizing: border-box;
    width: 100%;
    min-width: 0;
    padding: 10px;
    color: var(--color-text);
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: 6px;
  }
  :global(.part-IntegrationSettings-actions) {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .confirmation {
    display: flex;
    align-items: start;
    gap: 8px;
    margin: 1rem 0;
  }
  .confirmation input {
    width: auto;
    margin-top: 4px;
  }
  dt {
    color: var(--color-muted);
    font-size: 0.85rem;
  }
  dd {
    margin: 4px 0 16px;
  }
  .history {
    list-style: none;
    padding: 0;
  }
  .history li {
    display: grid;
    gap: 0.4rem;
    padding: 1rem 0;
    border-bottom: 1px solid var(--color-border);
  }
  .history time {
    color: var(--color-muted);
    font-size: 0.85rem;
  }
  @media (max-width: 600px) {
    .section-heading {
      flex-direction: column;
      gap: 0.5rem;
    }
  }
</style>
