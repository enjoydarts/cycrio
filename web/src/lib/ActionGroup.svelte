<script lang="ts">
  import type { HTMLAttributes } from 'svelte/elements';
  import type { Snippet } from 'svelte';
  let {
    children,
    class: className,
    stack = false,
    align = 'start',
    ...attributes
  }: HTMLAttributes<HTMLDivElement> & {
    children: Snippet;
    stack?: boolean;
    align?: 'start' | 'end' | 'between';
  } = $props();
</script>

<div
  {...attributes}
  class={['ui-actions', className]}
  class:stack
  class:end={align === 'end'}
  class:between={align === 'between'}
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
  .end {
    justify-content: flex-end;
  }
  .between {
    justify-content: space-between;
  }
</style>
