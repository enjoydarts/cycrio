<script lang="ts">
  import { tick } from 'svelte';
  import {
    type Model,
    type Price,
    type PriceInput,
    priceFields,
  } from './catalog-api';
  import {
    datetime,
    httpsURL,
    perMillion,
    priceInput,
    createRevisionDraft,
    reconcileRevisionDraft,
    type RevisionDraft,
  } from './model-catalog';
  let {
    model,
    prices,
    busy,
    save,
    reset,
    invalid,
    reload,
  }: {
    model: Model;
    prices: Price[];
    busy: boolean;
    save: (input: PriceInput) => Promise<boolean>;
    reset: (expected_version: number) => Promise<boolean>;
    invalid: () => void;
    reload: () => Promise<boolean>;
  } = $props();
  let confirmVersion = $state<number | null>(null);
  type PriceForm = Record<
    (typeof priceFields)[number] | 'currency' | 'effective_from' | 'source_url',
    string
  >;
  const blankValues = (): PriceForm =>
    ({
      currency: 'USD',
      effective_from: '',
      source_url: '',
      ...Object.fromEntries(priceFields.map((key) => [key, ''])),
    }) as PriceForm;
  let draft = $state<RevisionDraft<PriceForm> | null>(null);
  $effect(() => {
    draft = reconcileRevisionDraft(draft, model.version, blankValues());
  });
  const labels = {
    input_per_million: '入力 / 100万トークン',
    output_per_million: '出力 / 100万トークン',
    cache_read_per_million: 'キャッシュ読取 / 100万トークン',
    cache_write_per_million: 'キャッシュ書込 / 100万トークン',
    cache_write_30m_per_million: 'キャッシュ書込 30分 / 100万トークン',
    cache_write_1h_per_million: 'キャッシュ書込 1時間 / 100万トークン',
    cache_write_12h_per_million: 'キャッシュ書込 12時間 / 100万トークン',
    per_request: '1リクエスト',
  };
  const rateLabels: Record<string, string> = {
    input: '入力',
    output: '出力',
    cache_read: 'キャッシュ読取',
    cache_write: 'キャッシュ書込',
    cache_write_30m: 'キャッシュ書込 30分',
    cache_write_1h: 'キャッシュ書込 1時間',
    cache_write_12h: 'キャッシュ書込 12時間',
    request: 'リクエスト',
    image: '画像',
    image_output: '画像出力',
    image_token: '画像トークン',
    audio: '音声',
    audio_output: '音声出力',
    audio_cache: '音声キャッシュ',
    web_search: 'Web 検索',
    reasoning: '推論',
  };
  const unitLabels = {
    token: '100万トークン',
    request: 'リクエスト',
    image: '画像',
    search: '検索',
  };
  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !draft) return;
    try {
      if (
        await save(
          priceInput(
            new FormData(event.currentTarget as HTMLFormElement),
            draft.baseline_version,
          ),
        )
      ) {
        await tick();
        draft = createRevisionDraft(model.version, blankValues());
      }
    } catch {
      invalid();
    }
  }
  async function confirmation(value: boolean) {
    confirmVersion = value ? model.version : null;
    await tick();
    document
      .getElementById(value ? 'confirm-price-reset' : 'price-reset')
      ?.focus();
  }
  async function reloadDraft() {
    if (await reload()) {
      await tick();
      draft = createRevisionDraft(model.version, blankValues());
    }
  }
</script>

