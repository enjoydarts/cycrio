<script lang="ts">
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import { ApiError } from './api.ts';
  import type { AnalysisResult } from './analysis-api.ts';
  import {
    createSubscriptionsAPI,
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
    onSaved,
    onBackToResults,
    onBusyChange = () => {},
  }: {
    result: AnalysisResult;
    index: number;
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
    error = $state<string | null>(null),
    conflict = $state(false);
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
    if (!selected || !confirmed || saving || conflict) return;
    const s = selected;
    saving = true;
    onBusyChange(true);
    error = null;
    abort = new AbortController();
    try {
      const saved = await api.attach(
        s.id,
        {
          expected_version: s.version,
          source_result_id: result.id,
          source_event_index: index,
          confirmed: true,
        },
        token,
        abort.signal,
      );
      if (!disposed) onSaved(saved);
    } catch (e) {
      if (disposed) return;
      const code = e instanceof ApiError ? e.code : 'dependency_unavailable';
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
            current.sources.some(
              (source) =>
                source.result_id === result.id && source.event_index === index,
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
        onBusyChange(false);
      }
    }
  }
</script>

<div class="daily">
  <h3>既存のサブスクへ紐付ける</h3>
  <p class="muted">
    契約内容は変更しません。サービス名による自動統合は行いません。
  </p>
  <form
    class="toolbar"
    onsubmit={(e) => {
      e.preventDefault();
      searched = query;
      selected = null;
      confirmed = false;
      conflict = false;
      load();
    }}
  >
    <label class="search"
      >サブスクを検索<input disabled={saving} bind:value={query} /></label
    ><button disabled={saving}>検索</button>
  </form>
  {#if view.status === 'loading'}<p role="status">
      検索中…
    </p>{:else if view.error}<p role="alert">{processingErrors[view.error]}</p>
    <button onclick={() => load(offset)}>再読み込み</button
    >{:else if view.page}<ul class="compact-list">
      {#each view.page.subscriptions as s}<li>
          <label class="check"
            ><input
              type="radio"
              name="attach-subscription"
              checked={selected?.id === s.id}
              disabled={saving}
              onchange={() => {
                selected = s;
                confirmed = false;
                conflict = false;
                latest = null;
                error = null;
              }}
            /><span
              ><strong>{s.service}</strong><span class="secondary"
                >{s.plan ?? 'プラン不明'} · {money(s.amount, s.currency)} · {s.status ===
                'active'
                  ? '利用中'
                  : '終了済み'}</span
              ></span
            ></label
          >
        </li>{:else}<li>一致するサブスクがありません。</li>{/each}
    </ul>
    <div class="pager">
      <span>全 {view.page.total} 件</span>
      <div class="actions">
        <button
          disabled={!offset || saving}
          onclick={() => load(Math.max(0, offset - 20))}>前へ</button
        ><button
          disabled={offset + 20 >= view.page.total || saving}
          onclick={() => load(offset + 20)}>次へ</button
        >
      </div>
    </div>{/if}
  {#if conflict}<div class="notice" role="alert">
      <p>
        別の操作で契約または紐付けが更新されました。選択を残しています。最新の契約を確認してください。
      </p>
      {#if latest}<p>最新のサービス名: {latest.service}</p>
        <button
          disabled={saving}
          onclick={() => {
            selected = latest;
            latest = null;
            conflict = false;
            confirmed = false;
          }}>最新の契約を選択し直す</button
        >{/if}
      <div class="actions">
        <button type="button" disabled={saving} onclick={onBackToResults}
          >解析結果の一覧に戻って登録状況を更新</button
        >
      </div>
    </div>{/if}
  {#if selected}<p class="notice">
      「{selected.service}」へ AI イベント {index + 1} を紐付けます。契約の金額・日付・状態は変更しません。
    </p>{/if}
  <label class="check"
    ><input
      type="checkbox"
      checked={confirmed}
      disabled={!selected || saving || conflict}
      onchange={(e) => (confirmed = e.currentTarget.checked)}
    />紐付け先と元の抽出情報を確認しました</label
  >
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}<button
    class="primary"
    disabled={!selected || !confirmed || saving || conflict}
    onclick={attach}>確認したサブスクへ紐付ける</button
  >
</div>
