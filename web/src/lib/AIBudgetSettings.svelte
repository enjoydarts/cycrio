<script lang="ts">
  import type { TokenProvider } from './api.ts';
  import type { AIBudgetAPI } from './aibudget-api.ts';
  import {
    createAIBudgetController,
    emptyBudgetView,
  } from './aibudget-controller.ts';
  import {
    processingBlocked,
    processingErrors,
  } from './mail-processing-text.ts';
  import LibraryPanel from './LibraryPanel.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  let {
    owner,
    api,
    token,
    onDenied,
  }: {
    owner: string;
    api: AIBudgetAPI;
    token: TokenProvider;
    onDenied: () => void;
  } = $props();
  let budgetOwner = $derived(owner);
  let view = $state(emptyBudgetView());
  $effect(() => {
    const controller = createAIBudgetController(
      api,
      token,
      (v) => (view = v),
      onDenied,
    );
    current = controller;
    void controller.load(budgetOwner);
    return () => controller.dispose();
  });
  let current: ReturnType<typeof createAIBudgetController> | undefined;
</script>

<LibraryPanel padded>
  <section aria-label="AI 月額上限" aria-busy={view.busy}>
    <h2>AI 月額上限</h2>
    <p>対象：<code>{owner}</code></p>
    <p>
      判定対象は現在月（Asia/Tokyo）です。上の原価表示の対象月とは独立しています。管理者の接続テストとシステム処理は対象外です。
    </p>
    <p>
      空欄は上限なし、0 USD は新しい AI 呼び出しを停止します。原価不明・USD
      以外の原価を 0
      として扱いません。実行中の呼び出しや原価の訂正によって上限を超える場合があります。
    </p>
    {#if view.state}
      <p>判定月：{view.state.month} · 集計済み：{view.state.spent_usd} USD</p>
      <p>
        原価不明 {view.state.unknown_calls} 件 · 実行中 {view.state
          .pending_calls} 件 · USD 以外 {view.state.non_usd_calls} 件
      </p>
      <p>
        現在の上限：{view.state.monthly_limit_usd === null
          ? '未設定'
          : `${view.state.monthly_limit_usd} USD`}
      </p>
      {#if view.state.blocked_reason}<p role="status">
          {processingBlocked[view.state.blocked_reason]}
        </p>{/if}
      <form
        onsubmit={(event) => {
          event.preventDefault();
          void current?.save();
        }}
      >
        <label for="ai-budget-limit">月額上限（USD）</label>
        <input
          id="ai-budget-limit"
          type="text"
          inputmode="decimal"
          autocomplete="off"
          value={view.amount}
          disabled={view.busy || view.uncertain}
          oninput={(event) => current?.edit(event.currentTarget.value)}
        />
        <label class="confirmation"
          ><input
            type="checkbox"
            checked={view.confirmed}
            disabled={view.busy || view.uncertain}
            onchange={(event) => current?.confirm(event.currentTarget.checked)}
          />この利用者の月額上限の変更を確認しました</label
        >
        <ActionGroup spacing="before"
          ><Button
            type="submit"
            wrap
            disabled={view.busy || view.uncertain || !view.confirmed}
            >月額上限を保存</Button
          ></ActionGroup
        >
      </form>
    {/if}
    <div aria-live="polite">
      {#if view.busy}<p>月額上限を確認しています。</p>{/if}
      {#if view.error}<p role="alert">{processingErrors[view.error]}</p>{/if}
      {#if view.uncertain}<p>
          保存結果を確定できません。入力と保存時の版を保持しています。「保存結果を再確認」で変更を再送せず照合できます。
        </p>{/if}
      {#if view.saved}<p>月額上限を保存しました。</p>{/if}
    </div>
    <ActionGroup spacing="before"
      >{#if view.uncertain}<Button
          type="button"
          wrap
          disabled={view.busy}
          onclick={() => current?.reconcile()}>保存結果を再確認</Button
        >{/if}<Button
        type="button"
        wrap
        disabled={view.busy}
        onclick={() => current?.load(owner)}
        >{view.uncertain || view.error === 'conflict'
          ? '最新の上限を採用（入力を破棄）'
          : '月額上限を再読み込み'}</Button
      ></ActionGroup
    >
  </section>
</LibraryPanel>

<style>
  section {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  input[type='text'] {
    display: block;
    width: 100%;
    max-width: 28rem;
    box-sizing: border-box;
  }
  .confirmation {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-top: 16px;
  }
  .confirmation input {
    flex: none;
  }
</style>