{#snippet priceDetails(price: Price | null)}
  {#if price}
    <p>
      <code>{price.id}</code><br />{price.currency} · 取得（日本時間）{datetime(
        price.captured_at,
      )}
    </p>
    <dl class="rates">
      {#each ['input', 'output', ...Object.keys(price.rates).filter((k) => k !== 'input' && k !== 'output')] as key}
        {@const rate = price.rates[key]}
        <div>
          <dt>{rateLabels[key] ?? key}</dt>
          <dd>
            {rate
              ? `${rate.unit === 'token' ? perMillion(rate.amount) : rate.amount} ${price.currency} / ${unitLabels[rate.unit]}`
              : '不明・変動'}
          </dd>
        </div>
      {/each}
    </dl>
    <p class="muted">適用開始（日本時間）{datetime(price.effective_from)}</p>
    {#if httpsURL(price.source_url)}<a
        href={price.source_url}
        target="_blank"
        rel="noopener noreferrer">価格の出典を開く</a
      >{:else}<p class="muted">出典 URL なし</p>{/if}
    {#if price.raw_pricing}<details>
        <summary>条件別価格・割引などの元データ</summary>
        <pre>{JSON.stringify(price.raw_pricing, null, 2)}</pre>
      </details>{/if}
  {:else}<p class="muted">不明・変動（無料を意味しません）</p>{/if}
{/snippet}

<section aria-label="モデルの価格">
  <h4>現在の参照価格</h4>
  <p class="muted">
    カタログ上の参考価格です。条件別価格・割引・追加料金があるため、実際の呼出し原価ではありません。空欄は不明、0
    は明示的なゼロです。
  </p>
  {@render priceDetails(model.current_price)}
  <details>
    <summary>最新 API 価格</summary>{@render priceDetails(model.api_price)}
  </details>
  <details>
    <summary>手入力の上書き価格</summary>{@render priceDetails(
      model.manual_price,
    )}
  </details>
  <details>
    <summary>手入力価格を登録・更新</summary>
    <p class="muted">
      一部の料金だけを登録できます。空欄の項目は不明です。API
      同期後もこの上書きを保持します。
    </p>
    {#if draft}
      {#if draft.dirty && draft.baseline_version !== model.version}<p
          class="error"
          role="status"
        >
          編集中に価格・モデル情報が更新されました。入力と編集開始時のリビジョンを保持しています。最新の保存値を読み直してから、もう一度入力してください。
        </p>{/if}
      <form
        onsubmit={submit}
        oninput={() => {
          if (draft) draft.dirty = true;
        }}
      >
        <label for="price-currency">通貨（ISO 3文字）</label><input
          id="price-currency"
          name="currency"
          bind:value={draft.values.currency}
          pattern={'[A-Z]{3}'}
          maxlength="3"
          required
          disabled={busy}
        />
        {#each priceFields as key}<label for={`price-${key}`}
            >{labels[key]}</label
          ><input
            id={`price-${key}`}
            name={key}
            bind:value={draft.values[key]}
            inputmode="decimal"
            placeholder="未入力 = 不明"
            pattern="(?:0|[1-9][0-9]*)(?:\.[0-9]+)?"
            disabled={busy}
          />{/each}
        <label for="price-effective">適用開始日時（日本時間 / JST）</label
        ><input
          id="price-effective"
          name="effective_from"
          type="datetime-local"
          bind:value={draft.values.effective_from}
          required
          disabled={busy}
        />
        <label for="price-source">出典 URL（HTTPS 必須）</label><input
          id="price-source"
          name="source_url"
          type="url"
          bind:value={draft.values.source_url}
          placeholder="https://…"
          required
          disabled={busy}
        />
        <button type="submit" class="primary" disabled={busy}
          >手入力価格を保存</button
        >
        <button type="button" disabled={busy} onclick={reloadDraft}
          >最新の価格状態を読み直す（入力を破棄）</button
        >
      </form>
    {/if}
  </details>
  {#if model.manual_price}
    {#if confirmVersion !== null}<div
        class="box"
        role="group"
        aria-label="価格上書き解除の確認"
      >
        <p>
          手入力の上書きを解除し、最新 API 価格（未取得なら不明）へ戻しますか？
        </p>
        {#if confirmVersion !== model.version}<p class="error" role="status">
            確認中に保存状態が更新されました。キャンセルして最新の価格を確認してください。
          </p>{/if}
        <div class="actions">
          <button
            id="confirm-price-reset"
            disabled={busy}
            onclick={() => {
              const version = confirmVersion;
              confirmVersion = null;
              if (version !== null) void reset(version);
            }}>上書きを解除する</button
          ><button disabled={busy} onclick={() => confirmation(false)}
            >キャンセル</button
          >
        </div>
      </div>{:else}<button
        id="price-reset"
        disabled={busy}
        onclick={() => confirmation(true)}>API 価格へ戻す</button
      >{/if}
  {/if}
  <details>
    <summary>価格履歴（最新 50 件）</summary>
    <ol>
      {#each prices as price (price.id)}<li class="box">
          <strong>{price.source === 'api' ? 'API 取得' : '手入力'}</strong
          >{@render priceDetails(price)}
        </li>{/each}
    </ol>
    {#if prices.length === 0}<p>履歴はありません。</p>{/if}
  </details>
</section>
