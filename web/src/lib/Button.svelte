<script lang="ts">
  import type { HTMLButtonAttributes } from 'svelte/elements';
  import type { Snippet } from 'svelte';
  import './action.css';
  let {
    children,
    type = 'submit',
    variant = 'default',
    wrap = false,
    class: className,
    element = $bindable<HTMLButtonElement>(),
    ...attributes
  }: HTMLButtonAttributes & {
    children?: Snippet;
    variant?:
      | 'default'
      | 'primary'
      | 'danger'
      | 'tab'
      | 'icon'
      | 'card'
      | 'text'
      | 'plain'
      | 'overlay';
    element?: HTMLButtonElement;
    wrap?: boolean;
  } = $props();
</script>

<button
  bind:this={element}
  {...attributes}
  {type}
  class={['ui-action', `ui-${variant}`, { 'ui-wrap': wrap }, className]}
  >{#if children}{@render children()}{/if}</button
>
