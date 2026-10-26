<script lang="ts">
  import { getAppSession } from './app-context.ts';
  import type { ThemePreference } from './theme.ts';
  const session = getAppSession();
  const options: { value: ThemePreference; label: string }[] = [
    { value: 'light', label: 'ライト' },
    { value: 'dark', label: 'ダーク' },
    { value: 'system', label: 'システム' },
  ];
</script>

<section aria-label="表示テーマ">
  <h2>表示テーマ</h2>
  <p>
    この端末で使う配色を選びます。システムでは端末の設定に合わせて切り替わります。
  </p>
  <div class="theme-options" role="group" aria-label="表示テーマ">
    {#each options as option}<button
        class:active={session.theme === option.value}
        aria-pressed={session.theme === option.value}
        onclick={() => session.setTheme(option.value)}>{option.label}</button
      >{/each}
  </div>
</section>

<style>
  section {
    border-bottom: 1px solid var(--color-border);
    padding-bottom: 28px;
  }
  h2 {
    margin: 0 0 8px;
    font-size: 16px;
  }
  p {
    margin: 0 0 20px;
    color: var(--color-muted);
  }
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
