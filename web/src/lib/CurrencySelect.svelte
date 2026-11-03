<script lang="ts">
  let {
    value = null,
    disabled = false,
    onchange = () => {},
  }: {
    value?: string | null;
    disabled?: boolean;
    onchange?: (value: string | null) => void;
  } = $props();
  const currencies = ['JPY', 'USD', 'EUR'];
</script>

<select
  aria-label="通貨"
  value={value ?? ''}
  {disabled}
  onchange={(e) => onchange(e.currentTarget.value || null)}
>
  <option value="">不明</option>
  {#each currencies as currency}<option value={currency}>{currency}</option
    >{/each}
  {#if value && !currencies.includes(value)}<option {value}
      >{value}（現在の通貨）</option
    >{/if}
</select>
