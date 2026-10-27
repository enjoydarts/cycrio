<script lang="ts">
  import type { Model, PriceInput } from './catalog-api.ts';
  import { priceFields } from './catalog-api.ts';
  import {
    createRevisionDraft,
    priceInput,
    datetime,
    type RevisionDraft,
  } from './model-catalog.ts';
  type Values = Record<
    (typeof priceFields)[number] | 'currency' | 'effective_from' | 'source_url',
    string
  >;
  let {
    model,
    busy,
    save,
    onclose,
    onDirty = () => {},
  }: {
    model: Model;
    busy: boolean;
    save: (model: Model, input: PriceInput) => Promise<boolean>;
    onclose: () => void;
    onDirty?: (value: boolean) => void;
  } = $props();
  const blank = (): Values =>
    ({
      currency: model.current_price?.currency ?? 'USD',
      effective_from: new Date(Date.now() + 9 * 60 * 60 * 1000)
        .toISOString()
        .slice(0, 16),
      source_url: model.current_price?.source_url ?? '',
      ...Object.fromEntries(priceFields.map((key) => [key, ''])),
    }) as Values;
  const initialDraft = () => createRevisionDraft(model.version, blank());
  let draft = $state<RevisionDraft<Values>>(initialDraft()),
    invalid = $state(false);
  const labels = {
    input_per_million: '入力 / 100万トークン',
    output_per_million: '出力 / 100万トークン',
    cache_read_per_million: 'キャッシュ読取 / 100万トークン',
    cache_write_per_million: 'キャッシュ書込 / 100万トークン',
    cache_write_30m_per_million: 'キャッシュ書込 30分 / 100万トークン',
    cache_write_1h_per_million: 'キャッシュ書込 1時間 / 100万トークン',
    cache_write_12h_per_million: 'キャッシュ書込 12時間 / 100万トークン',
    per_request: '1リクエストごとの追加料金',
  };
  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    try {
      invalid = false;
      if (
        await save(
          model,
          priceInput(
            new FormData(event.currentTarget as HTMLFormElement),
            draft.baseline_version,
          ),
        )
      ) {
        onDirty(false);
        onclose();
      }
    } catch {
      invalid = true;
    }
  }
</script>

<section class="setup-panel" aria-label={`${model.display_name} の価格を設定`}>
  <h3>{model.display_name} の価格を設定</h3>
  <p class="muted">
    空欄は不明、0
    は明示的なゼロです。参考価格の版を保存し、実利用原価や現在の有効版は変更しません。
  </p>
  {#if draft.baseline_version !== model.version}<p class="setup-feedback">
      モデル・価格が更新されました。入力と元の版を保持しています。キャンセルして最新状態を読み直してください。
    </p>{/if}
  {#if invalid}<p class="error" role="alert">
      単価、通貨、日時と HTTPS の出典を確認してください。
    </p>{/if}
  <form
    onsubmit={submit}
    oninput={() => {
      draft.dirty = true;
      onDirty(true);
    }}
  >
    <div class="setup-grid">
      {#each ['input_per_million', 'output_per_million', 'per_request'] as field}{@const key =
          field as keyof typeof labels}<label
          >{labels[key]}<input
            name={key}
            bind:value={draft.values[key]}
            inputmode="decimal"
            placeholder="未入力 = 不明"
            pattern={'(?:0|[1-9][0-9]*)(?:[.][0-9]+)?'}
            disabled={busy}
          /></label
        >{/each}<label
        >通貨（ISO 3文字）<input
          name="currency"
          bind:value={draft.values.currency}
          maxlength="3"
          pattern={'[A-Z]{3}'}
          required
          disabled={busy}
        /></label
      >
    </div>
    <details>
      <summary>キャッシュなどの詳細料金</summary
      >{#each priceFields.filter((key) => !['input_per_million', 'output_per_million', 'per_request'].includes(key)) as key}<label
          >{labels[key]}<input
            name={key}
            bind:value={draft.values[key]}
            inputmode="decimal"
            placeholder="未入力 = 不明"
            pattern={'(?:0|[1-9][0-9]*)(?:[.][0-9]+)?'}
            disabled={busy}
          /></label
        >{/each}
    </details>
    <div class="setup-grid">
      <label
        >適用開始日時（日本時間 / JST）<input
          name="effective_from"
          type="datetime-local"
          bind:value={draft.values.effective_from}
          required
          disabled={busy}
        /></label
      ><label
        >価格の出典（HTTPS）<input
          name="source_url"
          type="url"
          bind:value={draft.values.source_url}
          placeholder="https://…"
          required
          disabled={busy}
        /></label
      >
    </div>
    <div class="setup-actions">
      <button type="submit" class="primary" disabled={busy}>価格を保存</button
      ><button
        type="button"
        disabled={busy}
        onclick={() => {
          onDirty(false);
          onclose();
        }}>キャンセル</button
      >
    </div>
  </form>
  {#if model.current_price}<p class="muted">
      現在の参照価格の取得（日本時間）：{datetime(
        model.current_price.captured_at,
      )}
    </p>{/if}
  <a href="/settings/catalog">価格の版・履歴を詳しく確認</a>
</section>
