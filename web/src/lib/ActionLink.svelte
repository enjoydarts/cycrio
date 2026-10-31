<script lang="ts">
  import type { HTMLAnchorAttributes } from 'svelte/elements';
  import type { Snippet } from 'svelte';
  import './action.css';
  let {
    children,
    href,
    disabled = false,
    onclick,
    tabindex,
    variant = 'default',
    wrap = false,
    class: className,
    ...attributes
  }: HTMLAnchorAttributes & {
    children?: Snippet;
    disabled?: boolean;
    wrap?: boolean;
    variant?:
      | 'default'
      | 'primary'
      | 'danger'
      | 'tab'
      | 'icon'
      | 'card'
      | 'text'
      | 'nav';
  } = $props();
</script>

<a
  href={disabled ? undefined : href}
  {...attributes}
  aria-disabled={disabled || undefined}
  tabindex={disabled ? -1 : tabindex}
  class={['ui-action', `ui-${variant}`, { 'ui-wrap': wrap }, className]}
  onclick={(event) => {
    if (disabled) {
      event.preventDefault();
      return;
    }
    onclick?.(event);
  }}
  >{#if children}{@render children()}{/if}</a
>
