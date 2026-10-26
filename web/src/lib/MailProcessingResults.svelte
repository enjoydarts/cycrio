<script lang="ts">
  import type {
    ProcessingEvent,
    ProcessingResult,
  } from './mail-processing-api.ts';
  import { processingDate, processingReasons } from './mail-processing-text.ts';
  let {
    results,
    canRetry,
    confirmationScope,
    onRetry,
  }: {
    results: ProcessingResult[];
    canRetry: boolean;
    confirmationScope: string;
    onRetry: (id: string, charge: boolean, unknown: boolean) => void;
  } = $props();
  let charge = $state<Record<string, boolean>>({}),
    unknown = $state<Record<string, boolean>>({});
  $effect(() => {
    void confirmationScope;
    charge = {};
    unknown = {};
  });
  const states = {
    succeeded: 'AI抽出・未確認',
    not_related: '非関連',
    failed: '処理失敗',
    unknown: '結果不明',
  };
  const types: Record<ProcessingEvent['event_type'], string> = {
    invoice: '請求',
    payment: '支払い',
    renewal: '更新',
    refund: '返金',
    change: '変更',
    cancellation: '解約',
    trial: '試用',
  };
  const dateFields = {
    billed_at: '請求日',
    period_start: '対象期間の開始日',
    period_end: '対象期間の終了日',
    next_billing_at: '次回請求日',
  } as const;
  const intervals = { day: '日', week: '週', month: '月', year: '年' };
</script>

<div class="heading">
  <h5>解析結果</h5>
  <span>各メールの最新結果 · 最新100件 · 日本時間</span>
</div>
<p class="help">
  AI抽出・未確認の情報です。請求日・次回日・記載周期はメールから得た候補として確認してください。
</p>
<ol class="results">
  {#each results as result (result.id)}
    <li>
      <div class="heading">
        <strong>{states[result.state]}</strong><time
          datetime={result.finished_at}
          >{processingDate(result.finished_at)}</time
        >
      </div>
      {#if result.state === 'not_related'}<p>
          サブスクリプションに関連しないと判定されました。
        </p>{/if}
      {#if result.classification === 'uncertain'}<p>
          関連性の判定も未確定です。
        </p>{/if}
      {#if result.error_code}<p class="help">
          {processingReasons[result.error_code]}
        </p>{/if}
      {#each result.events as event}
        <article>
          <h6>
            {event.service}{#if event.plan}
              / {event.plan}{/if}
          </h6>
          <p>{types[event.event_type]}の候補</p>
          <dl>
            {#if event.amount !== null}<div>
                <dt>金額（AI抽出）</dt>
                <dd>
                  {event.amount}{#if event.currency !== null}
                    {event.currency}{:else}（通貨不明）{/if}
                </dd>
              </div>{:else if event.currency !== null}<div>
                <dt>通貨（AI抽出）</dt>
                <dd>{event.currency}（金額不明）</dd>
              </div>{/if}
            {#each Object.entries(dateFields) as [field, label]}{#if event[field as keyof typeof dateFields] !== null}<div
                >
                  <dt>{label}（AI抽出）</dt>
                  <dd>{event[field as keyof typeof dateFields]}</dd>
                </div>{/if}{/each}
            {#if event.interval_count !== null || event.interval_unit !== null}<div
              >
                <dt>記載周期（AI抽出）</dt>
                <dd>
                  {event.interval_count ?? '回数不明'}
                  {event.interval_unit
                    ? intervals[event.interval_unit]
                    : '単位不明'}
                </dd>
              </div>{/if}
          </dl>
        </article>
      {/each}
      {#if result.state === 'failed' || result.state === 'unknown'}
        <div class="retry">
          <label
            ><input
              type="checkbox"
              checked={charge[result.id] ?? false}
              disabled={!canRetry}
              onchange={(event) => {
                charge[result.id] = event.currentTarget.checked;
              }}
            />外部に再送し、再び料金が発生する可能性を確認しました</label
          >
          {#if result.state === 'unknown'}<label
              ><input
                type="checkbox"
                checked={unknown[result.id] ?? false}
                disabled={!canRetry}
                onchange={(event) => {
                  unknown[result.id] = event.currentTarget.checked;
                }}
              />前回の結果が不明で、重複して処理・課金される可能性を確認しました</label
            >{/if}
          <button
            type="button"
            disabled={!canRetry ||
              !charge[result.id] ||
              (result.state === 'unknown' && !unknown[result.id])}
            onclick={() => {
              onRetry(result.id, !!charge[result.id], !!unknown[result.id]);
              charge[result.id] = false;
              unknown[result.id] = false;
            }}>このメールだけを再解析</button
          >
        </div>
      {/if}
    </li>
  {:else}<li class="help">解析結果はまだありません。</li>{/each}
</ol>

<style>
  h5,
  h6,
  p,
  dd,
  dl {
    margin: 0;
  }
  h5 {
    font-size: 0.9rem;
  }
  h6 {
    font-size: 0.88rem;
    overflow-wrap: anywhere;
  }
  .heading {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem;
    margin: 0.6rem 0;
  }
  .heading span,
  time,
  .help {
    color: var(--color-muted);
    font-size: 0.75rem;
    line-height: 1.9;
  }
  ol {
    list-style: none;
    margin: 0.8rem 0 0;
    padding: 0;
  }
  li {
    padding: 1rem;
    border: 1px solid var(--color-border);
    border-radius: 0.5rem;
    min-width: 0;
    font-size: 0.82rem;
    overflow-wrap: anywhere;
  }
  li + li {
    margin-top: 0.7rem;
  }
  article,
  .retry {
    margin-top: 1rem;
    padding-top: 0.8rem;
    border-top: 1px solid var(--color-border);
  }
  article p {
    margin: 0.4rem 0;
    color: var(--color-muted);
    font-size: 0.75rem;
  }
  dl {
    display: flex;
    flex-wrap: wrap;
    gap: 0.6rem 1.4rem;
  }
  dt {
    font-size: 0.7rem;
    color: var(--color-muted);
  }
  dd {
    margin-top: 0.3rem;
  }
  label {
    display: flex;
    align-items: flex-start;
    gap: 0.6rem;
    font-size: 0.75rem;
    line-height: 1.8;
    margin: 0.7rem 0;
  }
  input {
    flex: none;
    accent-color: var(--color-action);
    width: 1rem;
    height: 1rem;
    margin-top: 0.15rem;
  }
  button {
    min-height: 2.8rem;
    max-width: 100%;
    padding: 0.6rem 0.8rem;
    border: 1px solid var(--color-action);
    border-radius: 0.5rem;
    color: var(--color-action);
    background: var(--color-surface);
    font: inherit;
    font-size: 0.8rem;
    cursor: pointer;
  }
  button:disabled {
    opacity: 0.55;
    cursor: default;
  }
  button:focus-visible,
  input:focus-visible {
    outline: 2px solid var(--color-action);
    outline-offset: 2px;
  }
  @media (max-width: 400px) {
    li {
      padding: 0.75rem;
    }
    button {
      width: 100%;
    }
  }
</style>
