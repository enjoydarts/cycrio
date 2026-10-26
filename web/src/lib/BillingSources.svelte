<script lang="ts">
  import type { BillingEntry, BillingSource } from './billing-api.ts';
  import type {
    BillingEditorView,
    createBillingEditor,
  } from './billing-controller.ts';
  import EventFields from './EventFields.svelte';
  let {
    view,
    controller,
    sources,
    frozen,
    onSaved,
  }: {
    view: BillingEditorView;
    controller: ReturnType<typeof createBillingEditor> | undefined;
    sources: BillingSource[];
    frozen: boolean;
    onSaved: (entry: BillingEntry) => void;
  } = $props();
  let selected = $state<BillingSource | null>(null),
    operation = $state<'attach' | 'detach'>('attach'),
    confirmed = $state(false);
  const available = $derived(
    sources.filter(
      (source) =>
        !view.baseline?.sources.some(
          (s) =>
            s.result_id === source.result_id &&
            s.event_index === source.event_index,
        ),
    ),
  );
  async function save() {
    if (!selected || !confirmed || frozen) return;
    await controller?.changeSource(operation, selected, true);
    const saved = controller?.state().saved;
    if (saved) onSaved(saved);
    confirmed = false;
  }
</script>

<section class="section billing-sources">
  <h3>この履歴の根拠</h3>
  <p class="muted">
    紐付けを外しても、元の AI
    抽出情報と契約への紐付けは保持します。別の明細へ移す場合は、ここで外してから移動先へ紐付けてください。
  </p>
  {#each view.baseline?.sources ?? [] as source (`${source.result_id}/${source.event_index}`)}
    <details>
      <summary
        >元の AI 抽出値 · イベント {source.event_index + 1}<span
          class="secondary billing-id">{source.result_id}</span
        ></summary
      ><EventFields event={source.original_event} />
      <button
        type="button"
        disabled={frozen}
        onclick={() => {
          selected = source;
          operation = 'detach';
          confirmed = false;
        }}>この根拠の紐付けを外す</button
      >
    </details>
  {:else}<p class="empty">この履歴に紐付いた根拠はありません。</p>{/each}
  {#if available.length}<label
      >契約に紐付いている根拠を追加<select
        disabled={frozen}
        value={operation === 'attach' && selected
          ? `${selected.result_id}/${selected.event_index}`
          : ''}
        onchange={(event) => {
          selected =
            available.find(
              (s) =>
                `${s.result_id}/${s.event_index}` === event.currentTarget.value,
            ) ?? null;
          operation = 'attach';
          confirmed = false;
        }}
        ><option value="">根拠を選択</option>{#each available as source}<option
            value={`${source.result_id}/${source.event_index}`}
            >{source.original_event.service} · {source.original_event
              .billed_at ?? '日付不明'} · イベント {source.event_index +
              1}</option
          >{/each}</select
      ></label
    >{/if}
  {#if selected}<div class="billing-confirm">
      <h4>
        {operation === 'attach' ? '根拠を紐付ける' : '根拠の紐付けを外す'}
      </h4>
      <p class="billing-id">
        {selected.result_id} · イベント {selected.event_index + 1}
      </p>
      <EventFields event={selected.original_event} /><label class="check"
        ><input
          type="checkbox"
          bind:checked={confirmed}
          disabled={frozen}
        />この根拠の{operation === 'attach'
          ? '追加'
          : '紐付け解除'}を確認しました。元の AI 抽出情報は保持されます。</label
      ><button type="button" disabled={frozen || !confirmed} onclick={save}
        >{operation === 'attach'
          ? 'この履歴に根拠を紐付ける'
          : '紐付けを外す'}</button
      >
    </div>{/if}
</section>
