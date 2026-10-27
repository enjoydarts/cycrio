<script lang="ts">
  import { onMount } from 'svelte';
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
  let confirmed = $state(false);
  let controller: ReturnType<typeof createIntegrationController> | undefined;
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
    return () => controller?.dispose();
  });
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

<section class="integration-settings" aria-label="メール送信・外部イベント">
  <h2>メール送信・外部イベント</h2>
  <p>
    管理者本人へのテストメールと、ログインアカウントの停止・削除、メールの配信結果を管理します。
  </p>
  <p>
    <a href="/settings/credentials">API キー</a>で Resend のAPIキー、Clerk /
    Resend
    のWebhook署名鍵を登録してください。署名鍵は各サービスのWebhook設定画面にある
    <code>whsec_...</code> です。
  </p>
  <form
    onsubmit={(event) => {
      event.preventDefault();
      void controller?.save();
    }}
  >
    <label
      >送信元メールアドレス
      <input
        type="email"
        autocomplete="off"
        value={view.sender}
        disabled={view.busy || !!view.pending}
        oninput={(event) =>
          controller?.edit({ sender: event.currentTarget.value })}
        required
      />
    </label>
    <p>Resendで確認済みのドメインを使います。</p>
    <label
      >Clerk インスタンスID
      <input
        autocomplete="off"
        placeholder="ins_..."
        value={view.instance}
        disabled={view.busy || !!view.pending}
        oninput={(event) =>
          controller?.edit({ instance: event.currentTarget.value })}
        required
      />
    </label>
    <button disabled={view.busy || !view.view || !!view.pending}
      >設定を保存</button
    >
  </form>
  {#if view.view && view.dirty && view.view.config.version !== view.version}
    <div role="status">
      <p>
        別の操作で設定が更新されています。現在の保存内容は次のとおりです。編集中の入力は保持しています。
      </p>
      <p>
        送信元：{view.view.config.sender}<br />Clerk インスタンスID：{view.view
          .config.clerk_instance_id}
      </p>
      <button
        disabled={view.busy || !!view.pending}
        onclick={() => controller?.adoptLatestBasis()}
        >最新設定を基準にして編集を続ける</button
      >
    </div>
  {/if}
  <h3>Webhook の接続</h3>
  <p>
    外部サービスから到達できるAPIのURLに、次のパスを設定します。ローカルの
    localhost は外部から到達できません。
  </p>
  <dl>
    <dt>Clerk</dt>
    <dd>
      <code>/v1/webhooks/clerk</code><br />対象：user.created / user.updated /
      user.deleted
    </dd>
    <dt>Resend</dt>
    <dd>
      <code>/v1/webhooks/resend</code><br />対象：email.sent / email.delivered /
      email.bounced / email.complained / email.failed / email.suppressed /
      email.delivery_delayed
    </dd>
  </dl>
  {#if view.view}
    <ul>
      <li>Resend API キー：{view.view.resend_ready ? '登録済み' : '未登録'}</li>
      <li>
        Clerk 署名鍵：{view.view.clerk_webhook_ready ? '登録済み' : '未登録'}
      </li>
      <li>
        Resend 署名鍵：{view.view.resend_webhook_ready ? '登録済み' : '未登録'}
      </li>
      <li>
        本人の確認済みメール：{view.view.profile_ready
          ? '同期済み'
          : '同期待ち'}
      </li>
    </ul>
    {#if !view.view.profile_ready}<p>
        Clerkで本人のプロフィールを更新し、新しい user.updated
        イベントを発生させてください。イベント履歴がある場合は user.created
        または user.updated を再配信できます。Clerk
        インスタンスIDを変更した後は新しいイベントが必要です。確認済みのメインメールアドレスだけを送信先に使います。
      </p>{/if}
    {#if view.view.profile_synced_at}<p>
        最終同期：{new Date(view.view.profile_synced_at).toLocaleString(
          'ja-JP',
          { timeZone: 'Asia/Tokyo' },
        )}（日本時間）。Webhookの到着には遅延があります。
      </p>{/if}
  {/if}
  <button onclick={() => controller?.load()} disabled={view.busy}
    >状態を再確認</button
  >
  <h3>本人へテストメールを送信</h3>
  <p>
    ログイン中の管理者本人に固定メールを1通送ります。Resendの利用枠を消費します。宛先と本文は変更できません。
  </p>
  <label class="confirmation"
    ><input
      type="checkbox"
      bind:checked={confirmed}
      disabled={view.busy}
    />本人へのメール送信と利用枠の消費を確認しました</label
  >
  <button
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
      : 'テストメールを1通送信'}</button
  >
  {#if view.pending}
    <p>
      要求ID：<code>{view.pending.request_id}</code
      >。結果の再確認ではメールを再送しません。
    </p>
    <button
      disabled={view.busy}
      onclick={() => {
        controller?.newTest();
        confirmed = false;
      }}>別のテストメールを送る準備をする</button
    >
    <p>
      別のテストは新たなメールを送ります。結果不明の場合も、先のメールが届く可能性があります。
    </p>
  {/if}
  <div aria-live="polite">
    {#if view.error}<p role="alert">{errors[view.error]}</p>{/if}
    {#if view.ack}<p>今回の結果：{status(view.ack)}</p>{/if}
  </div>
  <h3>最近の送信</h3>
  {#if view.view?.attempts.length}
    <ul>
      {#each view.view.attempts as attempt (attempt.id)}<li>
          {new Date(attempt.created_at).toLocaleString('ja-JP', {
            timeZone: 'Asia/Tokyo',
          })}（日本時間） — {status(attempt)}
        </li>{/each}
    </ul>
  {:else}<p>送信記録はありません。</p>{/if}
</section>

<style>
  .integration-settings {
    min-width: 0;
    overflow-wrap: anywhere;
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
  button {
    padding: 10px 14px;
    margin: 8px 0;
    max-width: 100%;
    white-space: normal;
  }
  .confirmation {
    display: flex;
    align-items: start;
    gap: 8px;
  }
  .confirmation input {
    width: auto;
    margin-top: 4px;
  }
  dd {
    margin: 4px 0 16px;
  }
</style>
