<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api';
  import { modelProviders, type ModelProvider } from './catalog-api';
  import {
    scopes,
    stages,
    type AIRuntimeAPI,
    type CostFilter,
  } from './airuntime-api';
  import {
    costSource,
    costText,
    createUsageController,
    emptyUsageView,
    outcomeText,
    reasonText,
    runtimeErrors,
    scopeNames,
    stageNames,
    usageText,
    type UsageView,
  } from './ai-runtime-controller';
  import { datetime, providerNames } from './model-catalog';
  import CostTotals from './CostTotals.svelte';
  import UsageAmounts from './UsageAmounts.svelte';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import './catalog.css';
  import './ai-runtime.css';
  let {
    api,
    token,
    onDenied,
  }: { api: AIRuntimeAPI; token: TokenProvider; onDenied: () => void } =
    $props();
  let view = $state<UsageView>(emptyUsageView());
  let controller: ReturnType<typeof createUsageController> | undefined;
  let purpose = $state<'overview' | 'breakdown' | 'calls'>('overview');
  let busy = $derived(
    ['idle', 'loading', 'denied', 'disposed'].includes(view.status) ||
      Boolean(view.reconciling),
  );
  let models = $derived(
    Array.from(
      new Map(
        (view.report?.breakdown ?? []).map((row) => [row.model_id, row]),
      ).values(),
    ),
  );
  onMount(() => {
    controller = createUsageController(
      api,
      token,
      (next) => {
        view = next;
      },
      onDenied,
    );
    void controller.load();
    return () => controller?.dispose();
  });
  function filter(patch: Partial<CostFilter>) {
    void controller?.filter(patch);
  }
</script>

