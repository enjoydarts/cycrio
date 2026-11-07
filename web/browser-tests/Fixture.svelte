<script lang="ts">
  import AdminCredentials from '../src/lib/AdminCredentials.svelte';
  import MailAccounts from '../src/lib/MailAccounts.svelte';
  import { createMailAPI } from '../src/lib/mail-api.ts';
  import { createAPI } from '../src/lib/api.ts';
  import SubscriptionForm from '../src/lib/SubscriptionForm.svelte';
  import PriceResearch from '../src/lib/PriceResearch.svelte';
  import { createCatalogAPI } from '../src/lib/catalog-api.ts';
  import AIUsage from '../src/lib/AIUsage.svelte';
  import AccountSettings from '../src/lib/AccountSettings.svelte';
  import IntegrationSettings from '../src/lib/IntegrationSettings.svelte';
  import { createIntegrationAPI } from '../src/lib/integrations-api.ts';
  import UsageOverview from '../src/lib/UsageOverview.svelte';
  import { report as costReport } from '../src/lib/airuntime-test-fixtures.ts';
  import { createErasureAPI } from '../src/lib/erasure-api.ts';
  import { createAIRuntimeAPI } from '../src/lib/airuntime-api.ts';
  import SubscriptionNameModal from '../src/lib/SubscriptionNameModal.svelte';
  import AnalysisAttach from '../src/lib/AnalysisAttach.svelte';
  import SubscriptionChangeHistory from '../src/lib/SubscriptionChangeHistory.svelte';
  import SubscriptionListPanel from '../src/lib/SubscriptionListPanel.svelte';
  import ReportsPanel from '../src/lib/ReportsPanel.svelte';
  import {
    reportFixture,
    reportContractFixture,
  } from '../src/lib/reports-fixtures.ts';
  import type { Subscription } from '../src/lib/subscriptions-api.ts';
  import type { Report } from '../src/lib/reports-api.ts';
  import Modal from '../src/lib/Modal.svelte';
  import Button from '../src/lib/Button.svelte';
  import AppShell from '../src/lib/AppShell.svelte';
  import AnalysisInbox from '../src/lib/AnalysisInbox.svelte';
  import SubscriptionChangeReview from '../src/lib/SubscriptionChangeReview.svelte';
  import ScheduledChanges from '../src/lib/ScheduledChanges.svelte';
  import ImportLastActivity from '../src/lib/ImportLastActivity.svelte';
  import LibraryPanel from '../src/lib/LibraryPanel.svelte';
  import { createNotificationsAPI } from '../src/lib/notifications-api.ts';
  import { account as mailAccount } from '../src/lib/mail-test-fixtures.ts';
  import { page as fixturePage, goto as fixtureGoto } from './router.svelte.ts';
  import ImportProgressPanel from '../src/lib/ImportProgressPanel.svelte';
  import {
    provideAppSession,
    type AppSession,
  } from '../src/lib/app-context.ts';
  import { subscription } from '../src/lib/subscriptions-fixtures.ts';
  import { subscriptionTitle } from '../src/lib/subscription-title.ts';
  import { gettingStartedFixture } from '../src/lib/getting-started-fixtures.ts';
  import { overviewFixture } from '../src/lib/overview-fixtures.ts';
  import { installAPI, resultID } from './synthetic-api.ts';
  import '../src/lib/brand.css';
  import '../src/lib/app.css';
  import '../src/lib/daily.css';

  const mode = new URL(location.href).searchParams.get('mode') ?? 'register';
  const initial = {
    ...subscription(),
    service: 'Example Mail',
    contract_kind: 'recurring' as const,
    amount: '9',
    currency: 'USD',
    interval_unit: null,
    interval_count: null,
    next_billing_date: null,
    next_billing_mode: 'manual' as const,
    manual_next_billing_date: null,
    trial_end_date: null,
    auto_renew: null,
  };
  let notificationUser = $state({ id: initial.id, role: 'user' as const });
  let current = $state({ ...initial, display_name: null as string | null });
  let nameOpen = $state(true);
  let withdrawn = $state(false);
  let scheduledSaved = $state(false);
  const event = {
    service: 'Example Mail',
    plan: null,
    event_type: 'invoice' as const,
    amount: '9',
    currency: 'USD',
    recurring_amount: mode === 'partial-amount' ? '9' : null,
    recurring_currency: mode === 'partial-currency' ? 'USD' : null,
    contract_kind: 'recurring' as const,
    interval_unit: null,
    interval_count: null,
    next_billing_at: null,
    effective_date: null,
    access_end_date: null,
    billed_at: null,
    period_start: null,
    period_end: null,
  };
  const denied = () => {
    throw Error('Unexpected synthetic access denial');
  };
  provideAppSession({
    user: null,
    scope: 1,
    token: async () => 'synthetic',
    onDenied: denied,
    mailAPI: {
      list: async () => [
        mailAccount({ id: initial.id, label: '合成接続', state: 'ready' }),
      ],
    },
  } as AppSession);
  const changeInitial: Subscription = {
    ...initial,
    interval_unit: 'month',
    interval_count: 1,
    next_billing_date: '2027-11-01',
    manual_next_billing_date: '2027-11-01',
    auto_renew: mode === 'change-lifecycle' ? true : null,
  };
  const lifecycleInitial: Subscription = {
    ...initial,
    status: 'ended',
    next_billing_date: null,
    manual_next_billing_date: '2027-11-01',
    auto_renew: false,
  };
  const lifecycleReport: Report = reportFixture();
  lifecycleReport.contracts = [
    {
      ...reportContractFixture(),
      access_end_date: '2026-10-31',
      trial_end_date: null,
      auto_renew: false,
      monthly_amount: null,
      annual_amount: null,
      exclusion_reason: 'access_ended',
    },
  ];
  lifecycleReport.coverage = {
    ...lifecycleReport.coverage,
    total_contracts: 1,
    open_contracts: 1,
    active_contracts: 1,
  };
  let changeSaved = $state<Subscription | null>(null);
  installAPI(
    mode,
    mode.startsWith('change')
      ? changeInitial
      : mode === 'lifecycle-raw-edit'
        ? lifecycleInitial
        : initial,
  );
  window.browserTest.switchNotificationOwner = () => {
    window.browserTest.ownerGeneration = 2;
    notificationUser = {
      id: '22222222-2222-4222-8222-222222222222',
      role: 'user',
    };
  };
  const saved = (value: Subscription) => {
    window.browserTest.saved = value;
    if (mode.startsWith('change')) changeSaved = value;
  };
  // Relative timestamps keep waiting indicators valid on every CI date.
  const start = '2026-11-01T12:00:00.000Z';
  const success = '2026-11-01T12:05:00.000Z';
  const next = new Date(Date.now() + 300000).toISOString();
  const accounts = ['fastmail', 'gmail'].map((provider, i) => ({
    id: `${i + 1}1111111-1111-4111-8111-111111111111`,
    label: `合成接続${i + 1}`,
    provider,
    state: 'ready',
    last_error: '',
  }));
  const states = accounts.map((a, i) => ({
    ...gettingStartedFixture().automation!,
    account_id: a.id,
    state: i ? ('enabled' as const) : ('stopped' as const),
    reason: i ? '' : 'tls_failed',
    running: false,
    last_started_at: start,
    last_success_at: success,
    next_check_at: next,
    progress: {
      bootstrap_complete: true,
      historical_complete: true,
      discovered_count: 10,
      pending_count: 0,
      completed_count: 10,
      failed_count: 0,
      unknown_count: 0,
    },
    recent_sync_runs: [],
  }));
  const overview = {
    ...overviewFixture(),
    connections: accounts.map((a, i) => ({
      ...a,
      automation_state: states[i].state,
      automation_reason: states[i].reason,
      automation_running: false,
      ready: true,
      blocked_reasons: [],
      last_started_at: start,
      last_success_at: success,
      next_check_at: next,
      retry_at: null,
    })),
  };
