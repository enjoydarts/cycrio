<script lang="ts">
  import { onMount } from 'svelte';
  import { getAppSession } from './app-context.ts';
  import type { MailAccount } from './mail-api.ts';
  import type { AnalysisResult } from './analysis-api.ts';
  import {
    createMailProcessingController,
    emptyProcessingView,
    validProcessingMaximum,
    type ProcessingRequests,
  } from './mail-processing-controller.ts';
  import {
    processingBlocked,
    processingErrors,
  } from './mail-processing-text.ts';
  import MailProcessingModels from './MailProcessingModels.svelte';
  let {
    accounts,
    active = true,
    retry = null,
    requests,
    onAccepted,
    onBusyChange = () => {},
  }: {
    accounts: MailAccount[];
    active?: boolean;
    retry?: AnalysisResult | null;
    requests: ProcessingRequests;
    onAccepted: () => void;
    onBusyChange?: (busy: boolean) => void;
  } = $props();
  const session = getAppSession(),
    token = session.token;
  let view = $state(emptyProcessingView()),
    controller = $state<ReturnType<typeof createMailProcessingController>>();
  let selected = $state(''),
    unknownConfirmed = $state(false),
    reviewing = $state(false),
    selectionTouched = $state(false);
  const busy = $derived(
    reviewing || view.status === 'loading' || view.status === 'saving',
  );
  const canSend = $derived(
    !!selected &&
      view.account === selected &&
      view.preview?.account_id === selected &&
      !busy &&
      !view.uncertain &&
      !view.conflict &&
      !!view.preview?.ready,
  );
  const scope = $derived(JSON.stringify(view.form.basis));
  $effect(() => {
    void scope;
    unknownConfirmed = false;
  });
  $effect(() => {
    if (active && controller) {
      controller.setAccount(selected);
      if (selected) void controller.refresh();
    }
  });
  $effect(() => {
    if (active && !selectionTouched && !selected && accounts.length === 1)
      selected = accounts[0].id;
  });
  onMount(() => {
    controller = createMailProcessingController(
      session.mailProcessingAPI,
      token,
      (next) => {
        view = next;
        onBusyChange(next.status === 'saving');
      },
      session.onDenied,
      () => crypto.randomUUID(),
      requests,
      true,
    );
    selected =
      retry?.account_id ?? (accounts.length === 1 ? accounts[0].id : '');
    if (active && selected) {
      controller.setAccount(selected);
      void controller.refresh();
    }
    return () => {
      controller?.dispose();
      onBusyChange(false);
    };
  });
  async function submit(replay = false) {
    if (!selected || view.account !== selected || (!replay && !canSend)) return;
    if (replay) await controller?.replay();
    else if (retry)
      await controller?.retrySelected(
        retry,
        view.form.confirmed,
        unknownConfirmed,
      );
    else await controller?.start();
    if (
      controller?.state().status === 'ready' &&
      controller.state().runs.length &&
      !controller.state().uncertain
    )
      onAccepted();
  }
</script>

