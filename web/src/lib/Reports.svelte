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
  function selectMonth(value: string) {
    month = value;
    void controller?.load(value);
  }
  onMount(() => {
    controller = createReportsController(
      api,
      session.token,
      (next) => (view = next),
      session.onDenied,
    );
    void controller.load(month);
    return () => controller?.dispose();
  });
</script>

<ReportsPanel
  {view}
  {month}
  {currentMonth}
  onMonthChange={selectMonth}
  onReload={() => void controller?.load(month)}
/>
