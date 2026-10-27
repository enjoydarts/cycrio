<script lang="ts">
  import type { BillingEntry } from './billing-api.ts';
  let {
    entries,
    onselect,
    label = '詳細を見る',
    disabled = false,
  }: {
    entries: BillingEntry[];
    onselect?: (entry: BillingEntry) => void;
    label?: string;
    disabled?: boolean;
  } = $props();
  const kinds = { invoice: '請求', payment: '支払い', refund: '返金' };
</script>

<table class="billing-table">
  <thead
    ><tr
      ><th>日付</th><th>種別</th><th>金額 / 通貨</th><th>状態</th><th>根拠</th
      >{#if onselect}<th>操作</th>{/if}</tr
    ></thead
  >
  <tbody
    >{#each entries as entry (entry.id)}<tr>
        <td data-label="日付">{entry.occurred_on}</td>
        <td data-label="種別">{kinds[entry.kind]}</td>
        <td data-label="金額 / 通貨" class="money"
          >{entry.amount} {entry.currency}</td
        >
        <td data-label="状態"
          ><span class="badge"
            >{entry.state === 'active' ? '有効' : '無効'}</span
          ><span class="secondary"
            >{entry.provenance === 'automatic' ? '自動記録' : '本人確認'}</span
          ></td
        >
        <td data-label="根拠">{entry.sources.length} 件</td>
        {#if onselect}<td
            ><button type="button" {disabled} onclick={() => onselect?.(entry)}
              >{label}</button
            ></td
          >{/if}
      </tr>{/each}</tbody
  >
</table>
