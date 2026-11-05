<script lang="ts">
  import type { TokenProvider } from './api.ts';
  import type { PriceResearchAPI } from './price-research-api.ts';
  import {
    createResearchTraceController,
    emptyResearchTraceState,
  } from './price-research-trace-controller.ts';
  import Button from './Button.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import Modal from './Modal.svelte';
  import ResearchTraceDocument from './ResearchTraceDocument.svelte';
  let {
    api,
    token,
    runID,
    onDenied,
  }: {
    api: PriceResearchAPI;
    token: TokenProvider;
    runID: string;
    onDenied: () => void;
  } = $props();
  let view = $state(emptyResearchTraceState());
  let selected = $state('');
  let controller: ReturnType<typeof createResearchTraceController> | undefined;
  $effect(() => {
    const next = createResearchTraceController(
      api,
      token,
      (value) => {
        view = value;
      },
      onDenied,
    );
    // A changed run or unmounted administrator screen purges decrypted content.
    const currentRun = runID;
    void currentRun;
    controller = next;
    view = emptyResearchTraceState();
    selected = '';
    return () => next.dispose();
  });
  async function load() {
    await controller?.load(runID);
    selected = view.calls.find((call) => call.available)?.call_id ?? '';
  }
</script>

<LibraryPanel padded>
  <h4>管理者用の送信・応答履歴</h4>
  <p>
    実際の問い合わせと応答は7日間保存します。導入前の記録・期限切れ・保存されていない呼び出しの本文は取得できません。
  </p>
  <ActionGroup spacing="both"
    ><Button wrap type="button" disabled={view.busy} onclick={load}
      >呼び出し履歴を取得</Button
    ></ActionGroup
  >
  {#if view.loaded && view.calls.length}
    <label for="research-trace-call">確認する呼び出し</label>
    <select id="research-trace-call" bind:value={selected} disabled={view.busy}>
      <option value="">呼び出しを選択</option>
      {#each view.calls as call}<option
          value={call.call_id}
          disabled={!call.available}
          >#{call.attempt_no} · {call.call_id} · {call.available
            ? '本文あり'
            : '本文なし・期限切れ'}</option
        >{/each}
    </select>
    <ActionGroup spacing="both"
      ><Button
        wrap
        type="button"
        disabled={view.busy || !selected}
        onclick={() => controller?.fetch(runID, selected)}
        >選択した送信・応答を表示</Button
      ></ActionGroup
    >
  {:else if view.loaded}<p>呼び出し履歴はありません。</p>{/if}
  {#if view.busy}<p role="status">履歴を取得中です。</p>{/if}
  {#if view.error}<p role="alert">
      {view.error === 'not_found'
        ? '本文は保存されていないか、保存期限を過ぎています。'
        : '履歴を取得できませんでした。権限と接続状態を確認してください。'}
    </p>{/if}
</LibraryPanel>
<Modal
  open={!!view.trace}
  title="料金調査の送信・応答履歴"
  onclose={() => controller?.close()}
>
  {#if view.trace}<ResearchTraceDocument trace={view.trace} />{/if}
</Modal>

<style>
  select {
    width: 100%;
    min-width: 0;
    max-width: 100%;
  }
</style>
