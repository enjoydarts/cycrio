<script lang="ts">
  let { name }: { name: string } = $props();
  const initial = $derived([...name.trim()][0]?.toUpperCase() ?? '?');
</script>

<span class="avatar" aria-hidden="true">{initial}</span>

<style>
  .avatar {
    width: 42px;
    height: 42px;
    border-radius: 11px;
    display: inline-grid;
    place-items: center;
    flex-shrink: 0;
    background: var(--color-surface-soft);
    color: var(--color-action);
    font-weight: 700;
    font-size: 20px;
  }
</style>
