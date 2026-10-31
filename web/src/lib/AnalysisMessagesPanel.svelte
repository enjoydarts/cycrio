<script lang="ts">
  import DataTable from './DataTable.svelte';
  import Button from './Button.svelte';
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
    <DataTable
      responsive="cards"
      class={['table-AnalysisMessagesPanel']}
      aria-label="メールごとの取り込み履歴"
    >
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
            <td data-primary
              ><Button
                wrap
                variant="text"
                class={[
                  'message-open',
                  'part-AnalysisMessagesPanel-message-open',
                ]}
                aria-pressed={selectedId === message.id}
                onclick={() => onSelect(message)}>{messageName(message)}</Button
              ><span class="secondary"
                >{message.account_label}{#if message.event_type}
                  · {eventNames[
                    message.event_type as keyof typeof eventNames
                  ] ?? '抽出情報'}{/if}{#if message.amount || message.currency}
                  · {money(message.amount, message.currency)}{/if}</span
              >
            </td>
            <td data-label="一次判定"
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
            <td data-label="二次抽出"
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
            <td data-label="登録"
              ><span
                class="stage"
                class:attention={['failed', 'needs_review', 'unknown'].includes(
                  message.registration,
                )}>{stageNames[message.registration]}</span
              ></td
            >
            <td data-label="取込日時（日本時間）"
              ><time datetime={message.discovered_at}
                >{processingDate(message.discovered_at)}</time
              ></td
            >
          </tr>{/each}</tbody
      >
    </DataTable>
  </div>{:else}<p class="empty">該当するメールはありません。</p>{/if}

<style>
  .message-list {
    max-width: 100%;
    overflow-x: auto;
    border: 1px solid var(--color-border);
    border-radius: 8px;
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
  :global(.part-AnalysisMessagesPanel-message-open) {
    display: block;
    text-align: left;
    color: var(--color-text);
    font-size: 13px;
    font-weight: 600;
  }
  :global(.part-AnalysisMessagesPanel-message-open):hover {
    text-decoration: underline;
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
</style>
