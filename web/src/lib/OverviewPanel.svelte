<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { FeedView } from './daily-controller.ts';
  import type { OverviewReport } from './overview-api.ts';
  import { money, cycle } from './daily-text.ts';
  import { processingDate } from './mail-processing-text.ts';
  import { inclusiveEnd } from './mail-automation-api.ts';
  import BillingSummaryTable from './BillingSummaryTable.svelte';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  let {
    view,
    onReload = () => {},
    empty,
  }: {
    view: FeedView<OverviewReport>;
    onReload?: () => void;
    empty?: Snippet;
  } = $props();
  const report = $derived(view.status === 'ready' ? view.page : null);
  const connectionNames = {
    unverified: '接続未確認',
    ready: '接続確認済み',
    needs_reauth: '再認証が必要',
    disconnected: '切断済み',
  };
  const automationNames = {
    not_configured: '取り込み未設定',
    enabled: '自動取り込み設定あり',
    stopped: '取り込み停止中',
    needs_confirmation: '取り込み条件の再確認が必要',
  };
</script>

<section
  class="daily overview library-page"
  aria-labelledby="overview-heading"
  aria-busy={view.status === 'loading'}
>
  <LibraryHeader title="ホーム" eyebrow="OVERVIEW" id="overview-heading">
    {#snippet action()}
      <a
        class="button primary"
        href={report?.subscriptions.total === 0 ? '/import' : '/subscriptions'}
        >{report?.subscriptions.total === 0
          ? 'メールを取り込む'
          : 'サブスクを見る'}</a
      >
    {/snippet}
  </LibraryHeader>
  {#if view.status === 'loading' || view.status === 'idle'}<p
      class="notice"
      role="status"
    >
      ホームを読み込んでいます…
    </p>{:else if view.status === 'denied' || view.status === 'disposed'}<p
      role="status"
    >
      利用状態を確認しています…
    </p>{:else if view.error}<div class="notice" role="alert">
      <p>
        ホームの情報を取得できませんでした。時間をおいて、もう一度読み込んでください。
      </p>
      <button type="button" onclick={onReload}>もう一度読み込む</button>
    </div>{:else if report}
    <div class="overview-status">
      <span
        >登録したサブスク <strong>{report.subscriptions.total} 件</strong></span
      ><a href="/analysis?tab=attention"
        >確認が必要なメール <strong>{report.attention_messages} 件</strong></a
      >
    </div>
    {#if report.subscriptions.total === 0}<LibraryPanel padded
        ><div class="first-use">
          {#if empty}{@render empty()}{:else}<h2>
              メールからサブスクを見つけましょう
            </h2>
            <p class="muted">
              連携したメールから契約情報をまとめられます。
            </p>{/if}
        </div></LibraryPanel
      >{:else}
      <LibraryPanel
        ><section class="overview-section" aria-labelledby="saved-dates">
          <div class="section-heading library-toolbar">
            <div>
              <h2 id="saved-dates">保存済みの次回請求日</h2>
              <p class="muted">
                {report.as_of}〜{inclusiveEnd(report.window_end)} · 日本時間
              </p>
            </div>
            <button type="button" onclick={onReload}>更新</button>
          </div>
          {#if report.subscriptions.overdue || report.subscriptions.undated}<p
              class="date-notice library-content"
            >
              保存日が過ぎた予定 {report.subscriptions.overdue} 件 · 日付未設定 {report
                .subscriptions.undated} 件
            </p>{/if}
          {#if report.upcoming.length}<table class="upcoming-table">
              <thead
                ><tr><th>サービス</th><th>登録料金</th><th>次回請求日</th></tr
                ></thead
              ><tbody
                >{#each report.upcoming as item (item.id)}<tr
                    ><td
                      ><strong
                        ><a href={`/subscriptions/${item.id}`}>{item.service}</a
                        ></strong
                      ><span class="secondary"
                        >{item.plan ??
                          'プラン不明'}{#if item.status === 'unknown'}
                          · 利用状態不明{/if}</span
                      ></td
                    ><td class="amount" data-label="登録料金"
                      >{money(item.amount, item.currency)}<span
                        class="secondary"
                        >{cycle(item.interval_unit, item.interval_count)}</span
                      ></td
                    ><td data-label="次回請求日">{item.next_billing_date}</td
                    ></tr
                  >{/each}</tbody
              >
            </table>{:else}<p class="empty">
              この期間に保存されている次回請求日はありません。
            </p>{/if}
        </section></LibraryPanel
      >{/if}
    <details class="supplement">
      <summary>支払実績・メール連携・表示条件</summary>
      <div class="supplement-body">
        <section
          class="overview-section billing"
          aria-labelledby="month-actuals"
        >
          <div class="section-heading">
            <h2 id="month-actuals">
              {report.billing_month.month} の支払・返金実績
            </h2>
            <a
              href={`/subscriptions/summary?month=${report.billing_month.month}`}
              >月次集計へ</a
            >
          </div>
          <p class="muted">
            自分のすべてのサブスク（終了済みを含む）の保存済み明細です。
          </p>
          <BillingSummaryTable summary={report.billing_month} />
        </section>
        <section class="overview-section" aria-labelledby="mail-status">
          <div class="section-heading">
            <h2 id="mail-status">メール連携と取り込み</h2>
            <a href="/settings/mail">メール連携を管理</a>
          </div>
          {#if report.connections.length}<ul class="connection-list">
              {#each report.connections as connection (connection.id)}<li>
                  <strong>{connection.label}</strong>
                  <p>
                    {connectionNames[connection.state]} · {automationNames[
                      connection.automation_state
                    ]}
                  </p>
                  {#if connection.blocked_reasons.length}<p>
                      取り込みの準備条件を確認してください。
                    </p>{/if}
                  <p class="muted">
                    最終メール確認：{connection.last_success_at
                      ? processingDate(connection.last_success_at)
                      : 'まだありません'}
                  </p>
                  {#if connection.automation_state === 'enabled' && connection.ready && connection.next_check_at}<p
                      class="muted"
                    >
                      次の確認予定：{processingDate(connection.next_check_at)}
                    </p>{/if}<a href={`/analysis?account_id=${connection.id}`}
                    >この接続の取り込み状況を見る</a
                  >
                </li>{/each}
            </ul>{:else}<p>メール接続はまだ登録されていません。</p>{/if}
        </section>
        <section class="overview-section population">
          <h2>表示条件</h2>
          <p>
            基準日：{report.as_of}（日本時間） · 取得：{processingDate(
              report.generated_at,
            )}
          </p>
          <p>
            登録状況：利用中 {report.subscriptions.active} · 状態不明 {report
              .subscriptions.unknown} · 終了済み {report.subscriptions.ended}
          </p>
          <p>
            確認が必要な結果はメール単位です。失敗・結果不明・登録の確認待ちを含みます。
          </p>
          <p>
            次回請求日は今日から30日間の利用中・状態不明の契約が対象です。終了済みは含めません。対象
            {report.subscriptions.upcoming} 件のうち、日付順に最大10件を表示しています。
          </p>
          <p>
            登録料金と周期は保存されている契約情報です。日付を過ぎただけでは支払い済みとは判断できません。実際の引き落としを確定するものではありません。
          </p>
        </section>
      </div>
    </details>
  {/if}
</section>

<style>
  .overview {
    min-width: 0;
  }
  .overview-status {
    display: flex;
    gap: 28px;
    flex-wrap: wrap;
    padding: 18px 0 26px;
    color: var(--color-muted);
    font-size: 13px;
  }
  .overview-status strong {
    margin-left: 6px;
    font-weight: 600;
    color: var(--color-text);
  }
  .overview-status a {
    color: inherit;
    text-decoration: none;
  }
  .overview-section {
    min-width: 0;
  }
  .section-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 12px;
    margin-bottom: 20px;
  }
  .section-heading h2 {
    font-size: 17px;
    margin: 0 0 6px;
  }
  .section-heading p {
    font-size: 12px;
  }
  .section-heading > a {
    font-size: 12px;
  }
  .date-notice {
    font-size: 12px;
    color: var(--color-muted);
    margin-bottom: 16px !important;
  }
  .upcoming-table a {
    color: inherit;
    text-decoration: none;
  }
  .upcoming-table a:hover {
    text-decoration: underline;
  }
  .upcoming-table td:first-child {
    width: 42%;
  }
  .amount {
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
  }
  .supplement {
    margin-top: 28px;
    border-top: 1px solid var(--color-border);
    padding: 18px 0;
    color: var(--color-muted);
    font-size: 12px;
  }
  .supplement > summary {
    cursor: pointer;
    min-height: 32px;
  }
  .supplement-body {
    padding-top: 16px;
    color: var(--color-text);
  }
  .supplement-body .overview-section + .overview-section {
    border-top: 1px solid var(--color-border);
    padding-top: 24px;
    margin-top: 24px;
  }
  .connection-list {
    list-style: none;
    padding: 0;
  }
  .connection-list li {
    padding: 16px 0;
    border-bottom: 1px solid var(--color-border);
  }
  .connection-list p {
    margin: 8px 0 !important;
    font-size: 12px;
  }
  .population {
    line-height: 1.9;
    color: var(--color-muted);
    font-size: 12px;
  }
  .population h2 {
    font-size: 15px;
  }
  .population p + p {
    margin-top: 8px !important;
  }
  .first-use {
    padding: 44px 0;
  }
  .first-use h2 {
    font-size: 20px;
  }
  @media (max-width: 700px) {
    .upcoming-table tr {
      padding: 18px 16px;
      gap: 14px;
    }
    .upcoming-table tr td {
      padding: 0;
      border: 0;
    }
    .upcoming-table tr td:first-child {
      width: 100%;
    }
    .overview-status {
      gap: 12px;
      flex-direction: column;
      font-size: 12px;
    }
    .section-heading h2 {
      font-size: 15px;
    }
  }
</style>
