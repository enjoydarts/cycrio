<script lang="ts">
  import type { Subscription } from './subscriptions-api.ts';
  import { money, cycle } from './daily-text.ts';
  import ServiceAvatar from './ServiceAvatar.svelte';
  let {
    items,
    layout = 'list',
    onSelect,
    disabled = false,
  }: {
    items: Subscription[];
    layout?: 'list' | 'cards';
    onSelect: (id: string) => void;
    disabled?: boolean;
  } = $props();
  const status = (s: Subscription) =>
    s.status === 'active'
      ? '利用中'
      : s.status === 'ended'
        ? '終了済み'
        : '状態不明';
  const source = (s: Subscription) =>
    [...new Set(s.sources.map((x) => x.account_label).filter(Boolean))].join(
      ' / ',
    ) || '取得元情報なし';
</script>

{#snippet brand(item: Subscription)}<ServiceAvatar name={item.service} /><span
    class="brand-text"
    ><strong>{item.service}</strong><small>{item.plan ?? 'プラン不明'}</small
    ></span
  >{/snippet}
{#if layout === 'cards'}
  <div class="cards">
    {#each items as item (item.id)}<button
        class="card"
        {disabled}
        aria-label={`${item.service}の詳細`}
        onclick={() => onSelect(item.id)}
        ><span class="brand">{@render brand(item)}</span><span class="card-line"
          ><strong class="price"
            >{money(item.amount, item.currency)}<small
              >{cycle(item.interval_unit, item.interval_count)}</small
            ></strong
          ><span class:active={item.status === 'active'} class="status"
            >{status(item)}</span
          ></span
        ><span class="card-line meta"
          ><span>{source(item)}</span><span
            >{item.next_billing_date ?? '請求日不明'}</span
          ></span
        ></button
      >{/each}
  </div>
{:else}
  <table>
    <thead
      ><tr
        ><th>サービス</th><th>登録料金</th><th>次回の請求日</th><th
          class="source">取得元</th
        ><th class="state">状態</th><th><span class="sr">詳細</span></th></tr
      ></thead
    ><tbody
      >{#each items as item (item.id)}<tr
          ><td class="service"
            ><button
              class="brand"
              {disabled}
              aria-label={`${item.service}の詳細`}
              onclick={() => onSelect(item.id)}>{@render brand(item)}</button
            ></td
          ><td class="price"
            >{money(item.amount, item.currency)}<small
              >{cycle(item.interval_unit, item.interval_count)}</small
            ></td
          ><td class="date">{item.next_billing_date ?? '請求日不明'}</td><td
            class="source meta">{source(item)}</td
          ><td class="state"
            ><span class="status" class:active={item.status === 'active'}
              >{status(item)}</span
            ></td
          ><td class="arrow"
            ><button
              {disabled}
              aria-label={`${item.service}の詳細を開く`}
              onclick={() => onSelect(item.id)}>›</button
            ></td
          ></tr
        >{/each}</tbody
    >
  </table>
{/if}

<style>
  table {
    width: 100%;
    border-collapse: collapse;
    table-layout: auto;
  }
  th {
    text-align: left;
    padding: 15px 20px;
    color: var(--color-muted);
    font-size: 11px;
    font-weight: 500;
    border-bottom: 1px solid var(--color-border);
  }
  td {
    padding: 18px 20px;
    border-bottom: 1px solid var(--color-border);
    font-size: 13px;
  }
  tr:last-child td {
    border-bottom: 0;
  }
  .brand {
    display: flex;
    gap: 12px;
    align-items: center;
    text-align: left;
    border: 0;
    background: none;
    padding: 0;
    color: inherit;
    min-width: 0;
  }
  button {
    cursor: pointer;
  }
  button:disabled {
    cursor: default;
  }
  .brand-text {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  strong {
    font-weight: 600;
  }
  small {
    display: block;
    margin-top: 4px;
    color: var(--color-muted);
    font-size: 11px;
    font-weight: 400;
  }
  .price {
    overflow-wrap: anywhere;
    min-width: 0;
    font-variant-numeric: tabular-nums;
  }
  .date {
    white-space: nowrap;
  }
  .meta {
    color: var(--color-muted);
    font-size: 11px;
    overflow-wrap: anywhere;
  }
  .status {
    font-size: 11px;
    border-radius: 5px;
    padding: 5px 8px;
    background: var(--color-background, #f4f4f8);
    color: var(--color-muted);
    white-space: nowrap;
  }
  .active {
    color: #278768;
    background: #eaf7f0;
  }
  .arrow button {
    border: 0;
    background: transparent;
    color: var(--color-muted);
    font-size: 24px;
    min-width: 32px;
    min-height: 40px;
  }
  .cards {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 16px;
    padding: 20px;
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: 20px;
    border: 1px solid var(--color-border);
    border-radius: 11px;
    padding: 19px;
    background: var(--color-surface);
    color: var(--color-text);
    min-width: 0;
    text-align: left;
  }
  .card:hover {
    border-color: var(--brand, #8876df);
  }
  .card-line {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    align-items: center;
  }
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
  @media (max-width: 1100px) {
    .cards {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    .source {
      display: none;
    }
  }
  @media (max-width: 650px) {
    .cards {
      grid-template-columns: 1fr;
      padding: 13px;
    }
    thead,
    .state,
    .source {
      display: none;
    }
    tr {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto 22px;
      padding: 16px;
      gap: 5px 8px;
      border-bottom: 1px solid var(--color-border);
    }
    td {
      padding: 0;
      border: 0;
    }
    .service {
      grid-row: 1/3;
    }
    .price {
      grid-column: 2;
      text-align: right;
      font-size: 12px;
      max-width: 120px;
    }
    .date {
      grid-column: 2;
      font-size: 10px;
      text-align: right;
      color: var(--color-muted);
    }
    .arrow {
      grid-column: 3;
      grid-row: 1/3;
      align-self: center;
    }
    .brand {
      gap: 9px;
    }
    .brand-text strong {
      font-size: 12px;
    }
  }
</style>
