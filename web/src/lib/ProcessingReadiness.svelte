<script lang="ts">
  import type { Role } from './api.ts';
  import type { ProcessingPreview } from './mail-processing-api.ts';
  import { processingAction, analysisSetupLink } from './setup-readiness.ts';
  import { processingBlocked } from './mail-processing-text.ts';
  let {
    reason,
    role = 'user',
    returnTo = '/analysis',
    account = '',
    maximum = '',
    busy = false,
    uncertain = false,
    conflict = false,
  }: {
    reason: ProcessingPreview['blocked_reason'];
    role?: Role;
    returnTo?: string;
    account?: string;
    maximum?: string;
    busy?: boolean;
    uncertain?: boolean;
    conflict?: boolean;
  } = $props();
  const action = $derived(processingAction(reason, role === 'platform_admin'));
</script>

{#if uncertain}<p class="readiness" role="status">
    受付確認が終わるまで同意と条件変更はできません。「同じ解析要求を再送」で受付を確認してください。
  </p>{:else if busy}<p class="readiness" role="status">
    現在の条件・受付状況を確認しています。確認が終わると同意できます。
  </p>{:else if conflict}<p class="readiness" role="status">
    送信条件が変わったため同意できません。最新条件を読み込み、入力の基準を更新してください。
  </p>{:else if reason}<div class="readiness" role="status">
    <p>{processingBlocked[reason]}</p>
    {#if action?.href}<a
        href={analysisSetupLink(action.href, returnTo, account, maximum)}
        >{action.label}</a
      >{:else if action}<p>{action.label}</p>{/if}
  </div>{/if}

<style>
  .readiness {
    margin: 0.8rem 0;
    padding: 0.8rem 1rem;
    border-left: 3px solid var(--color-action);
    background: var(--color-surface-soft);
    font-size: 0.85rem;
    line-height: 1.8;
    overflow-wrap: anywhere;
  }
  .readiness p {
    margin: 0 0 0.4rem;
  }
</style>
