<script lang="ts">
  import type { Role } from './api.ts';
  import { availableSettings } from './settings.ts';
  let { role, section }: { role: Role; section: string } = $props();
</script>

<nav class="settings-navigation" aria-label="設定項目">
  {#each availableSettings(role) as item}
    {#if item.id === 'ai-models'}<span class="group-label">管理者設定</span
      >{/if}
    <a
      href={`/settings/${item.id}`}
      aria-current={section === item.id ? 'page' : undefined}>{item.title}</a
    >
  {/each}
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
  .group-label {
    color: var(--color-muted);
    font-size: 12px;
    padding: 24px 12px 7px;
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
    .group-label {
      display: none;
    }
  }
</style>
