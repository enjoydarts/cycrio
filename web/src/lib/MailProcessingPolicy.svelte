<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api.ts';
  import type { MailProcessingAPI } from './mail-processing-api.ts';
  import {
    createProcessingPolicyController,
    emptyProcessingPolicyView,
    validPolicyForm,
  } from './mail-processing-policy-controller.ts';
  import { processingErrors } from './mail-processing-text.ts';
  import MailProcessingModels from './MailProcessingModels.svelte';
  let {
    api,
    token,
    onDenied,
  }: { api: MailProcessingAPI; token: TokenProvider; onDenied: () => void } =
    $props();
  let view = $state(emptyProcessingPolicyView());
  let controller =
    $state<ReturnType<typeof createProcessingPolicyController>>();
  const busy = $derived(view.status === 'loading' || view.status === 'saving');
  onMount(() => {
    controller = createProcessingPolicyController(
      api,
      token,
      (next) => {
        view = next;
      },
      onDenied,
    );
    void controller.refresh();
    return () => controller?.dispose();
  });
</script>

<section class="policy" aria-label="メール解析の送信条件">
  <div class="heading">
    <div>
      <p class="eyebrow">MAIL ANALYSIS POLICY</p>
      <h3>メール解析の送信条件</h3>
    </div>
    <button
      id="processing-policy-refresh"
      type="button"
      disabled={busy}
      onclick={() => controller?.refresh()}>現在の条件を確認</button
    >
  </div>
  <p class="help">
    有効な AI
    設定のモデルと配信先について、管理者が実際の契約条件・保持条件を確認して記録します。この画面では外部
    AI
    を実行しません。秘密値や個人データは入力せず、保持条件だけを記入してください。
  </p>
  <div class="sources">
    <strong>契約・保持条件の確認先</strong>
    <p>
      実際に適用される契約、プラン、配信先の条件を管理者ご本人が確認してください。以下の資料だけで保持条件を保証するものではありません。
    </p>
    <ul>
      <li>
        <a
          href="https://www.liquid.ai/terms-conditions"
          target="_blank"
          rel="noreferrer">Liquid AI Terms</a
        >
        /
        <a
          href="https://www.liquid.ai/privacy-policy"
          target="_blank"
          rel="noreferrer">Liquid AI Privacy</a
        >
      </li>
      <li>
        <a
          href="https://openrouter.ai/docs/guides/features/zdr"
          target="_blank"
          rel="noreferrer">OpenRouter の ZDR と配信先条件</a
        >
      </li>
      <li>
        <a
          href="https://openai.com/enterprise-privacy/"
          target="_blank"
          rel="noreferrer">OpenAI API のプライバシー</a
        >
      </li>
      <li>
        <a
          href="https://privacy.claude.com/en/articles/7996866-how-long-do-you-store-my-organization-s-data"
          target="_blank"
          rel="noreferrer">Anthropic API のデータ保持</a
        >
      </li>
    </ul>
  </div>
  {#if view.current?.profile}
    <p class="saved">
      保存済みの送信許可: <strong
        >{view.current.policy?.enabled
          ? '許可中'
          : '停止中（初期状態では送信不可）'}</strong
      >
    </p>
    <MailProcessingModels {...view.current.profile} />
  {:else}<p class="feedback">
      先に AI
      設定を有効化してください。有効なモデルがない間は、送信条件を保存できません。
    </p>{/if}
  {#if view.conflict}<p class="feedback" role="alert">
      有効なモデルまたは保存済み条件が変わりました。入力と元のモデル・基準バージョンを保持しています。「入力を破棄して最新条件を確認」を押してから、新しいモデルの契約条件を改めて確認してください。
    </p>{/if}
  {#if view.form}
    <form
      onsubmit={(event) => {
        event.preventDefault();
        void controller?.save();
      }}
    >
      <div class="fields">
        <div>
          <label for="processing-primary-retention"
            >一次判定の保持条件<input
              id="processing-primary-retention"
              type="text"
              required={view.form.enabled}
              value={view.form.primary_retention}
              disabled={view.status === 'saving'}
              oninput={(event) =>
                controller?.edit({
                  primary_retention: event.currentTarget.value,
                })}
            /></label
          >
          <p class="help">1〜500文字。改行や制御文字は使用できません。</p>
          <label class="confirm"
            ><input
              type="checkbox"
              checked={view.form.primary_confirmed}
              disabled={view.status === 'saving'}
              onchange={(event) =>
                controller?.edit({
                  primary_confirmed: event.currentTarget.checked,
                })}
            />一次判定のモデル・配信先に適用される保持条件を契約で確認しました</label
          >
        </div>
        <div>
          <label for="processing-secondary-retention"
            >二次抽出の保持条件<input
              id="processing-secondary-retention"
              type="text"
              required={view.form.enabled}
              value={view.form.secondary_retention}
              disabled={view.status === 'saving'}
              oninput={(event) =>
                controller?.edit({
                  secondary_retention: event.currentTarget.value,
                })}
            /></label
          >
          <p class="help">
            1〜500文字。OpenRouter
            では承認した実配信先の条件も確認してください。
          </p>
          <label class="confirm"
            ><input
              type="checkbox"
              checked={view.form.secondary_confirmed}
              disabled={view.status === 'saving'}
              onchange={(event) =>
                controller?.edit({
                  secondary_confirmed: event.currentTarget.checked,
                })}
            />二次抽出のモデル・実配信先に適用される保持条件を契約で確認しました</label
          >
        </div>
      </div>
      <label class="confirm enable"
        ><input
          type="checkbox"
          checked={view.form.enabled}
          disabled={view.status === 'saving'}
          onchange={(event) =>
            controller?.edit({ enabled: event.currentTarget.checked })}
        />このモデル構成で本人によるメール本文の外部送信を許可する</label
      >
      <p class="help">
        許可するには両方の保持条件と確認が必要です。モデル構成を変えた場合は、新しい構成で確認し直してください。
      </p>
      <div class="actions">
        <button
          id="processing-policy-save"
          class="primary"
          type="submit"
          disabled={busy || view.conflict || !validPolicyForm(view.form)}
          >入力した送信条件を保存</button
        ><button
          id="processing-policy-discard"
          type="button"
          disabled={view.status === 'saving'}
          onclick={() => controller?.discard()}
          >入力を破棄して最新条件を確認</button
        >
      </div>
    </form>
  {/if}
  <div class="stop">
    <h4>保存済みの送信許可を停止</h4>
    <p class="help">
      保存済みの条件を使って新しい送信を停止します。編集中の入力は保持します。すでに外部へ送信した処理は取り消せません。
    </p>
    <button
      id="processing-policy-stop"
      type="button"
      disabled={busy || !view.current?.policy?.enabled}
      onclick={() => controller?.stop()}>保存済みの送信許可を停止する</button
    >
  </div>
  <div aria-live="polite" aria-atomic="true">
    {#if busy}<p class="feedback">
        送信条件を{view.status === 'saving' ? '保存' : '確認'}しています。
      </p>{/if}{#if view.error}<p class="feedback" role="alert">
        {processingErrors[view.error]}
      </p>{/if}
  </div>
</section>

<style>
  .policy {
    margin-top: 1.5rem;
    min-width: 0;
  }
  h3,
  h4,
  p {
    margin: 0;
  }
  h3 {
    font-size: 1.2rem;
  }
  h4 {
    font-size: 0.9rem;
  }
  .heading,
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.8rem;
  }
  .eyebrow {
    color: var(--color-action);
    font-size: 0.75rem;
    letter-spacing: 0.12em;
    margin-bottom: 0.5rem;
  }
  .help,
  .sources {
    font-size: 0.8rem;
    line-height: 1.9;
    color: var(--color-muted);
    overflow-wrap: anywhere;
  }
  .help {
    margin: 0.7rem 0;
  }
  .sources {
    padding: 1rem;
    background: var(--color-surface-soft);
    border: 1px solid var(--color-border);
    border-radius: 0.5rem;
    margin: 1rem 0;
  }
  .sources strong {
    color: var(--color-text);
  }
  .sources ul {
    margin: 0.5rem 0 0;
    padding-left: 1.3rem;
  }
  a {
    color: var(--color-action);
  }
  .saved {
    font-size: 0.85rem;
    margin: 1rem 0;
  }
  .fields {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1.2rem;
    margin-top: 1.3rem;
  }
  .fields > div {
    min-width: 0;
  }
  label {
    font-size: 0.8rem;
    line-height: 1.9;
  }
  .fields > div > label:first-child {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  input[type='text'] {
    width: 100%;
    box-sizing: border-box;
    min-width: 0;
    border: 1px solid var(--color-border);
    border-radius: 0.45rem;
    padding: 0.7rem;
    background: var(--color-surface);
    color: var(--color-text);
    font: inherit;
    font-size: 1rem;
  }
  .confirm {
    display: flex;
    align-items: flex-start;
    gap: 0.6rem;
  }
  .enable {
    margin-top: 1.5rem;
  }
  input[type='checkbox'] {
    flex: none;
    width: 1rem;
    height: 1rem;
    accent-color: var(--color-action);
    margin-top: 0.25rem;
  }
  button {
    min-height: 2.8rem;
    max-width: 100%;
    padding: 0.6rem 0.9rem;
    border: 1px solid var(--color-action);
    border-radius: 0.5rem;
    background: var(--color-surface);
    color: var(--color-action);
    font: inherit;
    font-size: 0.8rem;
    line-height: 1.6;
    cursor: pointer;
  }
  button:disabled {
    opacity: 0.55;
    cursor: default;
  }
  .primary {
    background: var(--color-action);
    color: var(--color-on-action);
  }
  button:focus-visible,
  input:focus-visible {
    outline: 2px solid var(--color-action);
    outline-offset: 2px;
  }
  .actions {
    justify-content: flex-start;
    margin-top: 1rem;
  }
  .stop {
    padding-top: 1.5rem;
    margin-top: 1.5rem;
    border-top: 1px solid var(--color-border);
  }
  .feedback {
    margin: 0.8rem 0;
    padding: 0.8rem 1rem;
    border-left: 3px solid var(--color-action);
    background: var(--color-surface-soft);
    font-size: 0.8rem;
    line-height: 1.9;
    overflow-wrap: anywhere;
  }
  @media (max-width: 1000px) {
    .fields {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  @media (max-width: 400px) {
    .actions {
      flex-direction: column;
      align-items: stretch;
    }
    .sources {
      padding: 0.8rem;
    }
  }
</style>
