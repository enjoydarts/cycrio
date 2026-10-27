<script lang="ts">
  import type { Role } from './api.ts';
  import { availableSettings } from './settings.ts';
  let { role, section }: { role: Role; section: string } = $props();
</script>

<nav class="settings-navigation" aria-label="設定項目">
  <a href="/settings">準備状況</a>
  {#each availableSettings(role).filter( (item) => ['mail', 'appearance', 'ai'].includes(item.id) ) as item}
    <a
      href={`/settings/${item.id}`}
      aria-current={section === item.id ? 'page' : undefined}>{item.title}</a
    >
  {/each}
  {#if role === 'platform_admin'}<details>
      <summary>管理の詳細</summary
      >{#each availableSettings(role).filter( (item) => ['credentials', 'catalog', 'usage'].includes(item.id) ) as item}<a
          href={`/settings/${item.id}`}
          aria-current={section === item.id ? 'page' : undefined}
          >{item.title}</a
        >{/each}
    </details>{/if}
</nav>

<style>
  .settings-navigation {
    display: flex;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
  }
  a {
    display: flex;
    align-items: center;
    padding: 10px 12px;
    min-height: 44px;
    color: var(--color-muted);
    border-radius: 5px;
    text-decoration: none;
    font-size: 13px;
    overflow-wrap: anywhere;
  }
  a[aria-current='page'] {
    background: var(--color-surface-soft);
    color: var(--color-text);
    font-weight: 600;
  }
  summary {
    color: var(--color-muted);
    font-size: 13px;
    padding: 10px 12px;
    min-height: 44px;
  }
  @media (max-width: 900px) {
    .settings-navigation {
      flex-direction: row;
      flex-wrap: wrap;
      border-bottom: 1px solid var(--color-border);
      padding-bottom: 10px;
      margin-bottom: 24px;
      gap: 4px;
    }
    a {
      padding: 8px 10px;
    }
  }
</style>
