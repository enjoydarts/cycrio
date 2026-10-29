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
    background: var(--brand-soft, #f0ecff);
    color: var(--brand, #7361cb);
    font-weight: 700;
    font-size: 20px;
  }
</style>
