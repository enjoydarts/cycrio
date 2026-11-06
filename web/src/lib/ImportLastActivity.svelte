<script lang="ts">
  let {
    syncStartedAt,
    analysisStartedAt,
  }: {
    syncStartedAt?: string | null;
    analysisStartedAt?: string | null;
  } = $props();
  const date = new Intl.DateTimeFormat('ja-JP', {
    timeZone: 'Asia/Tokyo',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  });
  const activity = $derived([
    { label: '最終取り込み', at: syncStartedAt },
    { label: '最終解析', at: analysisStartedAt },
  ]);
</script>

<section class="last-activity" aria-label="このメール接続の最終実行日時">
  <p>実行開始日時（日本時間）</p>
  <dl>
    {#each activity as item (item.label)}
      <div>
        <dt>{item.label}</dt>
        <dd>
          {#if item.at}<time datetime={item.at}
              >{date.format(new Date(item.at))}</time
            >
          {:else}{item.at === null ? '未実行' : '記録なし'}{/if}
        </dd>
      </div>
    {/each}
  </dl>
</section>

<style>
  .last-activity {
    padding: 4px 0 20px;
  }
  p {
    margin: 0 0 10px;
    font-size: 12px;
    color: var(--color-muted);
  }
  dl {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
    gap: 12px 24px;
    margin: 0;
  }
  dl > div {
    min-width: 0;
  }
  dt {
    font-size: 12px;
    color: var(--color-muted);
  }
  dd {
    margin: 5px 0 0;
    font-size: 14px;
    font-weight: 500;
    font-variant-numeric: tabular-nums;
    overflow-wrap: anywhere;
  }
</style>
