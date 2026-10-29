<script lang="ts">
  import type { AnalysisMessage } from './analysis-messages-api.ts';
  import { messageName, stageNames } from './analysis-messages-text.ts';
  import { eventNames, money } from './daily-text.ts';
  import { processingDate } from './mail-processing-text.ts';
  let {
    messages,
    selectedId = null,
    onSelect,
  }: {
    messages: AnalysisMessage[];
    selectedId?: string | null;
    onSelect: (message: AnalysisMessage) => void;
  } = $props();
</script>

{#if messages.length}<div class="message-list">
    <table aria-label="メールごとの取り込み履歴">
      <thead
        ><tr
          ><th>検出したサービス・イベント</th><th>一次判定</th><th>二次抽出</th
          ><th>登録</th><th>取込日時（日本時間）</th></tr
        ></thead
      >
      <tbody
        >{#each messages as message (message.id)}<tr
            class:selected={selectedId === message.id}
          >
            <td
              ><button
                class="message-open"
                aria-pressed={selectedId === message.id}
                onclick={() => onSelect(message)}>{messageName(message)}</button
              ><span class="secondary"
                >{message.account_label}{#if message.event_type}
                  · {eventNames[
                    message.event_type as keyof typeof eventNames
                  ] ?? '抽出情報'}{/if}{#if message.amount || message.currency}
                  · {money(message.amount, message.currency)}{/if}</span
              >
              <div class="mobile-stages" aria-label="工程">
                <span>一次判定 {stageNames[message.primary]}</span><span
                  >二次抽出 {stageNames[message.secondary]}</span
                ><span>登録 {stageNames[message.registration]}</span>
              </div>
              <span class="mobile-date"
                >取込日時 {processingDate(
                  message.discovered_at,
                )}（日本時間）</span
              ></td
            >
            <td
              ><span
                class="stage"
                class:attention={['failed', 'needs_review', 'unknown'].includes(
                  message.primary,
                )}
                >{message.primary === 'completed'
                  ? '一次判定済み'
                  : stageNames[message.primary]}</span
              ></td
            >
            <td
              ><span
                class="stage"
                class:attention={['failed', 'needs_review', 'unknown'].includes(
                  message.secondary,
                )}
                >{message.secondary === 'waiting'
                  ? '二次抽出待ち'
                  : stageNames[message.secondary]}</span
              ></td
            >
            <td
              ><span
                class="stage"
                class:attention={['failed', 'needs_review', 'unknown'].includes(
                  message.registration,
                )}>{stageNames[message.registration]}</span
              ></td
            >
            <td
              ><time datetime={message.discovered_at}
                >{processingDate(message.discovered_at)}</time
              ></td
            >
          </tr>{/each}</tbody
      >
    </table>
  </div>{:else}<p class="empty">該当するメールはありません。</p>{/if}

<style>
  .message-list {
    max-width: 100%;
    overflow-x: auto;
    border: 1px solid var(--color-border);
    border-radius: 8px;
  }
  .mobile-stages,
  .mobile-date {
    display: none;
  }
  .message-list table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
    min-width: 760px;
    table-layout: auto;
  }
  .message-list th {
    text-align: left;
    color: var(--color-muted);
    font-size: 11px;
    font-weight: 500;
    background: var(--color-surface);
  }
  .message-list th,
  .message-list td {
    padding: 11px 12px;
    border-bottom: 1px solid var(--color-border);
    vertical-align: middle;
  }
  .message-list td:first-child {
    width: auto;
  }
  th:first-child {
    width: 38%;
  }
  tbody tr:last-child td {
    border-bottom: 0;
  }
  .selected {
    background: var(--color-surface-soft);
  }
  .message-open {
    display: block;
    text-align: left;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--color-text);
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    max-width: 100%;
    overflow-wrap: anywhere;
    min-height: 28px;
  }
  .message-open:hover {
    text-decoration: underline;
  }
  .message-open:focus-visible {
    outline: 2px solid var(--color-action);
    outline-offset: 3px;
  }
  .secondary {
    display: block;
    font-size: 11px;
    color: var(--color-muted);
    margin-top: 2px;
    overflow-wrap: anywhere;
  }
  .stage,
  time {
    white-space: nowrap;
  }
  .stage {
    font-size: 11px;
  }
  .attention {
    color: var(--color-danger);
  }
  @media (max-width: 550px) {
    .message-list table {
      display: table;
      min-width: 0;
      table-layout: fixed;
    }
    .message-list thead {
      display: table-header-group;
    }
    .message-list tbody {
      display: table-row-group;
      width: auto;
    }
    .message-list tr {
      display: table-row;
      width: auto;
      padding: 0;
      border: 0;
    }
    .message-list td {
      display: table-cell;
      width: auto;
    }
    .message-list th,
    .message-list td {
      padding: 9px;
    }
    .message-list td::before {
      display: none;
    }
    .message-list th:not(:first-child),
    .message-list td:not(:first-child) {
      display: none;
    }
    .message-list th:first-child,
    .message-list td:first-child {
      width: 100%;
    }
    .mobile-stages {
      display: flex;
      flex-wrap: wrap;
      gap: 4px 12px;
      margin-top: 6px;
      font-size: 11px;
      color: var(--color-text);
    }
    .mobile-date {
      display: block;
      color: var(--color-muted);
      font-size: 11px;
      margin-top: 6px;
    }
    .message-open {
      min-height: 26px;
      font-size: 13px;
    }
  }
</style>
