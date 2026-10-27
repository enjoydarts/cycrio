<script lang="ts">
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import type { MailAccount } from './mail-api.ts';
  import {
    createMailAutomationAPI,
    validAutomationRange,
  } from './mail-automation-api.ts';
  import {
    createMailAutomationController,
    emptyAutomationView,
  } from './mail-automation-controller.ts';
  import { processingDate, processingErrors } from './mail-processing-text.ts';
  import { automationSetupLink } from './setup-readiness.ts';
  import MailProcessingModels from './MailProcessingModels.svelte';
  let {
    account,
    returnTo = '/analysis',
    onRefresh = () => {},
    onFrozen = () => {},
  }: {
    account: MailAccount | null;
    returnTo?: string;
    onRefresh?: () => void;
    onFrozen?: (frozen: boolean) => void;
  } = $props();
  const session = getAppSession(),
    api = createMailAutomationAPI(PUBLIC_API_BASE_URL);
  let view = $state(emptyAutomationView()),
    controller = $state<ReturnType<typeof createMailAutomationController>>(),
    reviewing = $state(false);
  const s = $derived(view.snapshot),
    draft = $derived(view.draft),
    frozen = $derived(view.busy || !!view.pending),
    needsReview = $derived(
      s?.state === 'not_configured' ||
        s?.state === 'needs_confirmation' ||
        reviewing,
    );
  const reasons = $derived(s?.preparation_blocked_reasons ?? []);
  const blockedLabels: Record<string, string> = {
    account_not_ready: 'メールの接続確認が必要です',
    folders_required: '対象フォルダーを選択してください',
    credentials_required: 'メール認証情報を更新してください',
    runtime_inactive: 'AI の準備が必要です',
    policy_disabled: '本文送信の許可が必要です',
    full_email_not_allowed: '本文全体を送信する許可が必要です',
    policy_reconfirmation_required: '最新の送信条件を確認する必要があります',
    configuration_unavailable: 'AI の設定を確認できません',
    account_changed: 'メール接続条件が変わりました',
    configuration_changed: 'AI の送信条件が変わりました',
    uid_validity_changed: 'メールボックスの識別情報が変わりました',
    authentication_failed: 'メール接続の認証が変わりました',
    credentials_unavailable: 'メールの認証情報を利用できません',
    connection_failed: 'メールサーバーに接続できませんでした',
    timeout: 'メールサーバーへの接続が時間切れになりました',
  };
  const blockedAction = $derived.by(() => {
    if (
      reasons.some((r) =>
        [
          'account_not_ready',
          'folders_required',
          'credentials_required',
        ].includes(r),
      )
    )
      return {
        href: automationSetupLink(
          '/settings/mail',
          returnTo,
          account?.id ?? '',
        ),
        label: 'メール連携を確認',
      };
    if (reasons.length) {
      const permission = reasons.some((r) =>
        [
          'policy_disabled',
          'full_email_not_allowed',
          'policy_reconfirmation_required',
        ].includes(r),
      );
      return session.user?.role === 'platform_admin'
        ? {
            href: automationSetupLink(
              permission ? '/settings/ai?stage=permission' : '/settings/ai',
              returnTo,
              account?.id ?? '',
            ),
            label: permission
              ? '本文全体の送信条件を確認して許可'
              : 'AI の準備を進める',
          }
        : {
            href: null,
            label: '管理者による AI の準備・本文全体の送信許可をお待ちください',
          };
    }
    return null;
  });
  $effect(() => {
    if (controller && account?.id && view.account !== account.id && !frozen) {
      reviewing = false;
      void controller.select(account.id);
    }
  });
  $effect(() => {
    onFrozen(frozen);
  });
  onMount(() => {
    controller = createMailAutomationController(
      api,
      session.mailProcessingAPI,
      session.token,
      (next) => {
        view = next;
      },
      session.onDenied,
    );
    if (account) void controller.select(account.id);
    const poll = () => {
      if (document.visibilityState !== 'hidden' && !view.denied) {
        void controller?.refresh();
        onRefresh();
      }
    };
    const timer = setInterval(poll, 5000);
    const visibility = () => {
      if (document.visibilityState === 'hidden') controller?.suspend();
      else poll();
    };
    document.addEventListener('visibilitychange', visibility);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', visibility);
      controller?.dispose();
      onFrozen(false);
    };
  });
  async function start() {
    await controller?.start();
    if (controller?.state().acknowledged === 'start') {
      reviewing = false;
      onRefresh();
    }
  }
</script>

