<script lang="ts">
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api';
  import type { CatalogAPI } from './catalog-api';
  import type { AIRuntimeAPI } from './airuntime-api';
  import {
    canActivate,
    createRuntimeController,
    emptyRuntimeView,
    outcomeText,
    pendingTest,
    reasonText,
    runtimeCurrent,
    runtimeErrors,
    testStateNames,
    type RuntimeView,
  } from './ai-runtime-controller';
  import { datetime, providerNames } from './model-catalog';
  import RuntimeProfile from './RuntimeProfile.svelte';
  import './catalog.css';
  import './ai-runtime.css';
  let {
    api,
    catalog,
    token,
    onDenied,
  }: {
    api: AIRuntimeAPI;
    catalog: CatalogAPI;
    token: TokenProvider;
    onDenied: () => void;
  } = $props();
  let view = $state<RuntimeView>(emptyRuntimeView());
  let controller: ReturnType<typeof createRuntimeController> | undefined;
  let confirmed = $state(false);
  let busy = $derived(
    ['idle', 'loading', 'saving', 'denied', 'disposed'].includes(view.status),
  );
  let pending = $derived(pendingTest(view.runtime?.latest_test));
  let current = $derived(runtimeCurrent(view));
  let locked = $derived(busy || pending || view.requestUncertain);
  onMount(() => {
    controller = createRuntimeController(
      api,
      catalog,
      token,
      (next) => {
        view = next;
      },
      onDenied,
    );
    void controller.load();
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      if (disposed) return;
      if (pendingTest(view.runtime?.latest_test)) await controller?.poll();
      if (!disposed) timer = setTimeout(poll, 2000);
    }
    timer = setTimeout(poll, 2000);
    return () => {
      disposed = true;
      clearTimeout(timer);
      controller?.dispose();
    };
  });
  $effect(() => {
    if (!current && !view.requestUncertain) confirmed = false;
  });
</script>

<section
  class="catalog-ui runtime-ui"
  aria-labelledby="runtime-heading"
  aria-busy={busy}
