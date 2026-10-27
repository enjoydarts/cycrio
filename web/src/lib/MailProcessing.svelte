<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider, Role } from './api.ts';
  import type { MailAccount } from './mail-api.ts';
  import type {
    MailProcessingAPI,
    ProcessingRun,
  } from './mail-processing-api.ts';
  import {
    createMailProcessingController,
    emptyProcessingView,
    processingActive,
    validProcessingMaximum,
    type ProcessingRequests,
  } from './mail-processing-controller.ts';
  import {
    processingBlocked,
    processingDate,
    processingErrors,
  } from './mail-processing-text.ts';
  import MailProcessingModels from './MailProcessingModels.svelte';
  import MailProcessingResults from './MailProcessingResults.svelte';
  import ProcessingReadiness from './ProcessingReadiness.svelte';
  let {
    account,
    api,
    token,
    onDenied,
    requests = new Map(),
    role = 'user',
    returnTo = '/settings/mail',
  }: {
    account: MailAccount;
    api: MailProcessingAPI;
    token: TokenProvider;
    onDenied: () => void;
    requests?: ProcessingRequests;
    role?: Role;
    returnTo?: string;
  } = $props();
  let view = $state(emptyProcessingView());
  let controller = $state<ReturnType<typeof createMailProcessingController>>();
  const busy = $derived(view.status === 'saving' || view.status === 'loading');
  const canSend = $derived(
    !busy &&
      !view.uncertain &&
      !view.conflict &&
      !!view.preview?.ready &&
      !view.runs.some(processingActive),
  );
  const confirmationScope = $derived(
    JSON.stringify([
      view.preview?.expected_version,
      view.preview?.runtime_version,
      view.preview?.profile_id,
      view.preview?.policy_version,
    ]),
  );
  const states: Record<ProcessingRun['state'], string> = {
    queued: '解析待ち',
    leased: '解析の準備中',
    running: '解析中',
    completed: '解析完了',
    failed: '解析の実行に失敗',
    cancelled: '解析を停止済み',
  };
  onMount(() => {
    controller = createMailProcessingController(
      api,
      token,
      (next) => {
        view = next;
      },
      onDenied,
      () => crypto.randomUUID(),
      requests,
    );
    controller.setAccount(account.id);
    void controller.refresh();
    return () => controller?.dispose();
  });
  $effect(() => {
    const version = account.version;
    void version;
    controller?.setAccount(account.id);
    void controller?.refresh();
  });
  $effect(() => {
    if (
      !controller ||
      busy ||
      view.status === 'denied' ||
      view.status === 'disposed' ||
      !(view.uncertain || view.runs.some(processingActive))
    )
      return;
    const timer = setTimeout(() => {
      void controller?.poll();
    }, 5000);
    return () => clearTimeout(timer);
  });
</script>

