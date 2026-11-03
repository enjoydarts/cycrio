<script lang="ts">
  import { subscriptionTitle } from './subscription-title.ts';
  import BrandIcon from './BrandIcon.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import DataTable from './DataTable.svelte';
  import Pagination from './Pagination.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { ApiError } from './api.ts';
  import type { AnalysisResult } from './analysis-api.ts';
  import type { CandidateSource } from './candidate-selection.ts';
  import {
    createSubscriptionsAPI,
    type SourceInput,
    type Subscription,
    type SubscriptionPage,
  } from './subscriptions-api.ts';
  import { createSubscriptionList } from './subscriptions-controller.ts';
  import type { FeedView } from './daily-controller.ts';
  import { money } from './daily-text.ts';
  import { processingErrors } from './mail-processing-text.ts';
  let {
    result,
    index,
    additionalSources = [],
    onSaved,
    onBackToResults,
    onBusyChange = () => {},
  }: {
    result: Pick<AnalysisResult, 'id'>;
    index: number;
    additionalSources?: CandidateSource[];
    onSaved: (s: Subscription) => void;
    onBackToResults: () => void;
    onBusyChange?: (busy: boolean) => void;
  } = $props();
  const session = getAppSession(),
    token = session.token,
    api = createSubscriptionsAPI(PUBLIC_API_BASE_URL);
  let controller: ReturnType<typeof createSubscriptionList> | undefined,
    abort: AbortController | undefined,
    disposed = false;
  let view = $state<FeedView<SubscriptionPage>>({
      page: null,
      status: 'idle',
      error: null,
    }),
    query = $state(''),
    searched = $state(''),
    offset = $state(0),
    selected = $state<Subscription | null>(null),
    latest = $state<Subscription | null>(null),
    confirmed = $state(false),
    saving = $state(false),
    uncertain = $state(false),
    error = $state<string | null>(null),
    conflict = $state(false);
  let pending: SourceInput | null = null;
  const frozen = $derived(saving || uncertain);
  const filter = () => ({
    q: searched,
    status: 'all' as const,
    sort: 'service' as const,
    limit: 20,
    offset,
  });
  function load(next = 0) {
    offset = next;
    void controller?.load(filter());
  }
  onMount(() => {
    controller = createSubscriptionList(
      api,
      token,
      (v) => (view = v),
      session.onDenied,
    );
    load();
    return () => {
      disposed = true;
      controller?.dispose();
      abort?.abort();
      onBusyChange(false);
    };
  });
  async function attach() {
    if (!selected || (!confirmed && !uncertain) || saving || conflict) return;
    const s = selected;
    saving = true;
    onBusyChange(true);
    error = null;
    abort = new AbortController();
    pending ??= {
      expected_version: s.version,
      source_result_id: result.id,
      source_event_index: index,
      ...(additionalSources.length
        ? {
            additional_sources: additionalSources.map((source) => ({
              ...source,
            })),
          }
        : {}),
      confirmed: true,
    };
    try {
      const saved = await api.attach(s.id, pending, token, abort.signal);
      if (!disposed) onSaved(saved);
    } catch (e) {
      if (disposed) return;
      const code = e instanceof ApiError ? e.code : 'dependency_unavailable';
      uncertain = code === 'dependency_unavailable';
      if (!uncertain) pending = null;
      error = processingErrors[code];
      confirmed = false;
      if (code === 'authentication_required' || code === 'access_denied') {
        selected = null;
        session.onDenied();
      } else if (code === 'conflict') {
        conflict = true;
        try {
          const current = await api.get(s.id, token, abort.signal);
          if (disposed) return;
          latest = current;
          if (
            [
              { result_id: result.id, event_index: index },
              ...additionalSources,
            ].every((required) =>
              current.sources.some(
                (source) =>
                  source.result_id === required.result_id &&
                  source.event_index === required.event_index,
              ),
            )
          ) {
            onSaved(current);
          }
        } catch (readError) {
          if (disposed) return;
          latest = null;
          if (
            readError instanceof ApiError &&
            (readError.code === 'authentication_required' ||
              readError.code === 'access_denied')
          ) {
            selected = null;
            error = processingErrors[readError.code];
            session.onDenied();
          }
        }
      }
    } finally {
      if (!disposed) {
        saving = false;
        onBusyChange(uncertain);
      }
    }
  }
</script>

