<script lang="ts">
  import { processingDate } from './mail-processing-text.ts';
  let { receivedAt }: { receivedAt?: string | null } = $props();
</script>

<span>
  メール受信日時：{#if receivedAt}<time datetime={receivedAt}
      >{processingDate(receivedAt)}（日本時間）</time
    >{:else}受信日時未取得{/if}
</span>
