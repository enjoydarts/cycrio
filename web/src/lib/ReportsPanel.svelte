<script lang="ts">
  import { subscriptionTitle } from './subscription-title.ts';
  import { untrack } from 'svelte';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import MonthSelector from './MonthSelector.svelte';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import BrandIcon from './BrandIcon.svelte';
  import DataTable from './DataTable.svelte';
  import type { FeedView } from './daily-controller.ts';
  import type { Report } from './reports-api.ts';
  import type { ExchangeRates } from './exchange-rates-api.ts';
  import {
    projectReport,
    currencyCSV,
    type CurrencyMode,
  } from './report-currency.ts';
  import ReportCurrencyControl from './ReportCurrencyControl.svelte';
  import {
    displayAmount,
    displayEstimate,
    upcomingWithin,
    savings,
    subtractAmounts,
    maxAmount,
    chartPercent,
    serviceBreakdown,
  } from './reports-helpers.ts';
  let {
    view,
    month,
    currentMonth,
    initialTab = 'summary',
    initialCurrencyMode = 'native',
    ratesView = { page: null, status: 'idle', error: null },
    onRatesReload = () => {},
    onMonthChange = () => {},
    onReload = () => {},
  }: {
    view: FeedView<Report>;
    month: string;
    currentMonth: string;
    initialTab?: string;
    initialCurrencyMode?: CurrencyMode;
    ratesView?: FeedView<ExchangeRates>;
    onRatesReload?: () => void;
    onMonthChange?: (month: string) => void;
    onReload?: () => void;
  } = $props();
  const tabs = [
    ['summary', '支出サマリー'],
    ['services', 'サービス別'],
    ['upcoming', '請求予定'],
    ['savings', '見直し'],
    ['monthly', '月次レポート'],
  ] as const;
  let tab = $state<string>(untrack(() => initialTab)),
    days = $state(30),
    selected = $state<string[]>([]),
    currencyMode = $state<CurrencyMode>(untrack(() => initialCurrencyMode));
  const nativeReport = $derived(view.status === 'ready' ? view.page : null);
  const rates = $derived(ratesView.status === 'ready' ? ratesView.page : null);
  const projection = $derived(
    nativeReport && rates ? projectReport(nativeReport, rates) : null,
  );
  const report = $derived(
    currencyMode === 'native' ? nativeReport : (projection?.report ?? null),
  );
  const normalized = $derived(
    report?.contracts.filter((x) => x.exclusion_reason === '') ?? [],
  );
  const breakdown = $derived(serviceBreakdown(report?.contracts ?? []));
  const currentCurrencies = $derived(
    report?.currencies.filter(
      (x) => x.monthly_amount !== null || x.next_30_days_count > 0,
    ) ?? [],
  );
  const unpriced = $derived(
    report?.contracts.filter((x) =>
      ['missing_amount', 'missing_currency', 'missing_interval'].includes(
        x.exclusion_reason,
      ),
    ) ?? [],
  );
  const missingDates = $derived(
    report?.contracts.filter(
      (x) =>
        x.status !== 'ended' &&
        x.contract_kind !== 'fixed_term' &&
        x.exclusion_reason !== 'access_ended' &&
        !x.billing_date_suppressed &&
        (!x.next_billing_date || x.next_billing_date < report!.as_of),
    ) ?? [],
  );
  const reasonNames = {
    missing_amount: '金額不明',
    missing_currency: '通貨不明',
    missing_interval: '周期不明',
  };
  const reductions = $derived(savings(normalized, selected));
  const due = $derived(
    report ? upcomingWithin(report.upcoming, report.as_of, days) : [],
  );
  const historicalCurrencies = $derived(
    [
      ...new Set(
        report?.history.flatMap((x) => x.currencies.map((c) => c.currency)) ??
          [],
      ),
    ].sort(),
  );
  function toggle(id: string, checked: boolean) {
    selected = checked ? [...selected, id] : selected.filter((x) => x !== id);
  }
  function download() {
    if (!report) return;
    const url = URL.createObjectURL(
      new Blob([currencyCSV(nativeReport!, currencyMode, rates)], {
        type: 'text/csv;charset=utf-8',
      }),
    );
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `cycrio-report-${report.month}.csv`;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }
</script>

