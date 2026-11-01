<script lang="ts">
  import BrandIcon from './BrandIcon.svelte';
  let { name }: { name: string } = $props();
</script>

<BrandIcon {name} />