<section class="processing" aria-label="メール本文の手動解析">
  <div class="heading">
    <div>
      <p class="eyebrow">MANUAL MAIL ANALYSIS</p>
      <h4>メール本文の手動解析</h4>
    </div>
    <button
      id="processing-refresh"
      type="button"
      disabled={busy}
      onclick={() => controller?.refresh()}>解析状況を更新</button
    >
  </div>
  <p class="help">
    同期済みのメールIDから、未解析のメール本文を取得して外部 AI
    へ送信します。保存や読み込みでは解析を開始しません。失敗・結果不明のメールは個別に再解析してください。
  </p>
  {#if view.preview}
    <div class="target">
      <strong>未解析 {view.preview.pending_count} 件</strong><span
        >処理失敗 {view.preview.failed_count} 件 / 結果不明 {view.preview
          .unknown_count} 件</span
      >
      <p>
        保存済みの対象フォルダーで取得可能なメールが対象です。実行時に取得できないメールは失敗として記録します。
      </p>
    </div>
    <MailProcessingModels
      primary={view.preview.primary}
      secondary={view.preview.secondary}
      max_input_bytes={view.preview.max_input_bytes}
      max_output_tokens={view.preview.max_output_tokens}
      timeout_seconds={view.preview.timeout_seconds}
    />
  {:else}<p class="help">
      現在のモデル・配信先・保持条件を読み込んでから開始できます。
    </p>{/if}
  {#if view.conflict}<p class="feedback" role="alert">
      入力と元の設定バージョン・モデル・送信条件の基準を保持しています。最新の条件を確認し、「入力を破棄して最新条件を確認」で基準を更新してから、料金と外部送信を再確認してください。
    </p>{/if}
  <form
    onsubmit={(event) => {
      event.preventDefault();
      void controller?.start();
    }}
  >
    <label class="maximum" for="processing-max"
      >今回解析する上限（1〜100件）<input
        id="processing-max"
        type="number"
        min="1"
        max="100"
        step="1"
        required
        placeholder="件数を入力"
        value={view.form.max_messages}
        disabled={view.status === 'saving' || view.uncertain}
        oninput={(event) =>
          controller?.edit({ max_messages: event.currentTarget.value })}
      /></label
    >
    <ProcessingReadiness
      reason={view.preview?.blocked_reason ?? ''}
      {role}
      {returnTo}
      account={account.id}
      maximum={view.form.max_messages}
      {busy}
      uncertain={view.uncertain}
      conflict={view.conflict}
    />
    <label class="confirm"
      ><input
        id="processing-consent"
        type="checkbox"
        checked={view.form.confirmed}
        disabled={!canSend}
        onchange={(event) =>
          controller?.edit({ confirmed: event.currentTarget.checked })}
      />上記のモデル・配信先・保持条件・入力と出力の上限を確認し、メール本文の外部送信と料金の発生に同意します</label
    >
    <p class="help">
      一次判定で関連性を確認し、必要なメールを二次抽出へ送信します。停止時も送信済みの処理には料金が発生する場合があります。
    </p>
    <div class="actions">
      <button
        id="processing-start"
        class="primary"
        type="submit"
        disabled={!canSend ||
          !validProcessingMaximum(view.form.max_messages) ||
          !view.form.confirmed ||
          !view.preview?.pending_count}>確認した条件で解析を開始</button
      ><button
        id="processing-discard"
        type="button"
        disabled={view.status === 'saving' || view.uncertain}
        onclick={() => controller?.discard()}
        >入力を破棄して最新条件を確認</button
      >
    </div>
  </form>
  <div aria-live="polite" aria-atomic="true">
    {#if view.status === 'loading'}<p class="feedback">
        モデル・送信条件・履歴・結果を確認しています。
      </p>{:else if view.status === 'saving'}<p class="feedback">
        操作を送信しています。
      </p>{/if}
    {#if view.error}<p class="feedback" role="alert">
        {processingErrors[view.error]}
      </p>{/if}
    {#if view.uncertain}<p class="feedback">
        要求が受け付けられたか確認できません。送信内容と要求IDを保持しています。新しい要求を作らず、同じ要求の再送で受付状況を確認できます。
      </p>
      <button
        id="processing-replay"
        type="button"
        disabled={busy}
        onclick={() => controller?.replay()}>同じ解析要求を再送</button
      >{/if}
  </div>
  <div class="heading history">
    <h5>解析履歴</h5>
    <span>最新20件 · 日本時間 · 進行中は5秒ごとに更新</span>
  </div>
  <ol class="runs">
    {#each view.runs as run (run.id)}<li>
        <div class="heading">
          <strong>{states[run.state]}</strong><time datetime={run.created_at}
            >{processingDate(run.created_at)}</time
          >
        </div>
        <p>
          完了 {run.completed_count} / 対象 {run.total_count} 件 · 指定上限 {run.max_messages}
          件
        </p>
        <dl>
          <div>
            <dt>関連あり</dt>
            <dd>{run.related_count}</dd>
          </div>
          <div>
            <dt>非関連</dt>
            <dd>{run.unrelated_count}</dd>
          </div>
          <div>
            <dt>失敗</dt>
            <dd>{run.failed_count}</dd>
          </div>
          <div>
            <dt>結果不明</dt>
            <dd>{run.unknown_count}</dd>
          </div>
        </dl>
        {#if processingActive(run)}<button
            type="button"
            disabled={view.status === 'saving'}
            onclick={() => controller?.cancel(run.id)}>解析を停止</button
          >{/if}
      </li>{:else}<li class="help">
        解析履歴はありません。条件を確認して明示的に開始してください。
      </li>{/each}
  </ol>
  <MailProcessingResults
    results={view.results}
    canRetry={canSend}
    {confirmationScope}
    onRetry={(id, charge, unknown) => {
      void controller?.retryResult(id, charge, unknown);
    }}
  />
</section>

<style>
  .processing {
    min-width: 0;
    margin-top: 1.75rem;
    padding-top: 1.5rem;
    border-top: 1px solid var(--color-border);
  }
  h4,
  h5,
  p,
  dd,
  dl {
    margin: 0;
  }
  h4 {
    font-size: 1rem;
  }
  h5 {
    font-size: 0.9rem;
  }
  .heading,
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.8rem;
  }
  .eyebrow {
    color: var(--color-action);
    font-size: 0.62rem;
    letter-spacing: 0.12em;
    margin-bottom: 0.4rem;
  }
  .help {
    color: var(--color-muted);
    font-size: 0.78rem;
    line-height: 1.9;
    margin: 0.7rem 0;
    overflow-wrap: anywhere;
  }
  .target {
    padding: 1rem;
    border-radius: 0.5rem;
    background: var(--color-surface-soft);
    font-size: 0.85rem;
    margin: 1rem 0;
    line-height: 1.9;
  }
  .target span {
    display: block;
  }
  .target p {
    font-size: 0.75rem;
    color: var(--color-muted);
  }
  .maximum {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    font-size: 0.8rem;
    max-width: 22rem;
    margin: 1.2rem 0;
  }
  input[type='number'] {
    box-sizing: border-box;
    min-width: 0;
    width: 100%;
    padding: 0.7rem;
    border: 1px solid var(--color-border);
    border-radius: 0.45rem;
    background: var(--color-surface);
    color: var(--color-text);
    font: inherit;
    font-size: 1rem;
  }
  .confirm {
    display: flex;
    align-items: flex-start;
    gap: 0.6rem;
    font-size: 0.8rem;
    line-height: 1.9;
  }
  input[type='checkbox'] {
    width: 1rem;
    height: 1rem;
    flex: none;
    margin-top: 0.25rem;
    accent-color: var(--color-action);
  }
  button {
    min-height: 2.8rem;
    max-width: 100%;
    padding: 0.6rem 0.9rem;
    border: 1px solid var(--color-action);
    border-radius: 0.5rem;
    background: var(--color-surface);
    color: var(--color-action);
    font: inherit;
    font-size: 0.8rem;
    line-height: 1.6;
    cursor: pointer;
  }
  button:disabled {
    opacity: 0.55;
    cursor: default;
  }
  .primary {
    background: var(--color-action);
    color: var(--color-surface);
  }
  button:focus-visible,
  input:focus-visible {
    outline: 2px solid var(--color-action);
    outline-offset: 2px;
  }
  .actions {
    justify-content: flex-start;
    margin: 1rem 0;
  }
  .feedback {
    margin: 0.8rem 0;
    padding: 0.8rem 1rem;
    border-left: 3px solid var(--color-action);
    background: var(--color-surface-soft);
    font-size: 0.8rem;
    line-height: 1.9;
    overflow-wrap: anywhere;
  }
  .history {
    margin-top: 1.6rem;
  }
  .heading span,
  time {
    color: var(--color-muted);
    font-size: 0.7rem;
  }
  ol {
    list-style: none;
    margin: 0.8rem 0 1.6rem;
    padding: 0;
  }
  li {
    border: 1px solid var(--color-border);
    border-radius: 0.5rem;
    padding: 1rem;
    min-width: 0;
    font-size: 0.8rem;
    line-height: 1.9;
  }
  li + li {
    margin-top: 0.7rem;
  }
  dl {
    display: flex;
    flex-wrap: wrap;
    gap: 0.6rem 1.4rem;
    margin: 0.8rem 0;
  }
  dt {
    color: var(--color-muted);
    font-size: 0.7rem;
  }
  dd {
    font-variant-numeric: tabular-nums;
  }
  @media (max-width: 400px) {
    .actions {
      flex-direction: column;
      align-items: stretch;
    }
    li {
      padding: 0.75rem;
    }
  }
</style>
