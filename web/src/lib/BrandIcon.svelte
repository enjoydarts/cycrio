<script lang="ts">
  import { resolveMailLogo, resolveServiceLogo } from './service-logos.ts';

  let {
    name,
    provider,
    size = 'large',
  }: {
    name?: string | null;
    provider?: string;
    size?: 'small' | 'medium' | 'large';
  } = $props();

  const logo = $derived(
    provider !== undefined
      ? resolveMailLogo(provider)
      : resolveServiceLogo(name),
  );
  const pixels = $derived({ small: 28, medium: 32, large: 42 }[size]);
  let failed = $state(false);
  $effect(() => {
    name;
    provider;
    failed = false;
  });
</script>

<span
  class="brand-icon"
  class:small={size === 'small'}
  class:medium={size === 'medium'}
  class:branded={logo !== null && !failed}
  class:dark={logo?.background === 'dark' && !failed}
  aria-hidden="true"
>
  {#if logo && !failed}
    <img
      src={logo.src}
      alt=""
      width={pixels}
      height={pixels}
      onerror={() => (failed = true)}
    />
  {:else}
    <svg width={pixels} height={pixels} viewBox="0 0 24 24" fill="none">
      {#if provider !== undefined}
        <rect x="3" y="5" width="18" height="14" rx="3" />
        <path d="m4 7 8 6 8-6" />
      {:else}
        <rect x="4" y="3" width="16" height="18" rx="3" />
        <path d="M8 8h8M8 12h8M8 16h4" />
      {/if}
    </svg>
  {/if}
</span>

<style>
  .brand-icon {
    display: inline-grid;
    place-items: center;
    width: 42px;
    height: 42px;
    flex: 0 0 auto;
    border-radius: 11px;
    background: var(--color-surface-soft);
    color: var(--color-action);
    vertical-align: middle;
  }
  .brand-icon.small {
    width: 28px;
    height: 28px;
    border-radius: 7px;
  }
  .brand-icon.medium {
    width: 32px;
    height: 32px;
    border-radius: 8px;
  }
  .brand-icon.branded {
    background: #fff;
    box-shadow: inset 0 0 0 1px #00000014;
  }
  .brand-icon.branded.dark {
    background: #161b22;
  }
  img {
    display: block;
    width: 72%;
    height: 72%;
    object-fit: contain;
  }
  svg {
    width: 65%;
    height: 65%;
    stroke: currentColor;
    stroke-width: 1.6;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
</style>
