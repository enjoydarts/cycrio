<script lang="ts">
  import SubscriptionForm from '../src/lib/SubscriptionForm.svelte';
  import SubscriptionNameModal from '../src/lib/SubscriptionNameModal.svelte';
  import AnalysisAttach from '../src/lib/AnalysisAttach.svelte';
  import Modal from '../src/lib/Modal.svelte';
  import Button from '../src/lib/Button.svelte';
  import AppShell from '../src/lib/AppShell.svelte';
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
  };
  let current = $state({ ...initial, display_name: null as string | null });
  let nameOpen = $state(true);
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
  } as AppSession);
  installAPI(mode, initial);
  const saved = (value: typeof initial) => {
    window.browserTest.saved = value;
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

{#if mode === 'status'}
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
{:else if mode === 'attach'}
  <Modal open title="登録候補を確認" onclose={() => {}}>
    <AnalysisAttach
      result={{ id: resultID }}
      index={0}
      onSaved={saved}
      onBackToResults={() => {}}
    />
  </Modal>
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
      initial={mode === 'edit' ? initial : null}
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
