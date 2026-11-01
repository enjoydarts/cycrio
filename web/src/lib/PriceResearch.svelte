<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api.ts';
  import type { CatalogAPI } from './catalog-api.ts';
  import {
    createPriceResearchAPI,
    type PriceResearchAPI,
    type ResearchProvider,
    type ResearchTarget,
  } from './price-research-api.ts';
  import {
    createPriceResearchController,
    emptyPriceResearchView,
    researchPending,
  } from './price-research-controller.ts';
  import { providerNames, datetime } from './model-catalog.ts';
  import { runtimeErrors } from './ai-runtime-controller.ts';
  import Button from './Button.svelte';
  import ActionLink from './ActionLink.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import Pagination from './Pagination.svelte';
  let {
    catalog,
    token,
    onDenied,
    onDirty = () => {},
    onBusy = () => {},
    onSync,
    apiBase = '',
    api,
  }: {
    catalog: CatalogAPI;
    token: TokenProvider;
    onDenied: () => void;
    onDirty?: (value: boolean) => void;
    onBusy?: (value: boolean) => void;
    onSync: () => void;
    apiBase?: string;
    api?: PriceResearchAPI;
  } = $props();
  let view = $state(emptyPriceResearchView());
  let controller: ReturnType<typeof createPriceResearchController> | undefined;
  let provider = $state<ResearchProvider>('openai'),
    search = $state(''),
    offset = $state(0),
    target = $state<ResearchTarget>('openai');
  const busy = $derived(
    ['idle', 'loading', 'saving', 'denied', 'disposed'].includes(view.status),
  );
  const pending = $derived(researchPending(view.saved));
  const locked = $derived(busy || view.uncertain);
  const ready = $derived(
    !!view.saved?.ready && !view.dirty && !pending && !locked,
  );
  const runNames = {
    queued: '待機中',
    running: '実行中',
    succeeded: '成功',
    failed: '失敗',
    unknown: '結果不明',
  };
  const errorNames: Record<string, string> = {
    credentials_required: 'API キーが未登録または無効です。',
    provider_unavailable: '提供元に接続できませんでした。',
    research_unavailable: '公式価格を確認できませんでした。',
    invalid_response: '公式価格の根拠を検証できませんでした。',
    search_unverified: 'Web検索の成功と公式の検索結果を確認できませんでした。',
    research_unresolved:
      '検索は実行できましたが、旧テストで要求していた価格を検証できませんでした。Web検索テストを再実行してください。',
    lease_expired: '処理の完了を確認できません。',
    configuration_changed: '設定が変更されたため、価格の適用を中止しました。',
  };
  $effect(() => onDirty(view.dirty || view.uncertain));
  $effect(() => onBusy(view.status === 'saving' || view.uncertain));
  onMount(() => {
    controller = createPriceResearchController(
      api ?? createPriceResearchAPI(apiBase),
      catalog,
      token,
      (next) => {
        view = next;
      },
      onDenied,
    );
    void (async () => {
      await controller?.load();
      if (view.saved?.provider) provider = view.saved.provider;
      await controller?.search(provider, search);
    })();
    const timer = setInterval(() => {
      if (pending && !locked) void controller?.poll();
    }, 3000);
    return () => {
      clearInterval(timer);
      controller?.dispose();
      onDirty(false);
      onBusy(false);
    };
  });
  async function find(event?: SubmitEvent, next = 0) {
    event?.preventDefault();
    offset = next;
    await controller?.search(provider, search, offset);
  }
  async function reload() {
    if (
      view.dirty &&
      !window.confirm('料金調査の未保存の入力を破棄して再読み込みしますか？')
    )
      return;
    await controller?.load(true);
    if (view.saved?.provider) provider = view.saved.provider;
    await find();
  }
  async function changeProvider() {
    controller?.edit({ model_id: null });
    search = '';
    await find();
  }
</script>

