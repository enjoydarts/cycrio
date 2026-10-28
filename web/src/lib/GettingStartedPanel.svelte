<script lang="ts">
  import type { FeedView } from './daily-controller.ts';
  import {
    gettingStartedGuide,
    type GettingStartedPage,
  } from './getting-started.ts';
  import { processingErrors } from './mail-processing-text.ts';
  let {
    view,
    admin = false,
    onSelect = () => {},
    onRefresh = () => {},
  }: {
    view: FeedView<GettingStartedPage>;
    admin?: boolean;
    onSelect?: (id: string) => void;
    onRefresh?: () => void;
  } = $props();
  const data = $derived(view.status === 'ready' ? view.page : null);
  const guide = $derived(data ? gettingStartedGuide(data, admin) : null);
  const progress = $derived(
    data?.automation?.state !== 'not_configured'
      ? data?.automation?.progress
      : null,
  );
</script>

<article
  class="getting-started"
  aria-label="初回の案内"
  aria-busy={view.status === 'loading'}
>
  <header>
    <h2>サブスクを一覧に表示するまで</h2>
    {#if view.status !== 'denied' && view.status !== 'disposed'}<button
        type="button"
        disabled={view.status === 'loading'}
        onclick={onRefresh}>状態と一覧を更新</button
      >{/if}
  </header>
  {#if view.status === 'loading' || view.status === 'idle'}
    <p role="status">接続と取り込みの状態を確認しています…</p>
  {:else if view.status === 'denied' || view.status === 'disposed'}
    <p role="status">利用状態を確認しています…</p>
  {:else if view.error}
    <div role="alert">
      <p>準備状況を確認できませんでした。</p>
      <p>{processingErrors[view.error]}</p>
      <button type="button" onclick={onRefresh}>もう一度確認</button>
    </div>
  {:else if guide && data}
    {#if data.accounts.length > 1}
      <label class="account-picker"
        >案内を確認するメール接続<select
          value={data.selected?.id ?? ''}
          onchange={(event) => onSelect(event.currentTarget.value)}
          >{#each data.accounts as account (account.id)}<option
              value={account.id}>{account.label}</option
            >{/each}</select
        ></label
      >
    {:else if data.selected}<p class="scope">
        対象のメール接続：{data.selected.label}
      </p>{/if}
    <div class="guide-message" aria-live="polite">
      <h3>{guide.title}</h3>
      <p>{guide.description}</p>
      {#if guide.href}<a class="button primary" href={guide.href}
          >{guide.label}</a
        >{/if}
    </div>
    <ol class="steps" aria-label="サブスク登録までの流れ">
      <li>
        <strong>メールを連携</strong><span>接続と対象フォルダーを確認</span>
      </li>
      <li>
        <strong>送信条件を確認</strong><span
          >管理者のAI準備と本文の送信許可</span
        >
      </li>
      <li>
        <strong>取り込み・解析</strong><span>対象期間を確認して開始</span>
      </li>
      <li>
        <strong>結果とサブスクを確認</strong><span
          >要確認の項目は内容を確認・訂正</span
        >
      </li>
    </ol>
    {#if progress}<dl aria-label="選択した接続の処理状況" class="progress">
        <div>
          <dt>発見</dt>
          <dd>{progress.discovered_count} 件</dd>
        </div>
        <div>
          <dt>解析待ち</dt>
          <dd>{progress.pending_count} 件</dd>
        </div>
        <div>
          <dt>解析済み</dt>
          <dd>{progress.completed_count} 件</dd>
        </div>
        <div>
          <dt>解析失敗</dt>
          <dd>{progress.failed_count} 件</dd>
        </div>
        <div>
          <dt>結果不明</dt>
          <dd>{progress.unknown_count} 件</dd>
        </div>
      </dl>{/if}
    <p class="note">
      対象期間・本文の外部送信への同意と開始操作は、メール解析画面で行います。
    </p>
  {/if}
</article>

<style>
  .getting-started {
    border: 1px solid var(--color-border);
    border-radius: 8px;
    padding: clamp(16px, 3vw, 28px);
    background: var(--color-surface);
    min-width: 0;
  }
  header {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  h2 {
    font-size: 18px;
    margin: 0;
  }
  h3 {
    font-size: 20px;
    margin: 0 0 8px;
  }
  p {
    line-height: 1.8;
    overflow-wrap: anywhere;
  }
  .guide-message {
    margin-block: 24px;
  }
  .account-picker {
    display: grid;
    gap: 6px;
    margin-top: 20px;
    max-width: 32rem;
  }
  select {
    min-width: 0;
    width: 100%;
  }
  .scope,
  .note {
    color: var(--color-muted);
    font-size: 13px;
  }
  .steps {
    display: grid;
    gap: 14px;
    padding-inline-start: 22px;
    margin-block: 24px;
  }
  .steps strong,
  .steps span {
    display: block;
    overflow-wrap: anywhere;
  }
  .steps span {
    color: var(--color-muted);
    font-size: 13px;
    margin-top: 3px;
  }
  .progress {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(100px, 1fr));
    gap: 12px;
    margin-block: 24px;
  }
  dt {
    color: var(--color-muted);
    font-size: 13px;
  }
  dd {
    margin: 4px 0 0;
    font-weight: 600;
  }
  .note {
    margin-bottom: 0;
  }
  a {
    overflow-wrap: anywhere;
  }
</style>
