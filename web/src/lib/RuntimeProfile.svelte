<script lang="ts">
  import type { Profile } from './airuntime-api';
  import { datetime, perMillion, providerNames } from './model-catalog';
  let { profile, title }: { profile: Profile | null; title: string } = $props();
</script>

<article class="box runtime-profile">
  <h4>{title}</h4>
  {#if profile}
    {#each [{ label: '一次判定', model: profile.primary }, { label: '二次処理', model: profile.secondary }] as item}
      <div class="profile-model">
        <span class="muted"
          >{item.label} · {providerNames[item.model.provider]}</span
        >
        <strong>{item.model.display_name}</strong><code
          >{item.model.model_id}</code
        >
        <p>API キー版 {item.model.credential_version}</p>
        {#if item.model.price}
          <p>
            保存時の参考価格：入力 {perMillion(
              item.model.price.rates.input?.unit === 'token'
                ? item.model.price.rates.input.amount
                : undefined,
            )} / 出力 {perMillion(
              item.model.price.rates.output?.unit === 'token'
                ? item.model.price.rates.output.amount
                : undefined,
            )}
            {item.model.price.currency}（100万トークン当たり）
          </p>
          <p class="muted">
            価格取得 {datetime(item.model.price.captured_at)} JST · {item.model
              .price.source === 'api'
              ? 'API 取得'
              : '管理者指定'}
          </p>
          <details>
            <summary>{item.label}の価格条件と出典</summary>
            <p>適用開始 {datetime(item.model.price.effective_from)} JST</p>
            <p>{item.model.price.source_url || '出典 URL なし'}</p>
            <pre>{JSON.stringify(item.model.price.rates, null, 2)}</pre>
            <pre>{JSON.stringify(item.model.price.raw_pricing, null, 2)}</pre>
          </details>
        {:else}<p>保存時の参考価格：不明（無料を意味しません）</p>{/if}
      </div>
    {/each}
    <p>
      入力上限 {profile.max_input_bytes} bytes · 出力上限 {profile.max_output_tokens}
      token · 制限時間 {profile.timeout_seconds} 秒
    </p>
    {#if profile.openrouter_provider}<p>
        OpenRouter の承認配信先：{profile.openrouter_provider}
      </p>{/if}
    {#if profile.anthropic_workspace_id}<p>
        Anthropic ワークスペース：{profile.anthropic_workspace_id}
      </p>{/if}
    <p class="muted">
      保存 {datetime(profile.created_at)} JST · プロンプト {profile.prompt_version}
    </p>
    <details>
      <summary>技術情報（設定と参照版の ID）</summary>
      <dl>
        <div>
          <dt>実行設定</dt>
          <dd><code>{profile.id}</code></dd>
        </div>
        <div>
          <dt>モデル選択</dt>
          <dd><code>{profile.catalog_settings_id}</code></dd>
        </div>
        {#each [profile.primary, profile.secondary] as model}<div>
            <dt>{model.display_name} / モデル・キー版・価格版</dt>
            <dd>
              <code
                >{model.id} / {model.credential_id} / {model.price?.id ??
                  '不明'}</code
              >
            </dd>
          </div>{/each}
      </dl>
    </details>
  {:else}<p class="muted">未設定</p>{/if}
</article>
