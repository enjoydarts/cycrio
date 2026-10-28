<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { FeedView } from './daily-controller.ts';
  import type { OverviewReport } from './overview-api.ts';
  import { money, cycle } from './daily-text.ts';
  import { processingDate } from './mail-processing-text.ts';
  import { inclusiveEnd } from './mail-automation-api.ts';
  import BillingSummaryTable from './BillingSummaryTable.svelte';
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
  class="daily overview"
  aria-labelledby="overview-heading"
  aria-busy={view.status === 'loading'}
>
  <header class="titlebar">
    <div>
      <h1 id="overview-heading">ホーム</h1>
      <p>請求予定、確認が必要な結果、メールの取り込み状況を見渡せます。</p>
    </div>
    <button
      type="button"
      disabled={view.status === 'loading' ||
        view.status === 'denied' ||
        view.status === 'disposed'}
      onclick={onReload}>最新の状態に更新</button
    >
  </header>
  {#if view.status === 'loading' || view.status === 'idle'}<p
      class="notice"
      role="status"
    >
      ホームを読み込んでいます…
    </p>
  {:else if view.status === 'denied' || view.status === 'disposed'}<p
      role="status"
    >
      利用状態を確認しています…
    </p>
  {:else if view.error}<div class="notice" role="alert">
      <p>
        ホームの情報を取得できませんでした。時間をおいて、もう一度読み込んでください。
      </p>
      <button type="button" onclick={onReload}>もう一度読み込む</button>
    </div>
  {:else if report}
    <p class="muted">
      基準日：{report.as_of}（日本時間） · 取得：{processingDate(
        report.generated_at,
      )}
    </p>
    {#if report.subscriptions.total === 0}<div class="first-use">
        {#if empty}{@render empty()}{:else}<p class="notice">
            サブスクはまだ登録されていません。<a href="/subscriptions"
              >サブスクの登録・初回案内へ</a
            >
          </p>{/if}
      </div>{/if}
    <div class="overview-metrics" aria-label="本人の登録状況">
      <article>
        <h2>登録済みサブスク</h2>
        <p class="metric">{report.subscriptions.total}<span> 件</span></p>
        <p class="muted">
          利用中 {report.subscriptions.active} · 状態不明 {report.subscriptions
            .unknown} · 終了済み {report.subscriptions.ended}
        </p>
        <a href="/subscriptions">サブスク一覧へ</a>
      </article>
      <article>
        <h2>30日以内の保存済み予定</h2>
        <p class="metric">{report.subscriptions.upcoming}<span> 件</span></p>
        <p class="muted">
          {report.as_of}〜{inclusiveEnd(report.window_end)}（日本時間）
        </p>
        <a href="#saved-dates">保存済みの予定を見る</a>
      </article>
      <article>
        <h2>要対応の解析結果</h2>
        <p class="metric">{report.attention_messages}<span> 件</span></p>
        <p class="muted">
          メール単位の件数です。失敗・結果不明・登録の確認待ちを含みます。
        </p>
        <a href="/analysis?tab=attention">要対応の結果を見る</a>
      </article>
    </div>
    {#if report.subscriptions.overdue || report.subscriptions.undated}<div
        class="notice saved-attention"
      >
        <p>
          保存日が過ぎている予定：<strong
            >{report.subscriptions.overdue} 件</strong
          >
          · 次回日が未設定：<strong>{report.subscriptions.undated} 件</strong
          >（利用中・状態不明の契約）
        </p>
        <p>
          日付を過ぎただけでは、支払い済みとは判断できません。各契約で次回日と請求明細を確認してください。
        </p>
        <a href="/subscriptions">契約を確認する</a>
      </div>{/if}
    <section class="overview-section" aria-labelledby="saved-dates">
      <h2 id="saved-dates">保存済みの次回請求日</h2>
      <p class="muted">
        今日から30日間の、利用中・状態不明の契約が対象です。終了済みは含めません。登録料金と周期は保存されている契約情報です。
      </p>
      {#if report.upcoming.length}<ul class="overview-list">
          {#each report.upcoming as item (item.id)}<li>
              <p class="date">{item.next_billing_date}</p>
              <h3><a href={`/subscriptions/${item.id}`}>{item.service}</a></h3>
              <p class="muted">
                {item.plan ?? 'プラン不明'} · {item.status === 'unknown'
                  ? '利用状態不明'
                  : '利用中'}
              </p>
              <dl>
                <div>
                  <dt>登録料金 / 周期</dt>
                  <dd class="amount">
                    {money(item.amount, item.currency)} · {cycle(
                      item.interval_unit,
                      item.interval_count,
                    )}
                  </dd>
                </div>
              </dl>
              <p class="muted">
                {item.provenance === 'automatic'
                  ? 'メールからの自動登録'
                  : '本人が保存した契約情報'}
              </p>
            </li>{/each}
        </ul>
        <p class="muted">
          対象 {report.subscriptions.upcoming} 件のうち、日付順に最大10件を表示しています。
        </p>
      {:else}<p class="empty">
          この期間に保存されている次回請求日はありません。
        </p>{/if}
      <p class="muted">
        保存した日付や登録料金から、実際の引き落としを確定するものではありません。周期から計算する候補や日付の更新は、契約の詳細で確認できます。
      </p>
    </section>
    <section class="overview-section billing" aria-labelledby="month-actuals">
      <div class="section-heading">
        <h2 id="month-actuals">
          {report.billing_month.month} の支払・返金実績
        </h2>
        <a href={`/subscriptions/summary?month=${report.billing_month.month}`}
          >月次集計へ</a
        >
      </div>
      <p class="muted">
        自分のすべてのサブスク（終了済みを含む）の保存済み明細を集計しています。
      </p>
      <BillingSummaryTable summary={report.billing_month} />
    </section>
    <section class="overview-section" aria-labelledby="mail-status">
      <div class="section-heading">
        <h2 id="mail-status">メール連携と取り込み</h2>
        <a href="/settings/mail">メール連携を管理</a>
      </div>
      {#if report.connections.length}<ul class="overview-list">
          {#each report.connections as connection (connection.id)}<li>
              <h3>{connection.label}</h3>
              <p>
                {connectionNames[connection.state]} · {automationNames[
                  connection.automation_state
                ]}
              </p>
              {#if connection.blocked_reasons.length}<p class="muted">
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
                </p>{/if}
              <a href={`/analysis?account_id=${connection.id}`}
                >この接続の取り込み状況を見る</a
              >
            </li>{/each}
        </ul>{:else}<p class="empty">
          メール接続はまだ登録されていません。
        </p>{/if}
      <p class="muted">
        最新の接続条件や進捗は、取り込み状況の画面で確認できます。
      </p>
    </section>
  {/if}
</section>

<style>
  .overview {
    min-width: 0;
  }
  .overview-metrics {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 16px;
    margin-block: 24px;
  }
  .overview-metrics article,
  .overview-list li {
    border: 1px solid var(--color-border);
    border-radius: 8px;
    padding: 20px;
    min-width: 0;
    background: var(--color-surface);
  }
  .overview-metrics h2 {
    font-size: 14px;
    margin: 0;
  }
  .overview-metrics p {
    margin: 10px 0;
  }
  .metric {
    font-size: 30px;
    font-weight: 650;
  }
  .metric span {
    font-size: 14px;
    font-weight: 400;
  }
  .overview-section {
    margin-block: 32px;
    min-width: 0;
  }
  .section-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 12px;
  }
  .overview-list {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    list-style: none;
    padding: 0;
    gap: 16px;
  }
  h3 {
    font-size: 17px;
    margin: 8px 0;
    overflow-wrap: anywhere;
  }
  .overview-list p {
    margin: 8px 0;
  }
  .overview-list dl {
    margin: 16px 0;
  }
  dt {
    color: var(--color-muted);
    font-size: 13px;
  }
  dd {
    margin: 5px 0 0;
  }
  .amount,
  a {
    overflow-wrap: anywhere;
  }
  .date {
    color: var(--color-action);
    font-weight: 600;
  }
  .first-use {
    margin-block: 24px;
  }
  @media (max-width: 900px) {
    .overview-metrics {
      grid-template-columns: 1fr;
    }
  }
  @media (max-width: 700px) {
    .overview-list {
      grid-template-columns: 1fr;
    }
  }
</style>