<section
  class="daily reports library-page"
  aria-labelledby="reports-heading"
  aria-busy={view.status === 'loading'}
>
  <LibraryHeader
    title="分析・レポート"
    eyebrow="REPORTS"
    id="reports-heading"
    description="登録料金の見通しと、記録された支払いを確認できます。"
  >
    {#snippet action()}<Button
        type="button"
        onclick={onReload}
        disabled={view.status === 'loading'}>更新</Button
      >{/snippet}
  </LibraryHeader>
  <LibraryPanel>
    <ReportCurrencyControl
      mode={currencyMode}
      {ratesView}
      onchange={(value) => (currencyMode = value)}
      onRetry={onRatesReload}
      excluded={projection?.excluded_contracts ?? 0}
      excludedActual={projection?.excluded_actual_rows ?? 0}
      excludedUpcoming={projection?.excluded_upcoming ?? 0}
    />
    <nav class="library-tabs" aria-label="レポートの種類">
      {#each tabs as [key, label]}<Button
          type="button"
          variant="tab"
          aria-pressed={tab === key}
          onclick={() => (tab = key)}>{label}</Button
        >{/each}
    </nav>
    {#if view.status === 'loading' || view.status === 'idle'}<p
        class="library-content"
        role="status"
      >
        レポートを読み込んでいます…
      </p>
    {:else if view.status === 'denied' || view.status === 'disposed'}<p
        class="library-content"
        role="status"
      >
        利用状態を確認しています…
      </p>
    {:else if view.error}<div class="library-content" role="alert">
        <p>レポートを取得できませんでした。</p>
        <Button type="button" onclick={onReload}>もう一度読み込む</Button>
      </div>
    {:else if report}
      {#if tab === 'summary'}
        <div class="library-content">
          <div class="section-title">
            <h2>現在の登録料金</h2>
            <span class="muted">{report.as_of} 時点 · 日本時間</span>
          </div>
          <p class="muted">
            利用中・状態不明の継続契約を換算。終了済みと期間契約は対象外です。料金・周期が不明な契約は換算に含まず、今後30日の金額は料金と通貨が判明した予定のみの小計です。
          </p>
          <div class="currency-grid">
            {#each currentCurrencies as item}<div class="metric">
                <span>{item.currency} · {item.contract_count} 契約</span><strong
                  >{displayEstimate(item.monthly_amount, item.currency)}<small>
                    / 月</small
                  ></strong
                >
                <p>
                  年換算 {displayEstimate(item.annual_amount, item.currency)}
                </p>
                <p class="muted">
                  今後30日 {item.next_30_days_count === 0
                    ? '予定なし'
                    : displayAmount(item.next_30_days_amount, item.currency)} · {item.next_30_days_count}
                  件
                </p>
              </div>{/each}
          </div>
          {#if !currentCurrencies.length}<p class="empty">
              集計できる登録料金がありません。
            </p>{/if}
          <div class="coverage">
            <span
              >利用終了 {report.contracts.filter(
                (row) => row.exclusion_reason === 'access_ended',
              ).length}</span
            >
            <span>利用中 {report.coverage.active_contracts}</span><span
              >状態不明 {report.coverage.unknown_contracts}</span
            ><span>終了済み {report.coverage.ended_contracts}</span><span
              >期間契約 {report.coverage.fixed_term_contracts}</span
            ><span>換算対象 {report.coverage.normalized_contracts}</span><span
              >料金・周期不足 {report.coverage.unpriced_contracts}</span
            >
          </div>
          {#if unpriced.length}<details class="review-details">
              <summary>料金・周期を確認する（{unpriced.length} 件）</summary>
              <ul>
                {#each unpriced as row}<li>
                    <a href={`/subscriptions/${row.id}`}
                      >{subscriptionTitle(row)}</a
                    ><span
                      >{reasonNames[
                        row.exclusion_reason as keyof typeof reasonNames
                      ]}</span
                    >
                  </li>{/each}
              </ul>
            </details>{/if}
          {#each currentCurrencies.filter((x) => x.monthly_amount !== null) as currency}
            <section class="ranking">
              <h3>{currency.currency} · 月換算の上位サービス</h3>
              <ol>
                {#each breakdown
                  .filter((x) => x.currency === currency.currency && x.monthly_amount !== null)
                  .slice(0, 3) as row}{@const share = chartPercent(
                    row.monthly_amount,
                    currency.monthly_amount!,
                  )}
                  <li>
                    <span class="service"
                      ><BrandIcon
                        name={row.service}
                        size="small"
                      />{row.service}</span
                    ><span
                      >{displayEstimate(row.monthly_amount, row.currency)}
                      <small>· {share}%</small></span
                    >
                  </li>{/each}
              </ol>
            </section>
          {/each}
        </div>
      {:else if tab === 'services'}
        <div class="library-toolbar">
          <div>
            <h2>サービス別の登録料金</h2>
            <p class="muted">月・年換算は現在の契約情報に基づく参考値です。</p>
          </div>
        </div>
        {#if breakdown.length}<DataTable responsive="cards"
            ><thead
              ><tr
                ><th>サービス</th><th>月換算</th><th>年換算</th><th
                  >同一通貨の構成比</th
                ><th>契約数</th></tr
              ></thead
            ><tbody
              >{#each breakdown as row}{@const total =
                  report.currencies.find((x) => x.currency === row.currency)
                    ?.monthly_amount ?? '0'}{@const share = chartPercent(
                  row.monthly_amount,
                  total,
                )}<tr
                  ><td data-primary
                    ><span class="service"
                      ><BrandIcon
                        name={row.service}
                        size="small"
                      />{row.service}</span
                    >{#if row.unpriced}<span class="secondary"
                        >料金・周期不足 {row.unpriced} 件 · 判明分のみ集計</span
                      >{/if}{#if row.unknown}<span class="secondary"
                        >利用状態不明 {row.unknown} 件を含む</span
                      >{/if}</td
                  ><td data-label="月換算"
                    >{displayEstimate(row.monthly_amount, row.currency)}</td
                  ><td data-label="年換算"
                    >{displayEstimate(row.annual_amount, row.currency)}</td
                  ><td data-label="同一通貨の構成比"
                    >{row.monthly_amount === null ? '不明' : `${share}%`}<span
                      class="share-track"
                      aria-hidden="true"
                      ><span style:width={`${share}%`}></span></span
                    ></td
                  ><td data-label="契約数">{row.count} 件</td></tr
                >{/each}</tbody
            ></DataTable
          >{:else}<p class="library-content">
            集計対象のサブスクがありません。
          </p>{/if}
      {:else if tab === 'upcoming'}
        <div class="library-toolbar">
          <div>
            <h2>保存済みの次回請求日・自動推定</h2>
            <p class="muted">今日から{days}日間 · 日本時間</p>
          </div>
          <label
            >表示期間 <select bind:value={days}
              ><option value={30}>30日</option><option value={90}>90日</option
              ><option value={365}>365日</option></select
            ></label
          >
        </div>
        <p class="date-note">
          日付未設定 {report.coverage.missing_date_contracts} 件 · 保存日超過 {report
            .coverage.overdue_contracts} 件
        </p>
        {#if missingDates.length}<details class="review-details date-note">
            <summary>日付を確認する（{missingDates.length} 件）</summary>
            <ul>
              {#each missingDates as row}<li>
                  <a href={`/subscriptions/${row.id}`}
                    >{subscriptionTitle(row)}</a
                  ><span
                    >{row.next_billing_date
                      ? `保存日超過 · ${row.next_billing_date}`
                      : '日付未設定'}</span
                  >
                </li>{/each}
            </ul>
          </details>{/if}
        {#if due.length}<DataTable responsive="cards"
            ><thead
              ><tr><th>サービス</th><th>登録料金</th><th>次回請求日</th></tr
              ></thead
            ><tbody
              >{#each due as row}<tr
                  ><td data-primary
                    ><span class="service"
                      ><BrandIcon name={row.service} size="small" /><a
                        href={`/subscriptions/${row.subscription_id}`}
                        >{subscriptionTitle(row)}</a
                      ></span
                    >{#if row.display_name}<span class="secondary"
                        >{row.service}</span
                      >{/if}<span class="secondary"
                      >{row.plan ?? 'プラン不明'}{row.status === 'unknown'
                        ? ' · 状態不明'
                        : ''}</span
                    ></td
                  ><td data-label="登録料金"
                    >{displayAmount(
                      row.amount,
                      row.currency,
                    )}{#if row.annual_renewal}<span class="secondary"
                        >年次更新</span
                      >{/if}</td
                  ><td data-label="次回請求日"
                    >{row.date}{row.next_billing_mode === 'automatic'
                      ? '（推定）'
                      : ''}</td
                  ></tr
                >{/each}</tbody
            ></DataTable
          >{:else}<p class="library-content">
            この期間の保存済み請求予定はありません。
          </p>{/if}
        <p class="library-footer">
          手動指定日または、最新のメール受信日と周期から計算した推定日を表示します。支払いの確定は行いません。
        </p>
      {:else if tab === 'savings'}
        <div class="library-content">
          <div class="section-title">
            <h2>見直しシミュレーション</h2>
            <Button
              type="button"
              disabled={!selected.length}
              onclick={() => (selected = [])}>選択解除</Button
            >
          </div>
          <p class="muted">
            見直す契約を選ぶと、登録料金から削減額を試算します。契約の保存・解約は行いません。
          </p>
          <ul class="simulation-list">
            {#each normalized as row}<li>
                <label class="simulation-choice"
                  ><input
                    type="checkbox"
                    checked={selected.includes(row.id)}
                    onchange={(event) =>
                      toggle(row.id, event.currentTarget.checked)}
                  /><BrandIcon name={row.service} size="small" /><span
                    class="choice-name"
                    >{subscriptionTitle(row)}{#if row.display_name}<small
                        >{row.service}</small
                      >{/if}{#if row.plan}<small>{row.plan}</small
                      >{/if}{#if row.status === 'unknown'}<small
                        >利用状態不明</small
                      >{/if}</span
                  ><span class="choice-amount"
                    >{displayEstimate(row.monthly_amount, row.currency)}<small>
                      / 月</small
                    ></span
                  ></label
                >
              </li>{/each}
          </ul>
          {#if !normalized.length}<p>試算できる契約がありません。</p>{/if}
          <div class="currency-grid" aria-live="polite">
            {#each report.currencies.filter((x) => x.monthly_amount !== null) as item}{@const reduction =
                reductions.find((x) => x.currency === item.currency)}
              <div class="metric">
                <span>{item.currency} の試算</span><strong
                  >{displayEstimate(
                    reduction?.monthly_amount ?? '0',
                    item.currency,
                  )}<small> / 月削減</small></strong
                >
                <p>
                  年換算 {displayEstimate(
                    reduction?.annual_amount ?? '0',
                    item.currency,
                  )} 削減
                </p>
                <p class="muted">
                  見直し後 {displayEstimate(
                    subtractAmounts(
                      item.monthly_amount!,
                      reduction?.monthly_amount ?? '0',
                    ),
                    item.currency,
                  )} / 月
                </p>
                <p class="muted">
                  年換算 {displayEstimate(
                    subtractAmounts(
                      item.annual_amount!,
                      reduction?.annual_amount ?? '0',
                    ),
                    item.currency,
                  )}
                </p>
              </div>{/each}
          </div>
        </div>
      {:else if tab === 'monthly'}
        <div class="library-toolbar">
          <MonthSelector
            value={month}
            {currentMonth}
            onchange={onMonthChange}
          /><ActionGroup
            ><Button type="button" onclick={download}>CSVをダウンロード</Button
            ></ActionGroup
          >
        </div>
        <div class="library-content">
          <h2>{report.month} の支払い実績</h2>
          <p class="muted">
            {report.history_start}〜{report.history_end}（終了日を含まない・日本時間）。終了済み契約も含みます。支払−返金が差引額で、請求は別集計です。記録がない金額は「記録なし」と表示します。
          </p>
          <div class="currency-grid">
            {#each report.history.find((x) => x.month === report.month)?.currencies ?? [] as item}<div
                class="metric"
              >
                <span>{report.month} · {item.currency}</span><strong
                  >{item.net_amount === null
                    ? '記録なし'
                    : displayAmount(item.net_amount, item.currency)}<small>
                    差引</small
                  ></strong
                >
                <p>
                  支払 {item.payment_amount === null
                    ? '記録なし'
                    : displayAmount(item.payment_amount, item.currency)} · {item.payment_count}
                  件
                </p>
                <p>
                  返金 {item.refund_amount === null
                    ? '記録なし'
                    : displayAmount(item.refund_amount, item.currency)} · {item.refund_count}
                  件
                </p>
                <p class="muted">
                  請求（別集計） {item.invoice_amount === null
                    ? '記録なし'
                    : displayAmount(item.invoice_amount, item.currency)} · {item.invoice_count}
                  件
                </p>
              </div>{/each}
          </div>
          {#each historicalCurrencies as code}{@const maximum = maxAmount(
              report.history.map(
                (x) =>
                  x.currencies.find((c) => c.currency === code)?.net_amount ??
                  null,
              ),
            )}
            <section class="history-chart" aria-label={`${code}の12か月差引額`}>
              <h3>{code} · 直近12か月の差引額</h3>
              <ol class="chart-list">
                {#each report.history as bucket}{@const value =
                    bucket.currencies.find((x) => x.currency === code)
                      ?.net_amount ?? null}
                  <li>
                    <span>{bucket.month}</span><span
                      class="bar-track"
                      aria-hidden="true"
                      ><span
                        class="bar"
                        class:negative={value?.startsWith('-')}
                        style:width={`${chartPercent(value, maximum)}%`}
                      ></span></span
                    ><span
                      >{value === null
                        ? '記録なし'
                        : displayAmount(value, code)}</span
                    >
                  </li>{/each}
              </ol>
            </section>{/each}
          {#if !historicalCurrencies.length}<p class="empty">
              この12か月に支払い・返金・請求の記録がありません。
            </p>{/if}
        </div>
        {#if report.monthly_services.length}<DataTable responsive="cards"
            ><caption>{report.month} のサービス別実績</caption><thead
              ><tr
                ><th>サービス</th><th>支払</th><th>返金</th><th>差引</th><th
                  >請求（別集計）</th
                ></tr
              ></thead
            ><tbody
              >{#each report.monthly_services as row}<tr
                  ><td data-primary
                    ><span class="service"
                      ><BrandIcon name={row.service} size="small" /><a
                        href={`/subscriptions/${row.subscription_id}`}
                        >{subscriptionTitle(row)}</a
                      ></span
                    >{#if row.display_name}<span class="secondary"
                        >{row.service}</span
                      >{/if}{#if row.plan}<span class="secondary"
                        >{row.plan}</span
                      >{/if}</td
                  ><td data-label="支払"
                    >{row.payment_amount === null
                      ? '記録なし'
                      : displayAmount(row.payment_amount, row.currency)}<span
                      class="secondary">{row.payment_count} 件</span
                    ></td
                  ><td data-label="返金"
                    >{row.refund_amount === null
                      ? '記録なし'
                      : displayAmount(row.refund_amount, row.currency)}<span
                      class="secondary">{row.refund_count} 件</span
                    ></td
                  ><td data-label="差引"
                    >{row.net_amount === null
                      ? '記録なし'
                      : displayAmount(row.net_amount, row.currency)}</td
                  ><td data-label="請求（別集計）"
                    >{row.invoice_amount === null
                      ? '記録なし'
                      : displayAmount(row.invoice_amount, row.currency)}<span
                      class="secondary">{row.invoice_count} 件</span
                    ></td
                  ></tr
                >{/each}</tbody
            ></DataTable
          >{:else}<p class="library-content">選択月の記録はありません。</p>{/if}
      {/if}
    {/if}
  </LibraryPanel>
  {#if report}<p class="report-footnote">
      現在換算は保存済み料金を年365日で換算した参考値です。実績は取り込まれた記録のみで、口座の引き落としを保証しません。元通貨表示では通貨ごとに集計し、円換算表示では最新取得レートを使います。
    </p>{/if}
</section>

<style>
  .reports {
    min-width: 0;
  }
  .ranking {
    margin-top: 24px;
    border-top: 1px solid var(--color-border);
    padding-top: 20px;
  }
  .ranking ol,
  .review-details ul {
    list-style: none;
    padding: 0;
    margin: 12px 0 0;
  }
  .ranking li,
  .review-details li {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    align-items: center;
    padding: 10px 0;
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .ranking li > span:last-child {
    text-align: right;
    max-width: 50%;
  }
  .ranking small,
  .review-details li > span {
    color: var(--color-muted);
  }
  .review-details {
    font-size: 12px;
    margin-top: 18px;
  }
  .review-details summary {
    cursor: pointer;
    min-height: 30px;
  }
  .share-track {
    display: block;
    height: 5px;
    min-width: 50px;
    background: var(--color-surface-soft);
    margin-top: 8px;
    border-radius: 2px;
    overflow: hidden;
  }
  .share-track > span {
    display: block;
    height: 100%;
    background: var(--color-action);
  }
  h2 {
    font-size: 17px;
    margin: 0 0 8px;
  }
  h3 {
    font-size: 14px;
    margin: 0 0 16px;
  }
  .section-title {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
    margin-bottom: 12px;
  }
  .muted,
  .report-footnote,
  .date-note {
    color: var(--color-muted);
    font-size: 12px;
    line-height: 1.8;
  }
  .currency-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
    gap: 16px;
    margin-top: 20px;
  }
  .metric {
    padding: 20px;
    background: var(--color-surface-soft);
    border-radius: 10px;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .metric > span {
    font-size: 12px;
    color: var(--color-muted);
  }
  .metric strong {
    display: block;
    font-size: 22px;
    font-weight: 650;
    margin: 12px 0;
  }
  .metric small {
    font-size: 12px;
    font-weight: 400;
  }
  .metric p {
    font-size: 12px;
    margin: 8px 0 0;
  }
  .coverage {
    display: flex;
    gap: 12px 20px;
    flex-wrap: wrap;
    border-top: 1px solid var(--color-border);
    padding-top: 18px;
    margin-top: 22px;
    font-size: 12px;
    color: var(--color-muted);
  }
  .service {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }
  .service a {
    color: inherit;
    overflow-wrap: anywhere;
  }
  .date-note {
    padding: 12px 22px;
    margin: 0;
  }
  .simulation-list {
    list-style: none;
    margin: 20px 0 0;
    padding: 0;
  }
  .simulation-list li + li {
    border-top: 1px solid var(--color-border);
  }
  .reports .simulation-choice {
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 12px;
    padding: 14px 0;
    margin: 0;
    cursor: pointer;
    font-size: 13px;
  }
  .simulation-choice input {
    width: 18px;
    height: 18px;
    flex: 0 0 auto;
    margin: 0;
  }
  .choice-name {
    flex: 1;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .choice-name small {
    display: block;
    color: var(--color-muted);
    margin-top: 4px;
  }
  .choice-amount {
    max-width: 45%;
    text-align: right;
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
  }
  .choice-amount small {
    white-space: nowrap;
  }
  .history-chart {
    margin-top: 28px;
  }
  .chart-list {
    list-style: none;
    padding: 0;
    margin: 0;
    display: grid;
    gap: 10px;
  }
  .chart-list li {
    display: grid;
    grid-template-columns: 62px minmax(24px, 1fr) minmax(100px, auto);
    gap: 12px;
    align-items: center;
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }
  .chart-list li > span:last-child {
    text-align: right;
    overflow-wrap: anywhere;
  }
  .bar-track {
    height: 12px;
    background: var(--color-surface-soft);
    border-radius: 3px;
    overflow: hidden;
  }
  .bar {
    display: block;
    height: 100%;
    background: var(--color-action);
    border-radius: 3px;
  }
  .bar.negative {
    background: var(--color-muted);
  }
  .report-footnote {
    margin: 18px 0 0;
  }
  @media (max-width: 500px) {
    .metric {
      padding: 16px;
    }
    .metric strong {
      font-size: 20px;
    }
    .chart-list li {
      grid-template-columns: 55px minmax(16px, 1fr) minmax(80px, auto);
      gap: 7px;
    }
    .simulation-choice {
      gap: 8px;
    }
    .choice-amount {
      max-width: 40%;
    }
    .date-note {
      padding-inline: 16px;
    }
  }
</style>
