<script lang="ts">
  import type { PublicProcessingModel } from './mail-processing-api.ts';
  let {
    primary,
    secondary,
    max_input_bytes,
    max_output_tokens,
    timeout_seconds,
  }: {
    primary: PublicProcessingModel | null;
    secondary: PublicProcessingModel | null;
    max_input_bytes: number;
    max_output_tokens: number;
    timeout_seconds: number;
  } = $props();
  const providers = {
    liquid: 'Liquid AI',
    openrouter: 'OpenRouter',
    openai: 'OpenAI',
    anthropic: 'Anthropic',
  };
  const stages = $derived([
    { name: '一次判定', model: primary },
    { name: '二次抽出', model: secondary },
  ]);
  const rateNames: Record<string, string> = {
    input: '入力',
    output: '出力',
    cache_read: 'キャッシュ読み取り',
    cache_write: 'キャッシュ書き込み',
    request: 'リクエスト',
    reasoning: '推論',
  };
</script>

<div class="models">
  {#each stages as stage}
    <section class="model" aria-label={stage.name}>
      <h5>{stage.name}</h5>
      {#if stage.model}
        <p>
          <strong>{stage.model.display_name}</strong>
          <span>{stage.model.model_id}</span>
        </p>
        <p>
          外部送信先: {providers[
            stage.model.provider
          ]}{#if stage.model.routing_provider}
            → {stage.model.routing_provider}{/if}
        </p>
        <p>保持条件: {stage.model.retention || '未確認・送信不可'}</p>
        <div class="pricing">
          <p>参考料金: {stage.model.price?.currency ?? '不明'}</p>
          {#if stage.model.price && Object.keys(stage.model.price.rates).length}
            <dl>
              {#each Object.entries(stage.model.price.rates) as [name, rate]}<div
                >
                  <dt>{rateNames[name] ?? name}</dt>
                  <dd>
                    {rate.amount}
                    {stage.model.price.currency} / {rate.unit}
                  </dd>
                </div>{/each}
            </dl>
          {:else}<p>料金は不明です。</p>{/if}
        </div>
      {:else}<p>有効なモデルがありません。</p>{/if}
    </section>
  {/each}
</div>
<p class="note">
  参考料金は確定費用・課金上限ではありません。実際の料金は使用量や配信先の条件で変わります。不明な料金を
  0 として扱いません。
</p>
<p class="note">
  入力上限 {max_input_bytes} bytes / 出力上限 {max_output_tokens} tokens / 1回の待機上限
  {timeout_seconds} 秒
</p>

<style>
  .models {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.8rem;
    margin: 1rem 0;
    min-width: 0;
  }
  .model {
    padding: 1rem;
    border: 1px solid var(--color-border);
    border-radius: 0.5rem;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  h5,
  p,
  dl,
  dd {
    margin: 0;
  }
  h5 {
    font-size: 0.85rem;
    margin-bottom: 0.5rem;
  }
  p,
  .note,
  dl {
    font-size: 0.78rem;
    line-height: 1.9;
  }
  p span {
    display: block;
    color: var(--color-muted);
  }
  .pricing {
    margin-top: 0.7rem;
  }
  dl div {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: 0.2rem 0.7rem;
  }
  dt,
  .note {
    color: var(--color-muted);
  }
  .note {
    margin: 0.6rem 0;
    overflow-wrap: anywhere;
  }
  @media (max-width: 1100px) {
    .models {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