<section class="research" aria-labelledby="research-heading" aria-busy={busy}>
  <h2 id="research-heading">料金調査</h2>
  <p>
    OpenAI または Anthropic の
    Web検索で公式料金を確認します。メール解析のモデル設定とは独立した設定です。調査の費用はシステム利用として記録します。
  </p>
  <p class="muted">
    手入力の価格上書きと過去の価格は保持します。確認できない価格は「不明」のままにし、無料として扱いません。
  </p>
  <ActionGroup>
    <ActionLink wrap href="/settings/credentials"
      >料金調査に使う API キーを登録・更新</ActionLink
    >
    <Button wrap type="button" disabled={busy || pending} onclick={onSync}
      >モデル一覧を API 同期する</Button
    >
    <Button wrap type="button" disabled={busy} onclick={reload}
      >料金調査の保存状態を再読み込み</Button
    >
  </ActionGroup>
  <div aria-live="polite" aria-atomic="true">
    {#if busy}<p>料金調査の状態を読み込み・保存中です。</p>{/if}
    {#if view.error}<p class="error" role="alert">
        {view.error === 'invalid_request'
          ? 'モデルを選び、実行間隔を 1〜168 時間で指定してください。'
          : runtimeErrors[view.error]}
      </p>{/if}
    {#if view.success}<p class="success">{view.success}</p>{/if}
    {#if view.uncertain}<p class="error" role="alert">
        送信結果を確認できません。二重実行を防ぐため、保存状態を再読み込みしてから操作してください。
      </p>{/if}
  </div>
  {#if view.draft}
    {@const draft = view.draft}
    {#if view.dirty && draft.expected_version !== view.saved?.version}<p
        class="error"
        role="status"
      >
        別の操作で設定が更新されました。入力と元の保存基準を保持しています。保存状態を再読み込みしてから編集してください。
      </p>{/if}
    <div class="research-grid">
      <section class="box" aria-label="料金調査モデルの選択">
        <h3>1 調査に使うモデル</h3>
        <p>
          登録済み API キーと、API
          同期したモデルを使います。参考価格が未取得のモデルも選べます。Web検索への対応はテストで確認してください。
        </p>
        <label for="research-provider">料金調査モデルの提供元</label>
        <select
          id="research-provider"
          bind:value={provider}
          disabled={locked || pending}
          onchange={changeProvider}
          ><option value="openai">OpenAI</option><option value="anthropic"
            >Anthropic</option
          ></select
        >
        <form onsubmit={find}>
          <label for="research-search">モデル名で検索</label>
          <input
            id="research-search"
            bind:value={search}
            disabled={locked}
            type="search"
          />
          <ActionGroup
            ><Button wrap type="submit" disabled={locked}
              >調査モデルを検索</Button
            ></ActionGroup
          >
        </form>
        <label for="research-model">料金調査モデル</label>
        <select
          id="research-model"
          value={draft.model_id ?? ''}
          disabled={locked || pending}
          onchange={(event) =>
            controller?.edit({ model_id: event.currentTarget.value || null })}
        >
          <option value="">モデルを選択</option>
          {#if draft.model_id && !view.models.some((model) => model.id === draft.model_id)}<option
              value={draft.model_id}>選択済みモデル（検索結果外）</option
            >{/if}
          {#each view.models as model}<option value={model.id}
              >{model.display_name}（{model.model_id}）{model.current_price
                ? ''
                : ' · 料金未取得'}</option
            >{/each}
        </select>
        {#if !view.models.length}<p>
            選択可能なモデルがありません。API
            キーを登録し、モデル一覧を同期してください。
          </p>{/if}
        <Pagination
          total={view.total}
          {offset}
          limit={20}
          disabled={locked}
          label="料金調査モデルのページ"
          onchange={(next) => find(undefined, next)}
        />
        <label
          ><input
            type="checkbox"
            checked={draft.automatic_enabled}
            disabled={locked || pending}
            onchange={(event) =>
              controller?.edit({
                automatic_enabled: event.currentTarget.checked,
              })}
          />定期的に公式価格を更新する</label
        >
        <label for="research-interval">実行間隔（時間）</label>
        <input
          id="research-interval"
          type="number"
          min="1"
          max="168"
          step="1"
          value={draft.interval_hours}
          disabled={locked || pending}
          oninput={(event) =>
            controller?.edit({
              interval_hours: event.currentTarget.valueAsNumber,
            })}
        />
        <p class="muted">
          設定を保存するとテスト結果は無効になります。定期更新は
          Web検索テストの成功まで保留されます。モデル一覧の同期後も、この設定に従って価格を調査します。
        </p>
        <ActionGroup
          ><Button
            wrap
            type="button"
            variant="primary"
            disabled={locked || pending || !draft.model_id}
            onclick={() => controller?.save()}>料金調査の設定を保存</Button
          ></ActionGroup
        >
        <p>
          次回の定期実行：{view.saved?.ready && view.saved.automatic_enabled
            ? datetime(view.saved.next_run_at)
            : '保留・無効'}
        </p>
      </section>
      <section class="box" aria-label="Web検索テストと公式価格の更新">
        <h3>2 Web検索をテスト</h3>
        <p>
          公式ページを実際に検索し、検索機能と公式の検索結果を確認します。価格の抽出・検証は、その後の公式価格の更新で行います。
        </p>
        <Button
          wrap
          type="button"
          disabled={locked || pending || view.dirty || !view.saved?.model_id}
          onclick={() => controller?.test()}>Web検索テストを実行</Button
        >
        <p role="status">
          {view.dirty
            ? '未保存の設定があります。先に保存してください。'
            : view.saved?.ready
              ? 'Web検索テスト確認済み'
              : 'Web検索テストの成功が必要です。公式価格の更新は保留しています。'}
        </p>
        <h3>3 公式価格を更新</h3>
        <label for="research-target">料金を調べる提供元</label>
        <select
          id="research-target"
          bind:value={target}
          disabled={locked || pending}
          ><option value="liquid">Liquid AI（D1）</option><option value="openai"
            >OpenAI</option
          ><option value="anthropic">Anthropic</option></select
        >
        <p>
          利用可能なモデルから最大 20
          件を調査します。新しいモデルも対象になります。
        </p>
        <Button
          wrap
          type="button"
          disabled={!ready}
          onclick={() => controller?.update(target)}
          >選んだ提供元の公式価格を更新</Button
        >
      </section>
    </div>
  {/if}
  {#if view.saved?.latest_run}
    {@const run = view.saved.latest_run}
    <section
      class="box"
      aria-labelledby="research-result-heading"
      aria-live="polite"
    >
      <h3 id="research-result-heading">直近の調査結果</h3>
      <p>
        {run.kind === 'test' ? 'Web検索テスト' : '公式価格の更新'} · {providerNames[
          run.target_provider
        ]} · {runNames[run.state]}
      </p>
      <dl>
        <div>
          <dt>開始 / 完了（日本時間）</dt>
          <dd>{datetime(run.started_at)} / {datetime(run.finished_at)}</dd>
        </div>
        <div>
          <dt>更新 / 未確認</dt>
          <dd>{run.updated_count} 件 / {run.unresolved_count} 件</dd>
        </div>
      </dl>
      {#if pending}<p>
          処理待ちまたは実行中です。完了を自動で確認しています。重複して実行できません。
        </p>{/if}
      {#if run.state === 'unknown'}<p class="error">
          結果不明です。自動で再送しません。利用状況と出典を確認してから再度実行してください。
        </p>{/if}
      {#if run.error_code}<p class="error">
          {errorNames[run.error_code] ??
            '調査を完了できませんでした。保存状態と API キーを確認してください。'}
        </p>{/if}
      <h4>確認した公式の出典</h4>
      {#if run.sources.length}<ul>
          {#each run.sources as source}<li>
              <a href={source} target="_blank" rel="noopener noreferrer"
                >{source}</a
              >
            </li>{/each}
        </ul>{:else}<p>検証済みの出典はありません。</p>{/if}
      <p class="muted">
        未確認のモデルは価格を推測して保存しません。検索の利用料金が未確定の場合、原価の合計も不明として記録します。
      </p>
    </section>
  {/if}
</section>

<style>
  .research {
    min-width: 0;
  }
  .research-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1rem;
  }
  .research-grid .box {
    margin-bottom: 0;
  }
  .research :global(.ui-actions) {
    margin-top: 1rem;
  }
  .research h3 {
    margin-top: 0;
  }
  .research a {
    overflow-wrap: anywhere;
  }
  @media (max-width: 900px) {
    .research-grid {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