</script>

<svelte:window
  onclick={(event) => {
    if (!mode.startsWith('notifications') || event.defaultPrevented) return;
    const anchor = (event.target as HTMLElement)?.closest?.('a');
    if (!anchor) return;
    const target = new URL(anchor.href, location.href);
    if (
      target.origin === location.origin &&
      ['/analysis', '/subscriptions', '/import'].includes(target.pathname)
    ) {
      event.preventDefault();
      void fixtureGoto(target.pathname + target.search);
    }
  }}
/>

{#if mode === 'mail-frankly'}
  <main>
    <MailAccounts
      api={createMailAPI('/synthetic')}
      token={async () => 'synthetic'}
      onDenied={denied}
    />
  </main>
{:else if mode.startsWith('scheduled-')}
  <main class="daily library-page">
    <LibraryPanel
      ><div class="library-content">
        {#if mode === 'scheduled-list'}<ScheduledChanges
            id={initial.id}
            onFrozen={() => {}}
            onChanged={() => {}}
          />
        {:else}<SubscriptionChangeReview
            subscription={initial}
            source={{ source_result_id: resultID, source_event_index: 0 }}
            onSaved={(outcome) => {
              window.browserTest.saved =
                outcome.current ??
                ('receipt' in outcome
                  ? outcome.receipt.after
                  : outcome.schedule.preview.subscription);
              scheduledSaved = true;
            }}
            onBackToResults={() => {}}
          />{/if}
        {#if scheduledSaved}<p role="status" data-scheduled-saved>
            予約を保存しました。現在の契約料金は変更していません。
          </p>{/if}
      </div></LibraryPanel
    >
  </main>
{:else if mode === 'import-activity'}
  <main class="daily library-page">
    <LibraryPanel>
      <div class="library-toolbar"><strong>合成接続の取り込み履歴</strong></div>
      <div class="library-content">
        <ImportLastActivity
          syncStartedAt="2026-11-03T14:59:58Z"
          analysisStartedAt="2026-11-03T15:00:01Z"
          syncActivity={{ state: 'completed', added_count: 0 }}
          analysisActivity={{ state: 'completed_with_failures' }}
          automationActivity={{
            state: 'enabled',
            next_check_at: '2026-11-03T16:00:01Z',
            retry_at: null,
            busy: false,
          }}
        />
      </div>
    </LibraryPanel>
  </main>
{:else if mode === 'healthchecks' || mode === 'posthog'}
  <main>
    <AdminCredentials
      api={createAPI('/synthetic')}
      token={async () => 'synthetic'}
      onDenied={denied}
    />
  </main>
{:else if mode === 'price-research'}
  <main>
    <PriceResearch
      catalog={createCatalogAPI('/synthetic')}
      apiBase="/synthetic"
      token={async () => 'synthetic'}
      onDenied={denied}
      onSync={() => {}}
    />
  </main>
{:else if mode === 'integration-profile'}
  <main>
    <IntegrationSettings
      api={createIntegrationAPI('/synthetic')}
      token={async () => 'synthetic'}
      onDenied={denied}
    />
  </main>
{:else if mode.startsWith('erasure')}
  <main>
    {#if withdrawn}<p role="status">サインアウトしました</p>{:else}
      <AccountSettings
        api={createErasureAPI('/synthetic')}
        token={async () => 'synthetic'}
        signOut={() => {
          window.browserTest.signedOut = true;
          withdrawn = true;
        }}
      />
    {/if}
  </main>
{:else if mode === 'anonymous-costs'}
  <main>
    <UsageOverview report={{ ...costReport(), anonymized: [] } as never} />
  </main>
{:else if mode.startsWith('budget')}
  <main>
    <AIUsage
      api={createAIRuntimeAPI('/synthetic')}
      token={async () => 'synthetic'}
      onDenied={denied}
    />
  </main>
{:else if mode.startsWith('notifications')}
  <AppShell
    user={notificationUser}
    pathname={fixturePage.url.pathname}
    search={fixturePage.url.search}
    signOut={() => {}}
    token={async () => 'synthetic'}
    notificationsAPI={createNotificationsAPI('/synthetic')}
    mailAPI={{
      list: async () => [
        mailAccount({ id: initial.id, label: '合成接続', state: 'ready' }),
      ],
    }}
    overviewAPI={{ read: async () => overview } as never}
  >
    {#if fixturePage.url.pathname === '/analysis'}<AnalysisInbox />{:else}<p>
        合成通知の確認画面
      </p>{/if}
  </AppShell>
{:else if mode === 'status'}
  <AppShell
    user={{ id: initial.id, role: 'user' }}
    pathname="/analysis"
    search="?tab=history"
    signOut={() => {}}
    token={async () => 'synthetic'}
    mailAPI={{ list: async () => accounts } as never}
    overviewAPI={{ read: async () => overview } as never}
  >
    <main class="daily">
      <ImportProgressPanel
        entries={accounts.map((a, i) => ({ label: a.label, state: states[i] }))}
      />
    </main>
  </AppShell>
{:else if mode === 'attach' || mode.startsWith('change')}
  <Modal open title="登録候補を確認" onclose={() => {}}>
    {#if changeSaved}<SubscriptionChangeHistory
        id={changeSaved.id}
      />{:else}<AnalysisAttach
        result={{ id: resultID }}
        index={0}
        onSaved={saved}
        onBackToResults={() => {
          window.browserTest.back = true;
        }}
      />{/if}
  </Modal>
{:else if mode === 'lifecycle-labels'}
  <main class="daily">
    <SubscriptionListPanel
      items={[
        {
          ...initial,
          trial_end_date: '2040-02-29',
          auto_renew: false,
          access_end_date: '2040-03-01',
        },
      ]}
      onSelect={() => {}}
    /><SubscriptionListPanel
      layout="cards"
      items={[
        {
          ...initial,
          trial_end_date: '2040-02-29',
          auto_renew: false,
          access_end_date: '2020-03-01',
        },
      ]}
      onSelect={() => {}}
    />
  </main>
{:else if mode === 'lifecycle-report'}
  <main class="daily">
    <ReportsPanel
      view={{ page: lifecycleReport, status: 'ready', error: null }}
      month="2026-11"
      currentMonth="2026-11"
    />
  </main>
{:else if mode === 'name'}
  <main class="daily">
    <h1 data-title>{subscriptionTitle(current)}</h1>
    <Button type="button" onclick={() => (nameOpen = true)}>名前を再変更</Button
    >
    {#if nameOpen}<SubscriptionNameModal
        subscription={current}
        base="/synthetic"
        token={async () => 'synthetic'}
        onclose={() => (nameOpen = false)}
        onSaved={(value) => {
          current = value as typeof current;
          nameOpen = false;
          saved(value as typeof initial);
        }}
        onDenied={denied}
        onFrozen={() => {}}
        onReload={() => {}}
      />{/if}
  </main>
{:else}
  <main class="daily">
    <SubscriptionForm
      initial={mode === 'edit'
        ? initial
        : mode === 'lifecycle-raw-edit'
          ? lifecycleInitial
          : null}
      event={mode.startsWith('partial') || mode === 'transaction'
        ? event
        : null}
      onSaved={saved}
    />
  </main>
{/if}

<style>
  main {
    max-width: 800px;
    margin: auto;
    padding: 16px;
    box-sizing: border-box;
  }
</style>