<div class="daily">
  <p>
    同期済みメールの本文を取得して外部 AI
    へ送信します。開始する前に、モデル・送信条件・参考料金を確認してください。
  </p>
  {#if !accounts.length && !retry}<div class="empty">
      <p>メールアカウントがありません。接続と同期を行ってから解析できます。</p>
      <a class="button" href="/settings/mail">メールを連携する</a>
    </div>{:else}
    <label class="section"
      >メールアカウント<select
        aria-label="メールアカウント"
        disabled={view.status === 'saving' || view.uncertain || !!retry}
        value={selected}
        onchange={(e) => {
          selectionTouched = true;
          selected = e.currentTarget.value;
        }}
        ><option value="">選択してください</option
        >{#each accounts as account}<option value={account.id}
            >{account.label}</option
          >{/each}{#if retry && !accounts.some((a) => a.id === retry.account_id)}<option
            value={retry.account_id}>{retry.account_label}</option
          >{/if}</select
      ></label
    >
    {#if view.preview}{@const p = view.preview}
      <p class="notice">
        未解析 {p.pending_count} 件 · 失敗 {p.failed_count} 件 · 結果不明 {p.unknown_count}
        件
      </p>
      <MailProcessingModels
        primary={p.primary}
        secondary={p.secondary}
        max_input_bytes={p.max_input_bytes}
        max_output_tokens={p.max_output_tokens}
        timeout_seconds={p.timeout_seconds}
      />
      {#if p.blocked_reason}<div class="notice">
          <p>{processingBlocked[p.blocked_reason]}</p>
          <a href="/settings/mail">メール連携を確認</a
          >{#if session.user?.role === 'platform_admin'}
            · <a href="/settings/ai-runtime">AI 設定</a> ·
            <a href="/settings/mail-policy">送信条件</a>{/if}
        </div>{/if}
    {/if}
    {#if view.status === 'loading'}<p role="status" class="notice">
        現在のモデルと送信条件を確認しています…
      </p>{/if}
    {#if view.conflict}<p class="notice" role="alert">
        条件が変更されました。入力と元のバージョンを保持しています。最新条件を読み込み、入力を破棄して基準を更新し、同意し直してください。
      </p>{/if}
    <form
      onsubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      {#if retry}<p class="notice">
          このメールだけを現在の条件で再解析します。別の操作で結果が更新されている場合は、最新の結果を確認してからやり直してください。
        </p>{:else}<label class="section"
          >今回解析する上限（1〜100件）<input
            id="analysis-max"
            type="text"
            inputmode="numeric"
            placeholder="件数を入力"
            value={view.form.max_messages}
            disabled={busy || view.uncertain}
            oninput={(e) =>
              controller?.edit({ max_messages: e.currentTarget.value })}
          /></label
        >{/if}
      <label class="check"
        ><input
          id="analysis-consent"
          type="checkbox"
          checked={view.form.confirmed}
          disabled={!canSend}
          onchange={(e) =>
            controller?.edit({ confirmed: e.currentTarget.checked })}
        />上記のモデル・配信先・保持条件・入力と出力の上限を確認し、メール本文の外部送信と料金の発生に同意します</label
      >
      {#if retry?.state === 'unknown'}<label class="check"
          ><input
            type="checkbox"
            checked={unknownConfirmed}
            disabled={!canSend}
            onchange={(e) => (unknownConfirmed = e.currentTarget.checked)}
          />前回の処理で料金が発生している可能性と、再解析で料金が重複して発生する可能性を確認しました</label
        >{/if}
      <p class="muted">
        一次判定の後、必要なメールを二次抽出へ送信します。停止しても送信済みの処理には料金が発生する場合があります。
      </p>
      <div class="actions">
        <button
          class="primary"
          type="submit"
          disabled={!canSend ||
            !view.form.confirmed ||
            (retry
              ? retry.state === 'unknown' && !unknownConfirmed
              : !validProcessingMaximum(view.form.max_messages) ||
                !view.preview?.pending_count)}
          >{retry
            ? '確認した条件で1件を再解析'
            : '確認した条件で解析を開始'}</button
        ><button
          type="button"
          disabled={busy || view.uncertain || !selected}
          onclick={async () => {
            unknownConfirmed = false;
            reviewing = true;
            try {
              await controller?.refresh();
              if (controller?.state().status === 'ready') controller.discard();
            } finally {
              reviewing = false;
            }
          }}>入力を破棄して最新条件を確認</button
        ><button
          type="button"
          disabled={busy || !selected}
          onclick={() => controller?.refresh()}>条件を更新</button
        >
      </div>
    </form>
    {#if view.error}<p role="alert" class="notice error">
        {processingErrors[view.error]}
      </p>{/if}
    {#if view.uncertain}<div class="notice">
        <p>
          受付状況を確認できません。解析条件と要求 ID
          を保持しています。新しい解析要求を作らず同じ要求で確認できます。
        </p>
        <button disabled={busy} onclick={() => submit(true)}
          >同じ解析要求を再送</button
        >
      </div>{/if}
  {/if}
</div>
