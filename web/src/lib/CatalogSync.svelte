<script lang="ts">
  import { tick } from 'svelte';
  import {
    type SyncState,
    type SyncInput,
    type ModelProvider,
  } from './catalog-api';
  import {
    providerNames,
    datetime,
    createRevisionDraft,
    reconcileRevisionDraft,
    revisionInput,
    type RevisionDraft,
  } from './model-catalog';
  let {
    states,
    busy,
    configure,
    request,
    reload,
    onDirty = () => {},
  }: {
    states: SyncState[];
    busy: boolean;
    configure: (provider: ModelProvider, input: SyncInput) => Promise<boolean>;
    request: (provider: ModelProvider) => void;
    reload: () => Promise<boolean>;
    onDirty?: (dirty: boolean) => void;
  } = $props();
  let drafts = $state<
    Partial<
      Record<ModelProvider, RevisionDraft<Omit<SyncInput, 'expected_version'>>>
    >
  >({});
  let selected = $state<ModelProvider | null>(null);
  const values = (row: SyncState) => ({
    automatic_enabled: row.automatic_enabled,
    interval_hours: row.interval_hours,
    workspace_id: row.workspace_id,
  });
  $effect(() => {
    for (const row of states)
      drafts[row.provider] = reconcileRevisionDraft(
        drafts[row.provider],
        row.version,
        values(row),
      );
  });
  $effect(() => {
    onDirty(Object.values(drafts).some((draft) => !!draft?.dirty));
  });
  const statusNames = {
    never: '未実行',
    queued: '実行待ち',
    running: '同期中',
    succeeded: '成功',
    failed: '失敗',
    credentials_required: 'API キーが必要',
    manual_only: '手入力のみ',
  };
  async function submit(event: SubmitEvent, row: SyncState) {
    event.preventDefault();
    const draft = drafts[row.provider];
    if (!draft || busy) return;
    if (
      await configure(row.provider, {
        ...revisionInput(draft),
        workspace_id:
          row.provider === 'anthropic' ? draft.values.workspace_id : '',
      })
    ) {
      await tick();
      resetDraft(row.provider);
    }
  }
  function resetDraft(provider: ModelProvider) {
    const row = states.find((value) => value.provider === provider);
    if (row) drafts[provider] = createRevisionDraft(row.version, values(row));
  }
  async function reloadDraft(provider: ModelProvider) {
    if (await reload()) {
      await tick();
      resetDraft(provider);
    }
  }
</script>

<section aria-label="カタログの同期と自動更新">
  <p class="muted">
    OpenRouter はモデルと価格を取得します。OpenAI / Anthropic は登録済み API
    キーでモデルを取得し、価格は手入力します。Liquid AI は手入力のみです。推論
    API は呼び出しません。
  </p>
  <table aria-label="提供元の同期状態">
    <thead
      ><tr
        ><th>提供元</th><th>状態</th><th>最終成功（日本時間）</th><th
          ><span class="sr-only">操作</span></th
        ></tr
      ></thead
    >
    <tbody
      >{#each states as row (row.provider)}<tr
          ><td><strong>{providerNames[row.provider]}</strong></td><td
            >{statusNames[row.status]}{#if row.error_code}<span class="error">
                · 同期を確認してください</span
              >{/if}</td
          ><td>{datetime(row.last_success_at)}</td><td
            ><button
              disabled={busy}
              aria-label={`${providerNames[row.provider]} の同期設定`}
              aria-pressed={selected === row.provider}
              onclick={() => (selected = row.provider)}
              >{selected === row.provider ? '選択中' : '設定'}</button
            ></td
          ></tr
        >{/each}</tbody
    >
  </table>
  {#if selected === null}<p class="muted">
      提供元を選ぶと、同期の設定と実行状況を確認できます。
    </p>{/if}
  <div>
    {#each states as row (row.provider)}
      {@const draft = drafts[row.provider]}
      <article class="box" hidden={selected !== row.provider}>
        <h4>{providerNames[row.provider]}</h4>
        <p>状態：{statusNames[row.status]}</p>
        {#if row.error_code}<p class="error">
            {row.error_code === 'credentials_required'
              ? 'API キー管理で有効なキーを登録してください。'
              : '提供元に接続できませんでした。時間をおいて再試行してください。'}
          </p>{/if}
        <dl>
          <div>
            <dt>最終試行（日本時間）</dt>
            <dd>{datetime(row.last_attempt_at)}</dd>
          </div>
          <div>
            <dt>最終成功（日本時間）</dt>
            <dd>{datetime(row.last_success_at)}</dd>
          </div>
          <div>
            <dt>次回予定（日本時間）</dt>
            <dd>{datetime(row.next_sync_at)}</dd>
          </div>
        </dl>
        {#if draft}<form onsubmit={(event) => submit(event, row)}>
            {#if draft.dirty && draft.baseline_version !== row.version}<p
                class="error"
                role="status"
              >
                編集中に保存状態が更新されました。入力は保持しています。最新の保存値を読み直してから、もう一度編集してください。
              </p>{/if}
            <label
              ><input
                type="checkbox"
                bind:checked={draft.values.automatic_enabled}
                oninput={() => {
                  draft.dirty = true;
                }}
                disabled={busy || row.provider === 'liquid'}
              />自動同期を有効にする</label
            >
            <label for={`sync-hours-${row.provider}`}
              >同期間隔（1〜168 時間）</label
            ><input
              id={`sync-hours-${row.provider}`}
              type="number"
              min="1"
              max="168"
              required
              bind:value={draft.values.interval_hours}
              oninput={() => {
                draft.dirty = true;
              }}
              disabled={busy || row.provider === 'liquid'}
            />
            {#if row.provider === 'anthropic'}<label for="anthropic-workspace"
                >Anthropic ワークスペース ID（任意）</label
              ><input
                id="anthropic-workspace"
                bind:value={draft.values.workspace_id}
                oninput={() => {
                  draft.dirty = true;
                }}
                disabled={busy}
              />{/if}
            <div class="actions">
              <button type="submit" disabled={busy || row.provider === 'liquid'}
                >同期設定を保存</button
              ><button
                type="button"
                disabled={busy ||
                  row.provider === 'liquid' ||
                  row.status === 'queued' ||
                  row.status === 'running'}
                onclick={() => request(row.provider)}>今すぐ同期を依頼</button
              >
            </div>
            <button
              type="button"
              disabled={busy || row.provider === 'liquid'}
              onclick={() => reloadDraft(row.provider)}
              >最新の同期設定を読み直す（入力を破棄）</button
            >
          </form>{/if}
      </article>
    {/each}
  </div>
</section>

<style>
  [hidden] {
    display: none !important;
  }
  td button {
    margin: 0;
  }
  @media (max-width: 720px) {
    table,
    tbody {
      display: block;
    }
  }
</style>
