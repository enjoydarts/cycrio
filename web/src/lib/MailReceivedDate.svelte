<script lang="ts">
  import { processingDate } from './mail-processing-text.ts';
  let {
    receivedAt,
    compact = false,
  }: { receivedAt?: string | null; compact?: boolean } = $props();
</script>

<span>
  {#if !compact}メール受信日時：{/if}{#if receivedAt}<time
      datetime={receivedAt}
      title={compact ? 'メール受信日時（日本時間）' : undefined}
      aria-label={compact
        ? `メール受信日時：${processingDate(receivedAt)}（日本時間）`
        : undefined}
      >{processingDate(receivedAt)}<span class="timezone">（日本時間）</span
      ></time
    >{:else}受信日時未取得{/if}
</span>

<style>
  .timezone {
    white-space: nowrap;
  }
</style>