<section class="library-page" aria-labelledby="usage-heading" aria-busy={busy}>
  <LibraryHeader
    title="AI 利用原価"
    id="usage-heading"
    eyebrow="SETTINGS"
    description="月別・使用ユーザー別に、保存された実利用の原価を確認できます。"
  >
    {#snippet action()}
      <button disabled={busy} onclick={() => controller?.load()}
        >原価を再読み込み</button
      >
    {/snippet}
  </LibraryHeader>
  <LibraryPanel>
    <div class="catalog-ui usage-ui usage-body">
      <nav class="library-tabs" aria-label="原価の表示">
        {#each [['overview', '概要・ユーザー別'], ['breakdown', '詳細内訳'], ['calls', '呼出し履歴']] as [value, label]}<button
            type="button"
            disabled={busy}
            aria-pressed={purpose === value}
            onclick={() => (purpose = value as typeof purpose)}>{label}</button
          >{/each}
      </nav>
      <div class="usage-filters library-toolbar">
        <div>
          <label for="usage-month">集計月（Asia/Tokyo）</label><input
            id="usage-month"
            type="month"
            min="2000-01"
            max="2199-12"
            required
            value={view.filter.month}
            disabled={Boolean(view.reconciling)}
            onchange={(event) => {
              if (event.currentTarget.validity.valid)
                filter({ month: event.currentTarget.value });
            }}
          />
        </div>
        <details class="advanced-filters">
          <summary
            >詳細条件{#if view.filter.provider || view.filter.scope || view.filter.stage || view.filter.model_id}
              · 適用中{/if}</summary
          >
          <div class="usage-filters">
            <div>
              <label for="usage-provider">プロバイダー</label><select
                id="usage-provider"
                value={view.filter.provider ?? ''}
                disabled={Boolean(view.reconciling)}
                onchange={(event) =>
                  filter({
                    provider: event.currentTarget.value as ModelProvider | '',
                    model_id: '',
                  })}
                ><option value="">すべて</option
                >{#each modelProviders as provider}<option value={provider}
                    >{providerNames[provider]}</option
                  >{/each}</select
              >
            </div>
            <div>
              <label for="usage-scope">スコープ</label><select
                id="usage-scope"
                value={view.filter.scope ?? ''}
                disabled={Boolean(view.reconciling)}
                onchange={(event) =>
                  filter({
                    scope: event.currentTarget.value as CostFilter['scope'],
                  })}
                ><option value="">すべて</option>{#each scopes as scope}<option
                    value={scope}>{scopeNames[scope]}</option
                  >{/each}</select
              >
            </div>
            <div>
              <label for="usage-stage">処理段階</label><select
                id="usage-stage"
                value={view.filter.stage ?? ''}
                disabled={Boolean(view.reconciling)}
                onchange={(event) =>
                  filter({
                    stage: event.currentTarget.value as CostFilter['stage'],
                  })}
                ><option value="">すべて</option>{#each stages as stage}<option
                    value={stage}>{stageNames[stage]}</option
                  >{/each}</select
              >
            </div>
            <div>
              <label for="usage-model">モデル</label><select
                id="usage-model"
                value={view.filter.model_id ?? ''}
                disabled={Boolean(view.reconciling)}
                onchange={(event) =>
                  filter({ model_id: event.currentTarget.value })}
                ><option value="">すべて</option>{#each models as model}<option
                    value={model.model_id}>{model.model_name}</option
                  >{/each}</select
              >
            </div>
          </div>
        </details>
      </div>
      {#if view.filter.user_id}<p>
          選択中の消費利用者：<code>{view.filter.user_id}</code>
        </p>
        <button disabled={busy} onclick={() => filter({ user_id: '' })}
          >利用者の絞り込みを解除</button
        >{/if}
      <div
        class="feedback"
        class:has-feedback={busy || !!view.error}
        aria-live="polite"
        aria-atomic="true"
      >
        {#if busy}<p>
            {view.reconciling
              ? '固定の生成 ID で原価を再照合しています。推論は行いません。'
              : '集計を読み込んでいます。'}
          </p>{/if}{#if view.error}<p class="error" role="alert">
            {runtimeErrors[view.error]}
          </p>{/if}
      </div>
      <section
        class="library-content"
        aria-label="原価の概要"
        hidden={purpose !== 'overview'}
      >
        {#if view.report}
          <div class="usage-summary">
            <h4>{view.report.month} の既知原価</h4>
            <p>
              {view.report.provisional
                ? '暫定月（後から更新される可能性があります）'
                : '月次集計'} · 更新 {datetime(view.report.updated_at)} JST
            </p>
            <details>
              <summary>集計の対象・計算方法</summary>
              <p class="muted">
                対象期間：{datetime(view.report.from)} 以上 / {datetime(
                  view.report.to,
                )} 未満（JST）。通貨ごとに表示し、不明額は合計に含めません。推定・報告は既知額の内訳です。
              </p>
              <p>
                原価は採用済みの最新記録です。配信先報告への置換後は推定額を重ねて加算しません。
              </p>
            </details>
            <UsageAmounts totals={view.report.totals} />
          </div>
          <h2>使用ユーザー別</h2>
          <p class="muted">
            {view.report.users_total} 人 · 呼出し 0 件の利用者も表示します。利用者を選ぶと、その利用者が消費した呼出しに絞り込みます。
          </p>
          <div class="usage-table">
            <table>
              <caption class="sr-only">使用ユーザー別の月次原価</caption><thead
                ><tr
                  ><th scope="col">消費利用者</th><th scope="col"
                    >呼出し / 不明 / 失敗</th
                  ><th scope="col">通貨別原価</th><th scope="col">操作</th></tr
                ></thead
              ><tbody
                >{#each view.report.users as user}<tr
                    class:active-row={view.filter.user_id === user.user_id}
                    ><td data-label="消費利用者"><code>{user.user_id}</code></td
                    ><td data-label="呼出し / 不明 / 失敗"
                      >{user.calls} 件 / {user.unknown_calls} 件 / {user.failures}
                      件</td
                    ><td data-label="通貨別原価"
                      ><UsageAmounts totals={user.totals} counts={false} /></td
                    ><td data-label="操作"
                      ><button
                        disabled={busy}
                        aria-label={`${user.user_id} の使用原価を表示`}
                        onclick={() => filter({ user_id: user.user_id })}
                        >この利用者を表示</button
                      ></td
                    ></tr
                  >{:else}<tr><td colspan="4">対象の利用者はいません。</td></tr
                  >{/each}</tbody
              >
            </table>
          </div>
          <div class="actions pagination">
            <button
              disabled={busy || view.usersOffset === 0}
              onclick={() =>
                controller?.usersPage(Math.max(0, view.usersOffset - 50))}
              >利用者の前の50件</button
            ><button
              disabled={busy ||
                view.usersOffset + 50 >= view.report.users_total}
              onclick={() => controller?.usersPage(view.usersOffset + 50)}
              >利用者の次の50件</button
            >
          </div>
        {/if}
      </section>
      <section
        class="library-content"
        aria-label="詳細内訳"
        hidden={purpose !== 'breakdown'}
      >
        {#if view.report}
          <h2>管理者テスト・システム処理</h2>
          <div class="selection-summary">
            {#each view.report.administration as item}<article class="box">
                <h4>{scopeNames[item.scope]}</h4>
                <CostTotals totals={item.totals} />
              </article>{:else}<p class="muted">
                対象の呼出しはありません。
              </p>{/each}
          </div>
          <h3>プロバイダー・モデル・処理段階の内訳</h3>
          <div class="usage-table">
            <table>
              <caption class="sr-only">選択した条件での原価内訳</caption><thead
                ><tr
                  ><th scope="col">プロバイダー / モデル</th><th scope="col"
                    >処理段階</th
                  ><th scope="col">通貨別原価</th></tr
                ></thead
              ><tbody
                >{#each view.report.breakdown as row}<tr
                    ><td data-label="プロバイダー / モデル"
                      ><strong>{row.model_name}</strong><span
                        >{providerNames[row.provider]}</span
                      ></td
                    ><td data-label="処理段階">{stageNames[row.stage]}</td><td
                      data-label="通貨別原価"
                      ><CostTotals totals={row.totals} /></td
                    ></tr
                  >{:else}<tr><td colspan="3">呼出し 0 件</td></tr
                  >{/each}</tbody
              >
            </table>
          </div>
        {/if}
      </section>
      <section
        class="library-content"
        aria-label="呼出し履歴"
        hidden={purpose !== 'calls'}
      >
        <h2>呼出し履歴</h2>
        <p class="muted">
          失敗した呼出しでも課金される場合があります。不明原価は 0
          として扱いません。再照合は既存の OpenRouter 生成 ID
          の料金を取得し直す操作です。
        </p>
        {#if view.calls}<p>
            {view.calls.total} 件 · {view.calls.total
              ? view.callsOffset + 1
              : 0} 件目から最大 50 件
          </p>
          <div class="usage-table calls-table">
            <table>
              <caption class="sr-only">AI 呼出し履歴と採用原価</caption><thead
                ><tr
                  ><th scope="col">開始（JST）/ スコープ</th><th scope="col"
                    >消費利用者 / 操作者</th
                  ><th scope="col">モデル</th><th scope="col"
                    >処理段階 / 結果</th
                  ><th scope="col">使用量</th><th scope="col"
                    >採用原価 / 出典</th
                  ><th scope="col">確認</th></tr
                ></thead
              ><tbody
                >{#each view.calls.calls as row}<tr
                    ><td data-label="開始（JST）/ スコープ"
                      >{datetime(row.started_at)}<span
                        >{scopeNames[row.scope]}</span
                      ></td
                    ><td data-label="消費利用者 / 操作者"
                      ><span
                        >消費：<code
                          >{row.consuming_user_id ?? '利用者に割当なし'}</code
                        ></span
                      ><span
                        >操作：<code
                          >{row.initiated_by_user_id ?? 'システム'}</code
                        ></span
                      ></td
                    ><td data-label="モデル"
                      ><strong>{row.model_name}</strong><span
                        >{providerNames[row.provider]}</span
                      >
                      <details>
                        <summary>呼出しの技術情報</summary><code
                          >{row.requested_model_id}</code
                        >{#if row.actual_model_id}<span
                            >実モデル：<code>{row.actual_model_id}</code></span
                          >{/if}{#if row.actual_provider}<span
                            >配信先：{row.actual_provider}</span
                          >{/if}
                      </details></td
                    ><td data-label="処理段階 / 結果"
                      >{stageNames[row.stage]}<span
                        >{{
                          calling: '呼出し中',
                          succeeded: '成功',
                          failed: '失敗',
                          unknown: '結果不明',
                        }[row.status]}</span
                      ><span>{outcomeText(row.outcome)}</span><span
                        >試行 {row.attempt_no}</span
                      ></td
                    ><td data-label="使用量"
                      >{usageText(
                        row.usage,
                      )}{#if row.usage.valid && row.usage.present}<details>
                          <summary>使用量の内訳</summary>
                          <p>
                            通常入力 {row.usage.ordinary_input_tokens} / キャッシュ読取
                            {row.usage.cache_read_tokens} / 保存 {row.usage
                              .cache_write_tokens} / 推論 {row.usage
                              .reasoning_tokens}
                          </p>
                          <p>
                            保存期間別 5分 {row.usage.cache_write_5m_tokens} / 30分
                            {row.usage.cache_write_30m_tokens} / 1時間 {row
                              .usage.cache_write_1h_tokens} / 12時間 {row.usage
                              .cache_write_12h_tokens}
                          </p>
                        </details>{/if}</td
                    ><td data-label="採用原価 / 出典"
                      >{costText(row.cost)}<span>{costSource(row.cost)}</span
                      >{#if row.cost.status === 'unknown'}<span class="muted"
                          >{reasonText(row.cost.reason)}</span
                        >{/if}</td
                    ><td data-label="確認"
                      ><button
                        disabled={busy}
                        aria-label={`${row.model_name} の原価履歴`}
                        onclick={() => controller?.revisions(row.id)}
                        >原価履歴</button
                      >{#if row.provider === 'openrouter' && row.generation_id}<button
                          disabled={busy}
                          aria-label={`${row.model_name} の原価を再照合`}
                          onclick={() => controller?.reconcile(row)}
                          >原価を再照合</button
                        >{/if}</td
                    ></tr
                  >{:else}<tr><td colspan="7">呼出し 0 件</td></tr
                  >{/each}</tbody
              >
            </table>
          </div>
          <div class="actions pagination">
            <button
              disabled={busy || view.callsOffset === 0}
              onclick={() =>
                controller?.callsPage(Math.max(0, view.callsOffset - 50))}
              >呼出しの前の50件</button
            ><button
              disabled={busy || view.callsOffset + 50 >= view.calls.total}
              onclick={() => controller?.callsPage(view.callsOffset + 50)}
              >呼出しの次の50件</button
            >
          </div>{/if}
        {#if view.revisionCallID}<section class="box" aria-label="原価履歴">
            <h4>使用量・原価の版履歴（最新 50 件）</h4>
            <details>
              <summary>呼出しの識別情報</summary>
              <p class="muted">
                呼出し ID：<code>{view.revisionCallID}</code
                >。月次合計では、この履歴を足し合わせません。
              </p>
            </details>
            <ul>
              {#each view.revisions as revision}<li class="box">
                  <strong>{datetime(revision.captured_at)} JST</strong>
                  <p>{costText(revision.cost)} · {costSource(revision.cost)}</p>
                  <p>{usageText(revision.usage)}</p>
                  {#if revision.cost.status === 'unknown'}<p>
                      {reasonText(revision.cost.reason)}
                    </p>{/if}
                </li>{:else}<li>履歴はまだありません。</li>{/each}
            </ul>
          </section>{/if}
      </section>
    </div>
  </LibraryPanel>
</section>

<style>
  [hidden] {
    display: none !important;
  }
  .usage-body {
    margin-top: 0;
  }
  .usage-body .feedback {
    min-height: 0;
  }
  .usage-body .has-feedback {
    padding: 1rem 22px;
  }
  .usage-body .usage-table {
    border: 0;
    border-radius: 0;
  }
  .usage-summary {
    margin-bottom: 1.5rem;
    border-bottom: 1px solid var(--color-border);
    padding-bottom: 1rem;
  }
  .advanced-filters {
    align-self: end;
  }
  .advanced-filters > .usage-filters {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  @media (max-width: 600px) {
    .advanced-filters > .usage-filters {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
