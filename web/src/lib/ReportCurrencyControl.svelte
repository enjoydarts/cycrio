<script lang="ts">
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import type { FeedView } from './daily-controller.ts';
  import type { ExchangeRates } from './exchange-rates-api.ts';
  import type { CurrencyMode } from './report-currency.ts';
  let {
    mode,
    ratesView,
    onchange,
    onRetry,
    excluded = 0,
    excludedActual = 0,
    excludedUpcoming = 0,
  }: {
    mode: CurrencyMode;
    ratesView: FeedView<ExchangeRates>;
    onchange: (mode: CurrencyMode) => void;
    onRetry: () => void;
    excluded?: number;
    excludedActual?: number;
    excludedUpcoming?: number;
  } = $props();
  const rates = $derived(ratesView.status === 'ready' ? ratesView.page : null);
  const fetchedAt = $derived(
    rates
      ? new Intl.DateTimeFormat('ja-JP', {
          timeZone: 'Asia/Tokyo',
          year: 'numeric',
          month: '2-digit',
          day: '2-digit',
          hour: '2-digit',
          minute: '2-digit',
        }).format(new Date(rates.fetched_at))
      : null,
  );
</script>

<div class="library-content currency-control">
  <ActionGroup
    ><Button
      type="button"
      variant="tab"
      aria-pressed={mode === 'native'}
      onclick={() => onchange('native')}>元通貨</Button
    ><Button
      type="button"
      variant="tab"
      aria-pressed={mode === 'jpy'}
      onclick={() => onchange('jpy')}>円換算</Button
    ></ActionGroup
  >
  {#if rates}<p>
      <a href={rates.source_url} target="_blank" rel="noreferrer"
        >{rates.source}</a
      >
      · 基準日 {rates.as_of}
    </p>
  {:else if ratesView.status === 'error'}<p role="alert">
      最新の為替レートを取得できませんでした。元通貨のレポートは確認できます。
    </p>
    <Button type="button" onclick={onRetry}>為替レートを再取得</Button>
  {:else if ratesView.status === 'loading' || ratesView.status === 'idle'}<p
      role="status"
    >
      最新の為替レートを取得中…
    </p>{/if}
  {#if rates}<details>
      <summary>換算条件</summary>
      <p>
        取得日時 <time datetime={rates.fetched_at}>{fetchedAt}</time
        >（日本時間）
      </p>
      <p>
        {rates.rates.map((x) => `1 ${x.currency} = ${x.rate} JPY`).join(' / ')}
      </p>
      <p>
        最新レートによる参考換算です。過去実績も最新レートで評価し、支払当時の円額を示すものではありません。元の契約料金・通貨は変更しません。
      </p>
      <p>
        JPY・USD・EUR
        以外の通貨や金額不明は換算対象外です。判明分のみ集計し、不明額を0にしません。利用状態ごとの件数は契約全体、換算対象は対応通貨で月・年換算できる契約の件数です。
      </p>
    </details>{/if}
  {#if mode === 'jpy' && rates && (excluded || excludedActual || excludedUpcoming)}<p
    >
      換算対象外: 契約 {excluded} 件 / 実績の通貨行 {excludedActual} 件 / 請求予定
      {excludedUpcoming} 件
    </p>{/if}
</div>

<style>
  .currency-control {
    border-bottom: 1px solid var(--color-border);
    overflow-wrap: anywhere;
  }
  .currency-control p {
    font-size: 12px;
    color: var(--color-muted);
    line-height: 1.8;
    margin: 10px 0 0;
  }
  details {
    font-size: 12px;
    margin-top: 10px;
  }
  summary {
    cursor: pointer;
    min-height: 30px;
  }
</style>
