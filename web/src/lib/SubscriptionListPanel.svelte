<script lang="ts">
  import ActionGroup from './ActionGroup.svelte';
  import Button from './Button.svelte';
  import DataTable from './DataTable.svelte';
  import type { Subscription } from './subscriptions-api.ts';
  import { money, cycle } from './daily-text.ts';
  import ServiceAvatar from './ServiceAvatar.svelte';
  import { subscriptionTitle } from './subscription-title.ts';
  import { lifecycleLabels } from './subscription-lifecycle.ts';
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
    ><strong>{subscriptionTitle(item)}</strong>{#if item.display_name}<small
        >{item.service}</small
      >{/if}<small>{item.plan ?? 'プラン不明'}</small
    >{#each lifecycleLabels(item) as label}<small>{label}</small>{/each}</span
  >{/snippet}
{#if layout === 'cards'}
  <div class="cards">
    {#each items as item (item.id)}<Button
        wrap
        variant="card"
        class={['card', 'part-SubscriptionListPanel-card']}
        {disabled}
        aria-label={`${subscriptionTitle(item)}の詳細`}
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
            >{item.next_billing_date ?? '請求日不明'}{item.next_billing_date &&
            item.next_billing_mode === 'automatic'
              ? '（推定）'
              : ''}</span
          ></span
        ></Button
      >{/each}
  </div>
{:else}
  <DataTable
    responsive="compact"
    density="list"
    class={['table-SubscriptionListPanel']}
  >
    <thead
      ><tr
        ><th>サービス</th><th>登録料金</th><th class="date">次回の請求日</th><th
          class="source">取得元</th
        ><th class="state">状態</th><th><span class="sr">詳細</span></th></tr
      ></thead
    ><tbody
      >{#each items as item (item.id)}<tr
          ><td data-primary class="service"
            ><Button
              wrap
              variant="plain"
              class={['brand', 'part-SubscriptionListPanel-brand']}
              {disabled}
              aria-label={`${subscriptionTitle(item)}の詳細`}
              onclick={() => onSelect(item.id)}>{@render brand(item)}</Button
            ></td
          ><td class="price"
            >{money(item.amount, item.currency)}<small
              >{cycle(item.interval_unit, item.interval_count)}</small
            ></td
          ><td class="date"
            >{item.next_billing_date ?? '請求日不明'}{item.next_billing_date &&
            item.next_billing_mode === 'automatic'
              ? '（推定）'
              : ''}</td
          ><td class="source meta">{source(item)}</td><td class="state"
            ><span class="status" class:active={item.status === 'active'}
              >{status(item)}</span
            ></td
          ><td data-actions class="arrow"
            ><ActionGroup
              ><Button
                wrap
                variant="icon"
                {disabled}
                aria-label={`${subscriptionTitle(item)}の詳細を開く`}
                onclick={() => onSelect(item.id)}>›</Button
              ></ActionGroup
            ></td
          ></tr
        >{/each}</tbody
    >
  </DataTable>
{/if}

<style>
  tr:last-child td {
    border-bottom: 0;
  }
  .brand,
  :global(.part-SubscriptionListPanel-brand) {
    display: flex;
    gap: 12px;
    align-items: center;
    min-width: 0;
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
  td.price small {
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
    background: var(--color-surface-soft);
    color: var(--color-muted);
    white-space: nowrap;
  }
  .active {
    color: var(--color-action);
    background: var(--color-surface-soft);
  }
  .cards {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 16px;
    padding: 20px;
  }
  :global(.part-SubscriptionListPanel-card) {
    gap: 20px;
  }
  :global(.part-SubscriptionListPanel-card):hover {
    border-color: var(--color-action);
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
    thead,
    .state,
    .source {
      display: none;
    }
    tr {
      display: grid;
      grid-template-columns: minmax(0, 1fr) 44px;
      padding: 12px 16px;
      gap: 5px 8px;
      border-bottom: 1px solid var(--color-border);
    }
    .service {
      grid-column: 1;
      grid-row: 1;
    }
    .price {
      grid-column: 1;
      grid-row: 2;
      text-align: left;
      font-size: 12px;
      max-width: none;
    }
    .price small {
      display: inline;
      margin-top: 0;
      margin-inline-start: 6px;
    }
    .date {
      grid-column: 1;
      grid-row: 3;
      font-size: 10px;
      text-align: left;
      white-space: normal;
      color: var(--color-muted);
    }
    .arrow {
      grid-column: 2;
      grid-row: 1/4;
      align-self: center;
    }
    :global(.part-SubscriptionListPanel-brand) {
      gap: 9px;
    }
    .brand-text strong {
      font-size: 12px;
    }
  }
  @media (max-width: 700px) {
    .cards {
      grid-template-columns: 1fr;
      padding: 13px;
    }
  }
  @media (max-width: 360px) {
    .price small {
      display: block;
      margin-top: 4px;
      margin-inline-start: 0;
    }
  }
</style>