<section class="automation" aria-label="メールの自動処理">
  {#if !account}<h2>メールを連携して始める</h2>
    <p>初回は過去1年分を取り込み、その後は新着メールを自動で確認します。</p>
    <a
      class="button primary"
      href={automationSetupLink('/settings/mail', returnTo, '')}
      >メールを連携する</a
    >
  {:else}
    <header>
      <div>
        <h2>メールの自動処理</h2>
        <p>{account.label} · 新着を5分ごとに確認</p>
      </div>
      {#if s?.id && (s.state === 'enabled' || s.state === 'needs_confirmation')}<button
          disabled={frozen}
          onclick={() => controller?.stop()}>自動処理を停止</button
        >
      {:else if s?.state === 'stopped' && s.ready}<button
          class="primary"
          disabled={frozen || !!view.readError}
          onclick={() => controller?.resume()}>自動処理を再開</button
        >{/if}
    </header>
    {#if s}
      <p class="status" role="status">
        {s.state === 'enabled'
          ? s.retry_at
            ? '接続を自動で再試行します'
            : '自動処理中'
          : s.state === 'stopped'
            ? '停止中'
            : s.state === 'needs_confirmation'
              ? '条件の再確認が必要です'
              : '初回の期間と送信条件を確認してください'}
      </p>
      {#if s.state === 'enabled' && s.retry_at}<p>
          メールの確認を一時的に待機しています。次の再試行：{processingDate(
            s.retry_at,
          )}（日本時間）
        </p>{/if}
      {#if s.state === 'needs_confirmation'}<p>
          接続や送信条件が変わったため自動処理を止めています。{blockedLabels[
            s.reason
          ] ??
            '新しい条件を確認してください'}。確認して開始するまで自動再開しません。
        </p>{/if}
      {#if s.id}<dl class="progress">
          <div>
            <dt>発見</dt>
            <dd>{s.progress.discovered_count} 件</dd>
          </div>
          <div>
            <dt>解析待ち</dt>
            <dd>{s.progress.pending_count} 件</dd>
          </div>
          <div>
            <dt>解析済み</dt>
            <dd>{s.progress.completed_count} 件</dd>
          </div>
          <div>
            <dt>解析失敗</dt>
            <dd>{s.progress.failed_count} 件</dd>
          </div>
          <div>
            <dt>結果不明</dt>
            <dd>{s.progress.unknown_count} 件</dd>
          </div>
        </dl>
        <p class="muted">
          {s.progress.historical_complete
            ? '初回期間のメール探索は完了しました。解析・登録は順次進みます。'
            : '初回期間のメールを順次探しています。探索が終わるまで総件数は確定しません。'}
        </p>
        <p class="muted">
          最終メール確認：{s.last_success_at
            ? processingDate(s.last_success_at)
            : 'まだありません'}（日本時間）
        </p>
        {#if s.state === 'enabled'}<p class="muted">
            アプリを閉じても取り込み・解析・登録は続きます。
          </p>{:else if s.state === 'stopped'}<p class="muted">
            この接続の取り込み・解析は停止中です。{blockedLabels[s.reason] ??
              ''} 再開すると進捗を引き継ぎます。
          </p>{/if}
      {/if}
    {/if}
    {#if view.loading}<p role="status">自動処理の状態を確認しています…</p>{/if}
    {#if view.readError}<div class="notice" role="alert">
        <p>
          {view.acknowledged
            ? '操作は受け付けられました。最新状態を再取得できませんでした。'
            : '最新状態を取得できませんでした。'}表示済みの進捗と入力を保持しています。
        </p>
        <button disabled={view.busy} onclick={() => controller?.refresh()}
          >最新状態を再読み込み</button
        >
      </div>{/if}
    {#if view.error && !view.uncertain}<p role="alert">
        {processingErrors[view.error]}
      </p>{/if}
    {#if view.uncertain}<div class="notice" role="status">
        <p>
          受付状況を確認できません。同じ条件・同じ要求で再確認してください。確認が終わるまで条件の変更と別操作を停止しています。
        </p>
        <button disabled={view.busy} onclick={() => controller?.replay()}
          >同じ要求で受付を確認</button
        >
      </div>{/if}
    {#if view.acknowledged && !view.readError}<p role="status">
        {view.acknowledged === 'start'
          ? '自動処理を開始しました。'
          : view.acknowledged === 'stop'
            ? '停止を受け付けました。'
            : '自動処理を再開しました。'}
      </p>{/if}
    {#if needsReview && s}
      {#if reasons.length}<div class="notice" role="status">
          <p>
            {reasons
              .map((r) => blockedLabels[r] ?? '設定の確認が必要です')
              .join('。')}。
          </p>
          {#if blockedAction?.href}<a href={blockedAction.href}
              >{blockedAction.label}</a
            >{:else if blockedAction}<p>{blockedAction.label}</p>{/if}
        </div>{/if}
      {#if draft}<form
          onsubmit={(e) => {
            e.preventDefault();
            void start();
          }}
        >
          <h3>
            {s.state === 'not_configured'
              ? '初回に取り込む期間'
              : '今回確認する取り込み期間'}（日本時間）
          </h3>
          <div class="range">
            <label
              >開始日<input
                type="date"
                value={draft.since}
                disabled={frozen}
                oninput={(e) =>
                  controller?.edit({ since: e.currentTarget.value })}
              /></label
            ><label
              >終了日（当日を含む）<input
                type="date"
                value={draft.through}
                disabled={frozen}
                oninput={(e) =>
                  controller?.edit({ through: e.currentTarget.value })}
              /></label
            >
          </div>
          <p>
            対象フォルダー：{account.folders
              .filter((f) => f.selected && f.available)
              .map((f) => f.name)
              .join('、') || '未選択'}
          </p>
          <p>
            選択した期間の全件を順次取り込み、本文全体を AI
            に送信します。新着メールも継続して送信し、サブスクと支払・請求履歴を自動で登録します。
          </p>
          <details>
            <summary>モデル・配信先・保持条件・参考料金を確認</summary
            ><MailProcessingModels
              primary={draft.preview.primary}
              secondary={draft.preview.secondary}
              max_input_bytes={draft.preview.max_input_bytes}
              max_output_tokens={draft.preview.max_output_tokens}
              timeout_seconds={draft.preview.timeout_seconds}
            />
          </details>
          <p>
            自動登録は、あなたのメール接続に共通の設定です。この接続に加え、他の接続を含む保存済みの解析結果も登録対象になります。{view
              .preferences?.enabled
              ? '現在、自動登録は有効です。'
              : '開始時に自動登録を有効にします。'}
          </p>
          {#if view.conflict}<div class="notice" role="alert">
              <p>
                条件が変わりました。入力と確認開始時の版を保持しています。最新の条件を採用してから確認し直してください。
              </p>
              <button
                type="button"
                disabled={frozen}
                onclick={() => controller?.adopt()}
                >最新の条件を採用して確認し直す</button
              >
            </div>{/if}
          <label class="check"
            ><input
              type="checkbox"
              checked={view.confirmed}
              disabled={frozen ||
                view.conflict ||
                !s.preparation_ready ||
                !view.preview?.full_email_allowed ||
                !!view.readError}
              onchange={(e) => controller?.confirm(e.currentTarget.checked)}
            />期間・本文全体の継続送信・保持条件・参考料金と自動登録の対象を確認しました</label
          >
          <button
            class="primary"
            type="submit"
            disabled={frozen ||
              !view.confirmed ||
              view.conflict ||
              !s.preparation_ready ||
              !view.preview?.full_email_allowed ||
              !!view.readError ||
              !validAutomationRange(draft.since, draft.through)}
            >{view.busy ? '受付を確認中…' : '自動処理を開始'}</button
          >
        </form>{/if}
    {:else if s?.id}<details>
        <summary>取り込み条件</summary>
        <p>
          {s.scope?.since} から {s.scope?.before} より前（日本時間） · 本文全体 ·
          新着確認5分ごと
        </p>
        <p>
          保存済み結果の自動登録（あなたの接続に共通）：{view.preferences
            ?.enabled
            ? '有効'
            : view.preferences
              ? '停止中'
              : '確認できません'}
        </p>
        <button
          disabled={frozen}
          onclick={() => {
            reviewing = true;
            controller?.adopt();
          }}>条件を見直す</button
        >
      </details>{/if}
  {/if}
</section>

<style>
  .automation {
    border: 1px solid var(--color-border);
    border-radius: 0.7rem;
    padding: 1.25rem;
    margin: 1rem 0 1.75rem;
    overflow-wrap: anywhere;
    min-width: 0;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    flex-wrap: wrap;
  }
  header > div {
    flex: 1 1 20rem;
    min-width: 0;
  }
  h2 {
    font-size: 1.15rem;
    margin: 0;
  }
  h3 {
    font-size: 1rem;
  }
  p {
    line-height: 1.8;
    margin: 0.55rem 0;
  }
  .status {
    font-weight: 600;
  }
  .progress {
    display: flex;
    flex-wrap: wrap;
    gap: 1rem 2rem;
    margin: 1rem 0;
  }
  .progress div {
    min-width: 5rem;
  }
  dt {
    color: var(--color-muted);
    font-size: 0.8rem;
  }
  dd {
    margin: 0.3rem 0;
    font-weight: 600;
  }
  .range {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1rem;
  }
  .range label {
    display: grid;
    gap: 0.4rem;
    min-width: 0;
  }
  input[type='date'] {
    min-width: 0;
    width: 100%;
    box-sizing: border-box;
  }
  details {
    margin: 0.75rem 0;
  }
  summary {
    cursor: pointer;
  }
  .check {
    margin: 1rem 0;
  }
  .notice {
    margin: 1rem 0;
  }
  .muted {
    color: var(--color-muted);
    font-size: 0.85rem;
  }
  @media (max-width: 550px) {
    .range {
      grid-template-columns: minmax(0, 1fr);
    }
    .automation {
      padding: 1rem;
    }
  }
</style>
