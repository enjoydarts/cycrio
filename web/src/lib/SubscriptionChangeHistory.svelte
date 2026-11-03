<script lang="ts">
  import { onMount } from 'svelte';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  import { getAppSession } from './app-context.ts';
  import {
    createSubscriptionChangesAPI,
    type ChangePage,
  } from './subscription-changes-api.ts';
  import { createFeed, type FeedView } from './daily-controller.ts';
  import {
    changeLabels,
    changeValue,
    subscriptionChangeValue,
  } from './subscription-change-text.ts';
  import { processingDate, processingErrors } from './mail-processing-text.ts';
  import Button from './Button.svelte';
  import Pagination from './Pagination.svelte';
  import DataTable from './DataTable.svelte';
  import ActionGroup from './ActionGroup.svelte';
  import EventFields from './EventFields.svelte';
  import MailReceivedDate from './MailReceivedDate.svelte';
  let { id }: { id: string } = $props();
  const session = getAppSession(),
    api = createSubscriptionChangesAPI(PUBLIC_API_BASE_URL);
  let view = $state<FeedView<ChangePage>>({
    page: null,
    status: 'idle',
    error: null,
  });
  let offset = $state(0);
  const feed = createFeed(
    (next: number, token, signal) => api.list(id, next, token, signal),
    session.token,
    (next) => (view = next),
    session.onDenied,
  );
  function load(next = 0) {
    offset = next;
    void feed.load(next);
  }
  onMount(() => {
    load();
    return () => feed.dispose();
  });
</script>

<section class="change-history" aria-label="契約変更の承認履歴">
  <h2>契約変更の承認履歴</h2>
  <p class="muted">
    メールの差分から本人が選択して反映した変更です。元の抽出情報と変更前後の契約内容を保持します。
  </p>
  {#if view.status === 'loading'}<p role="status">履歴を読み込んでいます…</p>
  {:else if view.error}<p role="alert">{processingErrors[view.error]}</p>
    <Button type="button" wrap onclick={() => load(offset)}
      >履歴を再読み込み</Button
    >
  {:else if view.page}
    {#each view.page.changes as receipt}<details>
        <summary
          >{processingDate(receipt.created_at)}（日本時間） · {receipt.selected_fields
            .map((key) => changeLabels[key])
            .join('・')}</summary
        >
        <DataTable responsive="cards"
          ><thead
            ><tr><th>変更項目</th><th>変更前</th><th>変更後</th></tr></thead
          ><tbody>
            {#each receipt.selected_fields as key}<tr
                ><td data-primary>{changeLabels[key]}</td><td
                  data-label="変更前"
                  >{changeValue(
                    key,
                    subscriptionChangeValue(receipt.before, key),
                  )}</td
                ><td data-label="変更後"
                  >{changeValue(
                    key,
                    subscriptionChangeValue(receipt.after, key),
                  )}</td
                ></tr
              >{/each}
          </tbody></DataTable
        >
        {#each [receipt.source, ...receipt.additional_evidence] as evidence}<section
            class="evidence"
          >
            <h3>元の抽出情報 · イベント {evidence.event_index + 1}</h3>
            <p><MailReceivedDate receivedAt={evidence.received_at} /></p>
            <EventFields event={evidence.original_event} />
          </section>{/each}
      </details>{:else}<p class="empty">
        承認した契約変更はありません。
      </p>{/each}
    <ActionGroup
      ><Pagination
        total={view.page.total}
        offset={view.page.offset}
        limit={view.page.limit}
        label="契約変更の承認履歴のページ"
        onchange={load}
      /></ActionGroup
    >
  {/if}
</section>

<style>
  .change-history {
    margin-top: 28px;
    min-width: 0;
  }
  summary {
    cursor: pointer;
    padding: 12px 0;
    overflow-wrap: anywhere;
  }
  .evidence {
    margin: 16px 0;
    overflow-wrap: anywhere;
  }
  h3 {
    font-size: 14px;
  }
</style>
