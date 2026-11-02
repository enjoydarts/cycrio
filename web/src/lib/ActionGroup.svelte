<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import type { Snippet } from 'svelte';
  let {
    children,
    class: className,
    stack = false,
    align = 'start',
    spacing = 'none',
    ...attributes
  }: HTMLAttributes<HTMLDivElement> & {
    children: Snippet;
    stack?: boolean;
    align?: 'start' | 'end' | 'between';
    spacing?: 'none' | 'before' | 'after' | 'both';
  } = $props();
</script>

<div
  {...attributes}
  class={['ui-actions', className]}
  class:stack
  class:end={align === 'end'}
  class:between={align === 'between'}
  class:space-before={spacing === 'before' || spacing === 'both'}
  class:space-after={spacing === 'after' || spacing === 'both'}
>
  {@render children()}
</div>

<style>
  .ui-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    min-width: 0;
    max-width: 100%;
  }
  .stack {
    flex-direction: column;
    align-items: flex-start;
  }
  .space-before {
    margin-block-start: 16px;
  }
  .space-after {
    margin-block-end: 16px;
  }
  .end {
    justify-content: flex-end;
  }
  .between {
    justify-content: space-between;
  }
</style>
