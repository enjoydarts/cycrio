<script lang="ts">
  import ActionLink from './ActionLink.svelte';
  import type { Role } from './api.ts';
  import {
    settingsNavigationGroups,
    settingsNavigationSection,
  } from './settings.ts';
  let {
    role,
    section,
    variant = 'sidebar',
  }: { role: Role; section: string; variant?: 'sidebar' | 'list' } = $props();
  const selected = $derived(settingsNavigationSection(section));
</script>

<nav class="settings-navigation {variant}" aria-label="設定項目">
  {#if variant === 'sidebar'}
    <ActionLink
      wrap
      variant="nav"
      href="/settings"
      aria-current={section === '' ? 'page' : undefined}>準備状況</ActionLink
    >
  {/if}
  {#each settingsNavigationGroups(role) as group (group.id)}
    <div class="settings-group">
      <p class="group-title">{group.title}</p>
      <div class="group-items">
        {#each group.items as item (item.id)}
          <ActionLink
            wrap
            variant="nav"
            href={`/settings/${item.id}`}
            aria-current={selected === item.id ? 'page' : undefined}
          >
            <span>{item.title}</span>{#if variant === 'list'}<span
                aria-hidden="true">›</span
              >{/if}
          </ActionLink>
        {/each}
      </div>
    </div>
  {/each}
</nav>

<style>
  .settings-navigation {
    min-width: 0;
  }
  .sidebar {
    padding: 8px 0 0 12px;
  }
  .sidebar :global(.ui-nav span) {
    text-wrap: balance;
  }
  .settings-group {
    margin-top: 14px;
  }
  .group-title {
    margin: 0 0 6px;
    padding-inline: 10px;
    color: var(--color-muted);
    font-size: 11px;
    font-weight: 600;
  }
  .group-items {
    display: grid;
    gap: 3px;
  }
  .settings-navigation :global(.ui-nav) {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 10px;
    min-height: 44px;
    color: var(--color-muted);
    border-radius: 6px;
    text-decoration: none;
    font-size: 13px;
    overflow-wrap: anywhere;
  }
  .settings-navigation :global(.ui-nav[aria-current='page']) {
    background: var(--color-surface-soft);
    color: var(--color-action);
    font-weight: 600;
  }
  .list .group-title {
    padding: 0;
    font-size: 13px;
  }
  .list .group-items {
    gap: 8px;
  }
  .list :global(.ui-nav) {
    justify-content: space-between;
    border: 1px solid var(--color-border);
    background: var(--color-surface);
    padding: 12px 14px;
    font-size: 14px;
  }
  @media (max-width: 700px) {
    .sidebar {
      display: none;
    }
  }
</style>