<section class="daily attach" aria-label="既存のサブスクへ紐付ける">
  <h3>既存のサブスクへ紐付ける</h3>
  <p class="muted">
    契約内容は変更しません。サービス名による自動統合は行いません。
  </p>
  <LibraryPanel>
    <form
      class="library-toolbar attach-search"
      onsubmit={(e) => {
        e.preventDefault();
        searched = query;
        selected = null;
        confirmed = false;
        conflict = false;
        load();
      }}
    >
      <label class="attach-query"
        >サブスクを検索<input
          type="search"
          disabled={frozen}
          bind:value={query}
        /></label
      ><Button wrap type="submit" disabled={frozen}>検索</Button>
    </form>
    {#if view.status === 'loading'}<p class="library-content" role="status">
        検索中…
      </p>{:else if view.error}<div class="library-content">
        <p role="alert">{processingErrors[view.error]}</p>
        <Button type="button" wrap onclick={() => load(offset)}
          >再読み込み</Button
        >
      </div>{:else if view.page}<DataTable responsive="cards" density="list">
        <thead><tr><th>紐付け先のサブスク</th><th>金額・状態</th></tr></thead>
        <tbody>
          {#each view.page.subscriptions as s}<tr>
              <td data-primary
                ><label class="attach-choice"
                  ><input
                    type="radio"
                    name="attach-subscription"
                    checked={selected?.id === s.id}
                    disabled={frozen}
                    onchange={() => {
                      selected = s;
                      confirmed = false;
                      conflict = false;
                      latest = null;
                      error = null;
                    }}
                  /><BrandIcon name={s.service} size="small" /><span
                    class="attach-name"
                    ><strong>{subscriptionTitle(s)}</strong
                    >{#if s.display_name}<span class="secondary"
                        >{s.service}</span
                      >{/if}<span class="secondary"
                      >{s.plan ?? 'プラン不明'}</span
                    ></span
                  ></label
                ></td
              ><td data-label="金額・状態"
                ><span class="money">{money(s.amount, s.currency)}</span><span
                  class="secondary"
                  >{s.status === 'active' ? '利用中' : '終了済み'}</span
                ></td
              >
            </tr>{:else}<tr
              ><td colspan="2">一致するサブスクがありません。</td></tr
            >{/each}
        </tbody>
      </DataTable>
      <ActionGroup align="between" class="library-footer">
        <span>全 {view.page.total} 件</span>
        <Pagination
          total={view.page.total}
          offset={view.page.offset}
          limit={view.page.limit}
          disabled={frozen}
          label="紐付け先サブスクのページ"
          onchange={load}
        />
      </ActionGroup>{/if}
  </LibraryPanel>
  <div class="attach-confirmation">
    {#if conflict}<div class="notice" role="alert">
        <p>
          別の操作で契約または紐付けが更新されました。選択を残しています。最新の契約を確認してください。
        </p>
        {#if latest}<p>最新の名前: {subscriptionTitle(latest)}</p>
          <Button
            wrap
            type="button"
            disabled={saving}
            onclick={() => {
              selected = latest;
              latest = null;
              conflict = false;
              confirmed = false;
            }}>最新の契約を選択し直す</Button
          >{/if}
        <ActionGroup class={['actions', 'part-AnalysisAttach-actions']}>
          <Button wrap type="button" disabled={saving} onclick={onBackToResults}
            >解析結果の一覧に戻って登録状況を更新</Button
          >
        </ActionGroup>
      </div>{/if}
    {#if selected}<p class="notice">
        「{subscriptionTitle(selected)}」へ AI イベント {index + 1} を紐付けます。契約の金額・日付・状態は変更しません。
      </p>{/if}
    <label class="attach-confirm"
      ><input
        type="checkbox"
        checked={confirmed}
        disabled={!selected || frozen || conflict}
        onchange={(e) => (confirmed = e.currentTarget.checked)}
      /><span>紐付け先と元の抽出情報を確認しました</span></label
    >
    {#if error}<p class="notice error" role="alert">{error}</p>{/if}<Button
      wrap
      variant="primary"
      class={['primary']}
      type="button"
      disabled={!selected || (!confirmed && !uncertain) || saving || conflict}
      onclick={attach}
      >{uncertain
        ? '同じ要求で紐付けを確認'
        : '確認したサブスクへ紐付ける'}</Button
    >
  </div>
</section>

<style>
  .attach {
    min-width: 0;
    margin-top: 24px;
  }
  .attach h3 {
    margin: 0 0 8px;
    font-size: 16px;
  }
  .attach > .muted {
    margin-bottom: 16px;
  }
  .attach .attach-query {
    flex: 1 1 180px;
    max-width: 440px;
  }
  .attach .attach-choice,
  .attach .attach-confirm {
    display: flex;
    flex-direction: row;
    align-items: flex-start;
    gap: 10px;
    min-width: 0;
    padding-block: 8px;
    cursor: pointer;
  }
  .attach .attach-choice input[type='radio'],
  .attach .attach-confirm input[type='checkbox'] {
    flex: 0 0 20px;
    width: 20px;
    height: 20px;
    min-height: 20px;
    margin: 3px 0 0;
    padding: 0;
    accent-color: var(--color-action);
  }
  .attach-name {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .attach-confirmation {
    min-width: 0;
    margin-top: 20px;
  }
  .attach .attach-confirmation .notice {
    margin: 0 0 12px;
    padding: 12px 16px;
  }
  .attach .attach-confirm {
    margin-bottom: 12px;
    line-height: 1.7;
  }
</style>
