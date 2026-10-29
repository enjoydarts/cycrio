<script lang="ts">
  import { getAppSession } from './app-context.ts';
  import type { ThemePreference } from './theme.ts';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  const session = getAppSession();
  const options: { value: ThemePreference; label: string }[] = [
    { value: 'light', label: 'ライト' },
    { value: 'dark', label: 'ダーク' },
    { value: 'system', label: 'システム' },
  ];
</script>

<section class="library-page" aria-label="表示テーマ">
  <LibraryHeader
    title="表示テーマ"
    description="この端末で使う配色を選びます。"
  />
  <LibraryPanel padded>
    <div class="theme-options" role="group" aria-label="表示テーマ">
      {#each options as option}<button
          class:active={session.theme === option.value}
          aria-pressed={session.theme === option.value}
          onclick={() => session.setTheme(option.value)}>{option.label}</button
        >{/each}
    </div>
  </LibraryPanel>
</section>

<style>
  .theme-options {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  button {
    padding: 10px 18px;
    border: 1px solid var(--color-border);
    border-radius: 6px;
    background: var(--color-surface);
    color: var(--color-text);
  }
  button.active {
    color: var(--color-action);
    border-color: var(--color-action);
    background: var(--color-surface-soft);
  }
</style>