>
  <div class="catalog-title">
    <h3 id="runtime-heading">実行・接続</h3>
    <Button
      wrap
      disabled={busy}
      onclick={() => {
        confirmed = false;
        void controller?.refresh();
      }}>実行状態を再読み込み</Button
    >
  </div>
  <p class="muted">
    モデル選択の保存、実行設定の保存、課金を伴う接続テスト、有効化の順に進めます。実行設定の保存だけでは通信しません。
  </p>
  <div class="feedback" aria-live="polite" aria-atomic="true">
    {#if busy}<p>読み込み・保存中です。</p>{/if}{#if view.error}<p
        class="error"
        role="alert"
      >
        {runtimeErrors[view.error]}
      </p>{/if}{#if view.success}<p class="success">{view.success}</p>{/if}
  </div>
  {#if view.runtime}
    <div class="runtime-status box">
      <h4>現在の実行状態</h4>
      <p>
        {view.runtime.active
          ? view.runtime.active_ready
            ? '有効・実行可能'
            : '有効版を利用できません'
          : '停止中'}
      </p>
      {#if view.runtime.active && !view.runtime.active_ready}<p class="error">
          {reasonText(view.runtime.active_blocked_reason)}
        </p>{/if}<Button
        wrap
        disabled={busy || !view.runtime.active}
        onclick={() => controller?.deactivate()}>実行を停止</Button
      >
    </div>
    <div class="selection-summary">
      <RuntimeProfile
        profile={view.runtime.active}
        title="現在の有効版"
      /><RuntimeProfile
        profile={view.runtime.draft}
        title="保存済みの実行下書き"
      />
    </div>
  {/if}
  <h3>実行設定を保存</h3>
  <p>
    対象は「モデル選択」で保存済みのモデルです。価格はモデル選択の保存時、API
    キーは実行設定の保存時の版に固定します。価格の更新を反映するには「モデル選択」を再保存し、続けて実行設定を再保存してください。
  </p>
  {#if view.settings?.id && view.form}
    <p>
      一次：{view.primary?.display_name ?? '未選択'} / 二次：{view.secondary
        ?.display_name ?? '未選択'}
      {view.secondary ? `（${providerNames[view.secondary.provider]}）` : ''}
    </p>
    {#if view.runtime?.draft && view.settings.id !== view.runtime.draft.catalog_settings_id}<p
        class="error"
        role="status"
      >
        保存済みのモデル選択が変更されています。実行設定を再保存してから接続テストを行ってください。
      </p>{/if}
    {#if view.form.baseline_version !== view.runtime?.version}<p class="error">
        編集中の基準版が古くなっています。入力を確認し、最新の保存内容に戻す場合は「入力を破棄して読み直す」を選んでください。
      </p>{/if}
    <form
      onsubmit={(event) => {
        event.preventDefault();
        confirmed = false;
        void controller?.save();
      }}
    >
      <div class="runtime-fields">
        <div>
          <label for="runtime-input"
            >旧方式・接続テストの入力基準（bytes）</label
          ><input
            id="runtime-input"
            type="number"
            min="1024"
            max="65536"
            step="1"
            required
            disabled={locked}
            value={view.form.values.max_input_bytes}
            oninput={(event) =>
              controller?.edit({
                max_input_bytes: Number(event.currentTarget.value),
              })}
          />
        </div>
        <div>
          <label for="runtime-output">出力上限（token）</label><input
            id="runtime-output"
            type="number"
            min="64"
            max="16384"
            step="1"
            required
            disabled={locked}
            value={view.form.values.max_output_tokens}
            oninput={(event) =>
              controller?.edit({
                max_output_tokens: Number(event.currentTarget.value),
              })}
          />
        </div>
        <div>
          <label for="runtime-timeout">制限時間（秒）</label><input
            id="runtime-timeout"
            type="number"
            min="1"
            max="120"
            step="1"
            required
            disabled={locked}
            value={view.form.values.timeout_seconds}
            oninput={(event) =>
              controller?.edit({
                timeout_seconds: Number(event.currentTarget.value),
              })}
          />
        </div>
      </div>
      {#if view.secondary?.provider === 'openrouter'}<label
          for="runtime-provider">OpenRouter の承認配信先 slug</label
        ><input
          id="runtime-provider"
          required
          maxlength="128"
          pattern={'[a-z0-9][a-z0-9._\\-]*(/[a-z0-9][a-z0-9._\\-]*)?'}
          disabled={locked}
          value={view.form.values.openrouter_provider}
          oninput={(event) =>
            controller?.edit({
              openrouter_provider: event.currentTarget.value,
            })}
          placeholder="provider または provider/variant"
        />
        <p class="muted">
          OpenRouter が処理内容を送る配信先です。管理者が承認した小文字の slug
          を指定します。この配信先のみ・指定順・フォールバック禁止・データ収集拒否・ZDR
          必須のポリシーで固定します。
        </p>{/if}
      <ActionGroup class={['actions', 'part-AIRuntime-actions']}>
        <Button
          wrap
          variant="primary"
          type="submit"
          class={['primary']}
          disabled={locked}>実行設定を保存</Button
        ><Button
          wrap
          type="button"
          disabled={busy}
          onclick={() => {
            confirmed = false;
            void controller?.load();
          }}>入力を破棄して読み直す</Button
        >
      </ActionGroup>
    </form>
  {:else if !busy}<p class="muted">
      先に「モデル選択」で一次判定と二次処理の下書きを保存してください。
    </p>{/if}
  <h3>接続テスト</h3>
  {#if view.runtime?.latest_test}<div class="box" aria-live="polite">
      <strong
        >最新テスト：{testStateNames[view.runtime.latest_test.state]}</strong
      >
      <p>
        一次判定 {outcomeText(view.runtime.latest_test.primary_outcome)} / 二次処理
        {outcomeText(view.runtime.latest_test.secondary_outcome)}
      </p>
      <p>
        作成 {datetime(view.runtime.latest_test.created_at)} JST · 完了 {datetime(
          view.runtime.latest_test.finished_at,
        )} JST
      </p>
      {#if view.runtime.latest_test.extracted_events !== null}<p>
          抽出イベント {view.runtime.latest_test.extracted_events} 件
        </p>{/if}{#if pending}<p>
          受付済みです。結果を自動確認しています。再送信しません。
        </p>
        <Button wrap disabled={busy} onclick={() => controller?.poll()}
          >テスト結果を再確認</Button
        >{/if}
    </div>{/if}
  {#if view.requestUncertain}<div class="box">
      <p>
        前のテスト要求の受付結果が不明です。同じ要求 ID
        で確認します。新しいテストは開始しません。
      </p>
      <Button wrap disabled={busy} onclick={() => controller?.requestTest(true)}
        >同じ要求 ID で受付を再確認</Button
      >
    </div>
  {:else}<div class="box">
      <h4>課金の確認</h4>
      <p>
        サーバー固定の合成メールを使い、一次判定と二次処理を最大 2
        回呼び出します。課金が発生します。実メールは送信しません。
      </p>
      <p>
        対象：{view.runtime?.draft?.primary.display_name ?? '未設定'} → {view
          .runtime?.draft?.secondary.display_name ?? '未設定'}
      </p>
      <p class="muted">
        {!view.runtime?.draft?.primary.price ||
        !view.runtime?.draft?.secondary.price
          ? '価格が不明なため試算できません。'
          : '保存時の参考価格は上記の実行下書きを確認してください。実際の入出力数によって料金が変わるため、合計額は未確定です。'}
      </p>
      <label class="confirmation"
        ><input
          type="checkbox"
          bind:checked={confirmed}
          disabled={busy || pending || !current}
        />課金を確認し、{view.runtime?.latest_test
          ? '追加の'
          : 'この'}接続テストを承認します</label
      ><Button
        wrap
        variant="primary"
        class={['primary']}
        disabled={busy || pending || !current || !confirmed}
        onclick={async () => {
          await controller?.requestTest(true, true);
          confirmed = false;
        }}>課金を確認して接続テスト</Button
      >{#if view.form?.dirty}<p class="muted">
          編集中の実行設定を保存してから、接続テストを行ってください。
        </p>{/if}
    </div>{/if}
  <h3>有効化</h3>
  <p class="muted">
    現在の実行下書きに一致する成功テストだけを有効化できます。有効化時にモデル・API
    キー・価格の版を再確認します。
  </p>
  <Button
    wrap
    variant="primary"
    class={['primary']}
    disabled={busy || !canActivate(view)}
    onclick={() => controller?.activate()}>テスト成功版を有効化</Button
  >
</section>
