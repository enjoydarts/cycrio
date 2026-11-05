<script lang="ts">
  import type { ResearchTrace } from './price-research-api.ts';
  import { datetime } from './model-catalog.ts';
  let { trace }: { trace: ResearchTrace } = $props();
  const json = (value: unknown) => JSON.stringify(value, null, 2);
</script>

<div class="trace-document">
  <p>
    取得日時（日本時間）：{datetime(trace.captured_at)}<br
    />保存期限（日本時間）：{datetime(trace.expires_at)}
  </p>
  {#if trace.document.response_truncated}<p role="status">
      応答が長いため切り詰めて保存しています。
    </p>{/if}
  <h3>送信内容</h3>
  <pre>{json(trace.document.request)}</pre>
  <h3>実応答</h3>
  <pre>{trace.document.response}</pre>
  <h3>公式基準</h3>
  <pre>{json(trace.document.reference_prices)}</pre>
  <h3>検証理由</h3>
  <pre>{json(trace.document.validation)}</pre>
</div>

<style>
  .trace-document {
    min-width: 0;
  }
  pre {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    word-break: break-word;
    max-width: 100%;
    max-height: 28rem;
    overflow: auto;
    padding: 12px;
    background: var(--color-background);
  }
</style>
