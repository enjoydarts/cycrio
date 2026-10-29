<script lang="ts">
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import {
    createOverviewAPI,
    createOverviewController,
    type OverviewReport,
  } from './overview-api.ts';
  import type { FeedView } from './daily-controller.ts';
  import OverviewPanel from './OverviewPanel.svelte';
  import './daily.css';
  import './billing.css';
  const session = getAppSession(),
    token = session.token,
    api = createOverviewAPI(PUBLIC_API_BASE_URL);
  let controller: ReturnType<typeof createOverviewController> | undefined;
  let view = $state<FeedView<OverviewReport>>({
    page: null,
    status: 'idle',
    error: null,
  });
  onMount(() => {
    controller = createOverviewController(
      api,
      token,
      (next) => (view = next),
      session.onDenied,
    );
    void controller.load();
    return () => controller?.dispose();
  });
</script>

<OverviewPanel
  {view}
  onReload={() => {
    void controller?.load();
  }}
/>
