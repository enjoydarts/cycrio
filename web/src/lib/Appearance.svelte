<script lang="ts">
  import ActionGroup from './ActionGroup.svelte';
  import Button from './Button.svelte';
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
    <ActionGroup
      class={['theme-options', 'part-Appearance-theme-options']}
      role="group"
      aria-label="表示テーマ"
    >
      {#each options as option}<Button
          wrap
          class={[{ active: session.theme === option.value }]}
          variant="tab"
          aria-pressed={session.theme === option.value}
          onclick={() => session.setTheme(option.value)}>{option.label}</Button
        >{/each}
    </ActionGroup>
  </LibraryPanel>
</section>

<style>
  :global(.part-Appearance-theme-options) {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
</style>
