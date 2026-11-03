<script lang="ts">
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import {
    createReportsAPI,
    createReportsController,
    type Report,
  } from './reports-api.ts';
  import type { FeedView } from './daily-controller.ts';
  import ReportsPanel from './ReportsPanel.svelte';
  import {
    createExchangeRatesAPI,
    createExchangeRatesController,
    type ExchangeRates,
  } from './exchange-rates-api.ts';
  import './daily.css';
  const session = getAppSession(),
    api = createReportsAPI(PUBLIC_API_BASE_URL);
  const currentMonth = new Intl.DateTimeFormat('sv-SE', {
    timeZone: 'Asia/Tokyo',
    year: 'numeric',
    month: '2-digit',
  }).format(new Date());
  let month = $state(currentMonth),
    view = $state<FeedView<Report>>({
      page: null,
      status: 'idle',
      error: null,
    });
  let controller: ReturnType<typeof createReportsController> | undefined;
  let ratesView = $state<FeedView<ExchangeRates>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let ratesController:
    ReturnType<typeof createExchangeRatesController> | undefined;
  function denied() {
    controller?.dispose();
    ratesController?.dispose();
    session.onDenied();
  }
  function reload() {
    void controller?.load(month);
    void ratesController?.load();
  }
  function selectMonth(value: string) {
    month = value;
    reload();
  }
  onMount(() => {
    controller = createReportsController(
      api,
      session.token,
      (next) => (view = next),
      denied,
    );
    ratesController = createExchangeRatesController(
      createExchangeRatesAPI(PUBLIC_API_BASE_URL),
      session.token,
      (next) => (ratesView = next),
      denied,
    );
    reload();
    return () => {
      controller?.dispose();
      ratesController?.dispose();
    };
  });
</script>

<ReportsPanel
  {view}
  {month}
  {currentMonth}
  onMonthChange={selectMonth}
  {ratesView}
  onReload={reload}
  onRatesReload={() => void ratesController?.load()}
/>
