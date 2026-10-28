<script lang="ts">
  import { onMount } from 'svelte';
  import type { TokenProvider } from './api.ts';
  import type { FeedView } from './daily-controller.ts';
  import {
    createGettingStartedController,
    type GettingStartedPage,
    type GettingStartedPorts,
  } from './getting-started.ts';
  import GettingStartedPanel from './GettingStartedPanel.svelte';
  let {
    api,
    token,
    admin = false,
    onDenied,
    onUpdated,
    initialAccount = '',
    onAccountChange = () => {},
  }: {
    api: GettingStartedPorts;
    token: TokenProvider;
    admin?: boolean;
    onDenied: () => void;
    onUpdated?: () => void;
    initialAccount?: string;
    onAccountChange?: (id: string) => void;
  } = $props();
  let view = $state<FeedView<GettingStartedPage>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let controller: ReturnType<typeof createGettingStartedController> | undefined;
  onMount(() => {
    controller = createGettingStartedController(
      api,
      token,
      (next) => {
        view = next;
        if (next.status === 'ready')
          onAccountChange(next.page?.selected?.id ?? '');
      },
      onDenied,
    );
    void controller.load(initialAccount);
    return () => controller?.dispose();
  });
  function refresh() {
    if (onUpdated) onUpdated();
    else void controller?.load(view.page?.selected?.id ?? '');
  }
</script>

<GettingStartedPanel
  {view}
  {admin}
  onSelect={(id) => {
    void controller?.load(id);
  }}
  onRefresh={refresh}
/>
